//! A minimal HTTP/1.1 server on `std::net` for the Web UI and the API of
//! the config service (ADR-0028 §3, A-357): one request per connection
//! (`Connection: close`), bounded in size, time and number of connections;
//! a response may be an event stream (`text/event-stream`). Without TLS and
//! authentication, like every demo UI (A-305): it listens on loopback by
//! default and refuses requests whose `Host` is a DNS name other than
//! `localhost` or whose `Origin` is another site (DNS rebinding, CSRF).

use std::collections::HashMap;
use std::io::{Read, Write};
use std::net::{IpAddr, TcpListener, TcpStream};
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc::RecvTimeoutError;
use std::time::{Duration, Instant};

use crate::events::Subscription;

/// At most this many connections at a time; more get a 503.
pub(crate) const MAX_CONNECTIONS: usize = 16;
/// Request line and headers.
pub(crate) const MAX_HEAD: usize = 8 * 1024;
/// Body.
pub(crate) const MAX_BODY: usize = 64 * 1024;
/// Time to receive a request.
const READ_TIME: Duration = Duration::from_secs(5);
/// Time to drop what a client sends after the response.
const DRAIN_TIME: Duration = Duration::from_millis(100);
/// How often an event stream looks for its client having gone.
const POLL_TIME: Duration = Duration::from_millis(250);
/// The headers of every response.
const COMMON_HEADERS: &str = "Connection: close\r\nCache-Control: no-store\r\n\
X-Content-Type-Options: nosniff\r\n\
Content-Security-Policy: default-src 'self'; frame-ancestors 'none'\r\n\
Referrer-Policy: no-referrer\r\n";

/// A request.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct Request {
    pub(crate) method: String,
    /// The path without query.
    pub(crate) path: String,
    /// The query (after `?`), `None` without `?`.
    pub(crate) query: Option<String>,
    pub(crate) body: Vec<u8>,
}

/// A response.
#[derive(Debug)]
pub(crate) struct Response {
    pub(crate) status: u16,
    pub(crate) content_type: &'static str,
    pub(crate) body: Vec<u8>,
    /// Extra headers, e.g. `Allow`.
    pub(crate) headers: Vec<(&'static str, String)>,
    /// An event stream instead of the body.
    pub(crate) stream: Option<Box<Subscription>>,
}

impl Response {
    /// A JSON response.
    pub(crate) fn json(status: u16, value: &serde_json::Value) -> Self {
        Self {
            status,
            content_type: "application/json",
            body: value.to_string().into_bytes(),
            headers: Vec::new(),
            stream: None,
        }
    }

    /// An error as `{"error": message}`.
    pub(crate) fn error(status: u16, message: &str) -> Self {
        Self::json(status, &serde_json::json!({ "error": message }))
    }

    /// A response of the type `content_type`.
    pub(crate) fn content(status: u16, content_type: &'static str, body: &[u8]) -> Self {
        Self {
            status,
            content_type,
            body: body.to_vec(),
            headers: Vec::new(),
            stream: None,
        }
    }

    /// The event stream of `subscription`.
    pub(crate) fn events(subscription: Subscription) -> Self {
        Self {
            status: 200,
            content_type: "text/event-stream",
            body: Vec::new(),
            headers: Vec::new(),
            stream: Some(Box::new(subscription)),
        }
    }
}

fn reason(status: u16) -> &'static str {
    match status {
        200 => "OK",
        400 => "Bad Request",
        403 => "Forbidden",
        404 => "Not Found",
        405 => "Method Not Allowed",
        408 => "Request Timeout",
        409 => "Conflict",
        413 => "Content Too Large",
        415 => "Unsupported Media Type",
        431 => "Request Header Fields Too Large",
        501 => "Not Implemented",
        503 => "Service Unavailable",
        504 => "Gateway Timeout",
        _ => "Error",
    }
}

/// The handler of the requests.
pub(crate) type Handler = Arc<dyn Fn(&Request) -> Response + Send + Sync>;

/// Serves `listener` until it fails; one thread per connection.
pub(crate) fn serve(listener: &TcpListener, handler: &Handler) {
    let open = Arc::new(AtomicUsize::new(0));
    for stream in listener.incoming() {
        let Ok(mut stream) = stream else { continue };
        if open.fetch_add(1, Ordering::SeqCst) >= MAX_CONNECTIONS {
            open.fetch_sub(1, Ordering::SeqCst);
            let _ = stream.set_write_timeout(Some(READ_TIME));
            let _ = write(&mut stream, &Response::error(503, "too many connections"));
            finish(&mut stream);
            continue;
        }
        let (open, handler) = (Arc::clone(&open), Arc::clone(handler));
        let spawned = std::thread::Builder::new()
            .name("oec-config-http".to_owned())
            .spawn(move || {
                connection(stream, &handler);
                open.fetch_sub(1, Ordering::SeqCst);
            });
        if spawned.is_err() {
            log::warn!("HTTP: no thread for a connection");
        }
    }
}

fn connection(mut stream: TcpStream, handler: &Handler) {
    let _ = stream.set_read_timeout(Some(READ_TIME));
    let _ = stream.set_write_timeout(Some(READ_TIME));
    let mut response = match read(&mut stream) {
        Ok(req) => handler(&req),
        Err(resp) => resp,
    };
    match response.stream.take() {
        Some(sub) => stream_events(&mut stream, &response, &sub),
        None => {
            let _ = write(&mut stream, &response);
        }
    }
    finish(&mut stream);
}

/// Sends the events of `sub` as `data:` lines until the client goes, a
/// write fails or the hub drops the subscriber; a comment `keep-alive`
/// after [`Subscription::keep_alive`] without an event.
fn stream_events(stream: &mut TcpStream, r: &Response, sub: &Subscription) {
    let head = format!(
        "HTTP/1.1 {} {}\r\nContent-Type: {}\r\n{COMMON_HEADERS}\r\n",
        r.status,
        reason(r.status),
        r.content_type
    );
    if send(stream, head.as_bytes()).is_err() || send(stream, &event(&sub.first)).is_err() {
        return;
    }
    let mut quiet = Instant::now();
    loop {
        let sent = match sub.events.recv_timeout(POLL_TIME) {
            Ok(data) => send(stream, &event(&data)),
            Err(RecvTimeoutError::Disconnected) => return,
            Err(RecvTimeoutError::Timeout) if quiet.elapsed() >= sub.keep_alive() => {
                send(stream, b": keep-alive\n\n")
            }
            Err(RecvTimeoutError::Timeout) => {
                if gone(stream) {
                    return;
                }
                continue;
            }
        };
        if sent.is_err() {
            return;
        }
        quiet = Instant::now();
    }
}

fn event(data: &str) -> Vec<u8> {
    format!("data: {data}\n\n").into_bytes()
}

fn send(stream: &mut TcpStream, b: &[u8]) -> std::io::Result<()> {
    stream.write_all(b)?;
    stream.flush()
}

/// Whether the client of an event stream has closed its side (what it
/// still sends is dropped).
fn gone(stream: &mut TcpStream) -> bool {
    if stream.set_nonblocking(true).is_err() {
        return true;
    }
    let mut sink = [0u8; 512];
    let closed = match stream.read(&mut sink) {
        Ok(0) => true,
        Ok(_) => false,
        Err(e) => e.kind() != std::io::ErrorKind::WouldBlock,
    };
    stream.set_nonblocking(false).is_err() || closed
}

/// Closes after the response: what the client still sends is read and
/// dropped for a short time, so that the close does not reset the
/// connection before the client has read the response.
fn finish(stream: &mut TcpStream) {
    let _ = stream.shutdown(std::net::Shutdown::Write);
    let _ = stream.set_read_timeout(Some(DRAIN_TIME));
    let mut sink = [0u8; 4096];
    let mut left = MAX_HEAD + MAX_BODY;
    while left > 0 {
        match stream.read(&mut sink) {
            Ok(0) | Err(_) => break,
            Ok(n) => left = left.saturating_sub(n),
        }
    }
}

fn write(stream: &mut TcpStream, r: &Response) -> std::io::Result<()> {
    let mut head = format!(
        "HTTP/1.1 {} {}\r\nContent-Type: {}\r\nContent-Length: {}\r\n{COMMON_HEADERS}",
        r.status,
        reason(r.status),
        r.content_type,
        r.body.len()
    );
    for (name, value) in &r.headers {
        use std::fmt::Write as _;
        let _ = write!(head, "{name}: {value}\r\n");
    }
    head.push_str("\r\n");
    stream.write_all(head.as_bytes())?;
    stream.write_all(&r.body)?;
    stream.flush()
}

fn find(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack.windows(needle.len()).position(|w| w == needle)
}

/// Reads one request; an error response for everything refused.
fn read(stream: &mut impl Read) -> Result<Request, Response> {
    let deadline = Instant::now() + READ_TIME;
    let mut buf = Vec::new();
    let mut chunk = [0u8; 4096];
    let head_end = loop {
        if let Some(i) = find(&buf, b"\r\n\r\n") {
            break i;
        }
        if buf.len() > MAX_HEAD {
            return Err(Response::error(431, "request head too large"));
        }
        let n = fill(stream, &mut chunk, deadline)?;
        buf.extend_from_slice(&chunk[..n]);
    };
    if head_end > MAX_HEAD {
        return Err(Response::error(431, "request head too large"));
    }
    let head = std::str::from_utf8(&buf[..head_end])
        .map_err(|_| Response::error(400, "request head is not UTF-8"))?;
    let mut lines = head.split("\r\n");
    let line = lines.next().unwrap_or_default();
    let mut parts = line.split(' ');
    let (Some(method), Some(target), Some(version), None) =
        (parts.next(), parts.next(), parts.next(), parts.next())
    else {
        return Err(Response::error(400, "invalid request line"));
    };
    if !version.starts_with("HTTP/1.") || !target.starts_with('/') {
        return Err(Response::error(400, "invalid request line"));
    }
    let mut headers = HashMap::new();
    for l in lines {
        let (name, value) = l
            .split_once(':')
            .ok_or_else(|| Response::error(400, "invalid header"))?;
        if headers
            .insert(name.trim().to_ascii_lowercase(), value.trim().to_owned())
            .is_some()
        {
            return Err(Response::error(400, "repeated header"));
        }
    }
    check_headers(method, &headers)?;
    let length = match headers.get("content-length") {
        None => 0,
        Some(v) => v
            .parse::<usize>()
            .map_err(|_| Response::error(400, "invalid Content-Length"))?,
    };
    if length > MAX_BODY {
        return Err(Response::error(413, "body too large"));
    }
    let mut body = buf[head_end + 4..].to_vec();
    while body.len() < length {
        let n = fill(stream, &mut chunk, deadline)?;
        body.extend_from_slice(&chunk[..n]);
    }
    if body.len() != length {
        return Err(Response::error(400, "body longer than Content-Length"));
    }
    let (path, query) = match target.split_once('?') {
        Some((path, query)) => (path.to_owned(), Some(query.to_owned())),
        None => (target.to_owned(), None),
    };
    Ok(Request {
        method: method.to_owned(),
        path,
        query,
        body,
    })
}

fn fill(stream: &mut impl Read, chunk: &mut [u8], deadline: Instant) -> Result<usize, Response> {
    if Instant::now() > deadline {
        return Err(Response::error(408, "request too slow"));
    }
    match stream.read(chunk) {
        Ok(0) => Err(Response::error(400, "incomplete request")),
        Ok(n) => Ok(n),
        Err(_) => Err(Response::error(408, "request too slow")),
    }
}

/// The `Host` is `localhost` or an IP address; an `Origin` is the same
/// site; no `Transfer-Encoding`; a `POST` carries JSON.
fn check_headers(method: &str, headers: &HashMap<String, String>) -> Result<(), Response> {
    let host = headers
        .get("host")
        .ok_or_else(|| Response::error(400, "Host is required"))?;
    if !host_allowed(host) {
        return Err(Response::error(
            403,
            "Host is not localhost or an IP address",
        ));
    }
    if let Some(origin) = headers.get("origin")
        && *origin != format!("http://{host}")
    {
        return Err(Response::error(403, "cross-origin request"));
    }
    if headers.contains_key("transfer-encoding") {
        return Err(Response::error(501, "Transfer-Encoding is not supported"));
    }
    if method == "POST" {
        let json = headers
            .get("content-type")
            .and_then(|c| c.split(';').next())
            .is_some_and(|c| c.trim().eq_ignore_ascii_case("application/json"));
        if !json {
            return Err(Response::error(
                415,
                "Content-Type application/json is required",
            ));
        }
    }
    Ok(())
}

/// `localhost` or an IP literal, with an optional port.
fn host_allowed(host: &str) -> bool {
    let name = if let Some(rest) = host.strip_prefix('[') {
        match rest.split_once(']') {
            Some((ip, port)) if port.is_empty() || valid_port(port) => {
                return ip.parse::<std::net::Ipv6Addr>().is_ok();
            }
            _ => return false,
        }
    } else {
        match host.rsplit_once(':') {
            Some((name, port)) if valid_port(&format!(":{port}")) => name,
            Some(_) => return false,
            None => host,
        }
    };
    name.eq_ignore_ascii_case("localhost") || name.parse::<IpAddr>().is_ok()
}

fn valid_port(p: &str) -> bool {
    p.strip_prefix(':')
        .is_some_and(|p| !p.is_empty() && p.len() <= 5 && p.bytes().all(|b| b.is_ascii_digit()))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parse(raw: &str) -> Result<Request, u16> {
        read(&mut raw.as_bytes()).map_err(|r| r.status)
    }

    #[test]
    fn requests() {
        let r = parse("GET /api/apps?x=1 HTTP/1.1\r\nHost: 127.0.0.1:8783\r\n\r\n").unwrap();
        assert_eq!(
            (r.method.as_str(), r.path.as_str(), r.query.as_deref()),
            ("GET", "/api/apps", Some("x=1"))
        );
        let r = parse(
            "POST /api/set HTTP/1.1\r\nHost: localhost:8783\r\nOrigin: http://localhost:8783\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: 2\r\n\r\n{}",
        )
        .unwrap();
        assert_eq!((r.body.as_slice(), r.query), (&b"{}"[..], None));
    }

    #[test]
    fn refused_requests() {
        let big = format!(
            "POST /api/set HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n",
            MAX_BODY + 1
        );
        let long_head = format!(
            "GET / HTTP/1.1\r\nHost: localhost\r\nX: {}\r\n\r\n",
            "a".repeat(MAX_HEAD)
        );
        for (raw, status) in [
            ("GET / HTTP/1.1\r\n\r\n", 400),
            ("GET / HTTP/1.1\r\nHost: evil.example:8783\r\n\r\n", 403),
            (
                "GET / HTTP/1.1\r\nHost: localhost\r\nOrigin: http://evil.example\r\n\r\n",
                403,
            ),
            (
                "GET / HTTP/1.1\r\nHost: localhost\r\nOrigin: null\r\n\r\n",
                403,
            ),
            (
                "POST /api/set HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\n\r\n{}",
                415,
            ),
            (
                "POST /api/set HTTP/1.1\r\nHost: localhost\r\nContent-Type: text/plain\r\nContent-Length: 2\r\n\r\n{}",
                415,
            ),
            (
                "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n",
                501,
            ),
            (big.as_str(), 413),
            (long_head.as_str(), 431),
            ("GET / HTTP/1.1\r\nHost: a\r\nHost: b\r\n\r\n", 400),
            ("GET http://x/ HTTP/1.1\r\nHost: localhost\r\n\r\n", 400),
            (
                "GET / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\n\r\nab",
                400,
            ),
            ("GET / HTTP/1.1\r\nHost: localhost\r\n", 400),
        ] {
            assert_eq!(parse(raw).unwrap_err(), status, "{raw:?}");
        }
    }

    #[test]
    fn hosts() {
        for (host, ok) in [
            ("localhost", true),
            ("LOCALHOST:8783", true),
            ("127.0.0.1", true),
            ("192.168.1.5:8783", true),
            ("[::1]:8783", true),
            ("[::1]", true),
            ("example.com", false),
            ("localhost.example.com", false),
            ("localhost:abc", false),
            ("[::1]x", false),
            ("[nope]", false),
        ] {
            assert_eq!(host_allowed(host), ok, "{host}");
        }
    }

    #[test]
    fn event_streams() {
        use crate::events::Hub;
        use std::io::{BufRead, BufReader};

        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        let hub = Hub::new(Duration::from_millis(50));
        let served = Arc::clone(&hub);
        let handler: Handler = Arc::new(move |_: &Request| {
            Response::events(served.subscribe("first".to_owned()).unwrap())
        });
        std::thread::spawn(move || serve(&listener, &handler));
        let mut s = TcpStream::connect(addr).unwrap();
        s.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
        write!(s, "GET /api/events HTTP/1.1\r\nHost: {addr}\r\n\r\n").unwrap();
        let mut lines = BufReader::new(s.try_clone().unwrap()).lines();
        let mut next = || lines.next().unwrap().unwrap();
        assert_eq!(next(), "HTTP/1.1 200 OK");
        let mut head = Vec::new();
        loop {
            let l = next();
            if l.is_empty() {
                break;
            }
            head.push(l);
        }
        assert!(head.contains(&"Content-Type: text/event-stream".to_owned()));
        assert!(head.contains(&"Cache-Control: no-store".to_owned()));
        assert!(!head.iter().any(|h| h.starts_with("Content-Length")));
        assert_eq!((next(), next()), ("data: first".to_owned(), String::new()));
        hub.send("{\"a\":1}");
        let mut got = Vec::new();
        while got.len() < 2 {
            let l = next();
            if !l.is_empty() {
                got.push(l);
            }
        }
        assert_eq!(got[0], "data: {\"a\":1}");
        assert_eq!(got[1], ": keep-alive");
        assert_eq!(hub.len(), 1);
        drop(s);
        drop(lines);
        let mut gone = false;
        for _ in 0..40 {
            std::thread::sleep(Duration::from_millis(50));
            if hub.len() == 0 {
                gone = true;
                break;
            }
        }
        assert!(gone, "the stream ends when the client goes");
    }

    #[test]
    fn served_over_loopback_with_a_connection_limit() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        let handler: Handler = Arc::new(|r: &Request| {
            Response::content(200, "text/plain; charset=utf-8", r.path.as_bytes())
        });
        std::thread::spawn(move || serve(&listener, &handler));
        let get = |path: &str| {
            let mut s = TcpStream::connect(addr).unwrap();
            write!(s, "GET {path} HTTP/1.1\r\nHost: {addr}\r\n\r\n").unwrap();
            let mut out = Vec::new();
            let _ = s.read_to_end(&mut out);
            String::from_utf8(out).unwrap()
        };
        let out = get("/hello");
        assert!(out.starts_with("HTTP/1.1 200 OK\r\n"), "{out}");
        assert!(out.contains("Cache-Control: no-store\r\n"));
        assert!(out.contains("X-Content-Type-Options: nosniff\r\n"));
        assert!(out.contains("Referrer-Policy: no-referrer\r\n"));
        assert!(
            out.contains("Content-Security-Policy: default-src 'self'; frame-ancestors 'none'\r\n")
        );
        assert!(out.ends_with("\r\n\r\n/hello"));
        // idle connections occupy the slots
        let idle: Vec<TcpStream> = (0..MAX_CONNECTIONS)
            .map(|_| TcpStream::connect(addr).unwrap())
            .collect();
        std::thread::sleep(Duration::from_millis(200));
        assert!(get("/x").starts_with("HTTP/1.1 503 "));
        drop(idle);
        let mut ok = false;
        for _ in 0..50 {
            std::thread::sleep(Duration::from_millis(100));
            if get("/x").starts_with("HTTP/1.1 200 ") {
                ok = true;
                break;
            }
        }
        assert!(ok, "the slots are free again");
    }
}
