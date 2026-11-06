package demotest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// The common limits of the Web UIs of the demos (ADR-0028 §3, amendment
// C-083).
const (
	UIMaxConnections = 16
	UIMaxSSE         = 8
	UIMaxBody        = 64 << 10
	UIMaxHead        = 8 << 10
	// UIRequestTime is the time a client has to send its request.
	UIRequestTime = 5 * time.Second
	// UIKeepAliveEnv shortens the SSE keep-alive period of a demo (tests
	// only; default 15 s).
	UIKeepAliveEnv = "OEC_UI_KEEPALIVE_MS"
)

// UI is the Web UI of a demo listening on Addr (host:port).
type UI struct {
	Addr string
}

// UIAddr is a free loopback address for OEC_UI_LISTEN.
func UIAddr(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("127.0.0.1:%d", FreePort(t))
}

// WaitUI waits until the Web UI on addr accepts connections.
func WaitUI(t *testing.T, addr string) *UI {
	t.Helper()
	Eventually(t, "Web UI on "+addr, func() bool {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
		}
		return err == nil
	})
	return &UI{Addr: addr}
}

var uiClient = &http.Client{Timeout: Wait}

// Do sends a request (body "" = none, then without Content-Type) and
// returns the response with its body read.
func (u *UI) Do(t *testing.T, method, path, contentType, body string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://"+u.Addr+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := uiClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp, raw
}

// JSON gets path, expects 200 with a JSON body and decodes it into out.
func (u *UI) JSON(t *testing.T, path string, out any) {
	t.Helper()
	resp, raw := u.Do(t, "GET", path, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("GET %s: Content-Type %q", path, ct)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("GET %s: %v in %s", path, err, raw)
	}
}

// Raw sends request as text on a new connection and returns the status
// line of the answer.
func (u *UI) Raw(t *testing.T, request string) string {
	t.Helper()
	c, err := net.Dial("tcp", u.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(Wait))
	if _, err := io.WriteString(c, request); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("%q: %v", request, err)
	}
	return strings.TrimSpace(line)
}

// SSEItem is one line of an event stream that is not empty: an event with
// its data (Type is its member "type") or a comment.
type SSEItem struct {
	Comment string
	Type    string
	Data    map[string]any
	Raw     string
}

func (i SSEItem) String() string { return i.Raw }

// SSE is an open event stream GET /api/events.
type SSE struct {
	resp  *http.Response
	items *lines[SSEItem]
	once  sync.Once
}

// Events opens the event stream; it is closed at the end of t.
func (u *UI) Events(t *testing.T) *SSE {
	t.Helper()
	s, resp := u.TryEvents(t)
	if s == nil {
		t.Fatalf("GET /api/events: %s", resp)
	}
	return s
}

// TryEvents opens the event stream; nil and the status line if the UI
// refuses it.
func (u *UI) TryEvents(t *testing.T) (*SSE, string) {
	t.Helper()
	req, err := http.NewRequest("GET", "http://"+u.Addr+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil, resp.Status
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("event stream: Content-Type %q", ct)
	}
	s := &SSE{resp: resp}
	scan := bufio.NewScanner(resp.Body)
	scan.Buffer(make([]byte, 1<<20), 1<<24)
	s.items = readLines(scan, func(b []byte) SSEItem {
		line := string(b)
		item := SSEItem{Raw: line}
		switch {
		case strings.HasPrefix(line, ":"):
			item.Comment = strings.TrimSpace(line[1:])
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal(b[len("data: "):], &item.Data); err != nil {
				item.Type = "invalid JSON: " + err.Error()
			} else {
				item.Type, _ = item.Data["type"].(string)
			}
		case line != "":
			item.Type = "invalid line"
		}
		return item
	})
	t.Cleanup(s.Close)
	return s, resp.Status
}

// Close ends the stream.
func (s *SSE) Close() {
	s.once.Do(func() {
		s.resp.Body.Close()
		<-s.items.done
	})
}

// Len is the number of lines so far.
func (s *SSE) Len() int { return s.items.len() }

// All is a copy of the lines so far.
func (s *SSE) All() []SSEItem { return s.items.all() }

// Expect waits for a line from index from on that matches.
func (s *SSE) Expect(t *testing.T, from int, what string, match func(SSEItem) bool) (SSEItem, int) {
	t.Helper()
	return s.items.expect(t, from, "event: "+what, match)
}

// Event waits for an event of the type from index from on that matches
// (nil: any).
func (s *SSE) Event(t *testing.T, from int, typ string, match func(map[string]any) bool) (map[string]any, int) {
	t.Helper()
	item, i := s.Expect(t, from, typ, func(i SSEItem) bool {
		return i.Type == typ && (match == nil || match(i.Data))
	})
	return item.Data, i
}

// Absolute URLs and the CSS functions that load resources: a Web UI of a
// demo loads nothing from elsewhere (offline, ADR-0028 §3).
var (
	htmlResource = regexp.MustCompile(`(?i)<(script|link|img|iframe|source|embed|object|base|form)\b[^>]*>`)
	htmlURLAttr  = regexp.MustCompile(`(?i)\b(src|href|action|data|srcset)\s*=\s*"([^"]*)"`)
	absoluteURL  = regexp.MustCompile(`(?i)(https?:|wss?:|//[a-z0-9])`)
	cssLoad      = regexp.MustCompile(`(?i)(@import|url\s*\()`)
	jsLoad       = regexp.MustCompile(`(?i)(https?://|wss?://|import\s*\(|importScripts|XMLHttpRequest|WebSocket|EventSource\(\s*["'][a-z]+:)`)
	svgNamespace = `"http://www.w3.org/2000/svg"`
	jsFetch      = regexp.MustCompile(`\b(fetch|EventSource)\(\s*(["'` + "`" + `])([^"'` + "`" + `]*)`)
)

// CheckStatic checks the embedded files of a Web UI: GET / (HTML with the
// title part), /app.css and /app.js with their types and the common
// headers, and that they load nothing from elsewhere.
func (u *UI) CheckStatic(t *testing.T, title string) {
	t.Helper()
	files := map[string]string{
		"/":        "text/html; charset=utf-8",
		"/app.css": "text/css; charset=utf-8",
		"/app.js":  "text/javascript; charset=utf-8",
	}
	body := map[string]string{}
	for path, ct := range files {
		resp, raw := u.Do(t, "GET", path, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != ct {
			t.Errorf("GET %s: Content-Type %q, want %q", path, got, ct)
		}
		u.checkHeaders(t, "GET "+path, resp)
		body[path] = string(raw)
	}
	html := body["/"]
	if !strings.Contains(html, "<title>") || !strings.Contains(html, title) {
		t.Errorf("GET /: no title %q", title)
	}
	for _, tag := range htmlResource.FindAllString(html, -1) {
		for _, m := range htmlURLAttr.FindAllStringSubmatch(tag, -1) {
			if absoluteURL.MatchString(m[2]) {
				t.Errorf("GET /: external resource %s", tag)
			}
		}
	}
	for _, need := range []string{`src="/app.js"`, `href="/app.css"`} {
		if !strings.Contains(html, need) {
			t.Errorf("GET /: no %s", need)
		}
	}
	if m := cssLoad.FindString(body["/app.css"]); m != "" {
		t.Errorf("app.css loads a resource: %s", m)
	}
	// The SVG namespace is a name, nothing is loaded from it.
	if m := jsLoad.FindString(strings.ReplaceAll(body["/app.js"], svgNamespace, "")); m != "" {
		t.Errorf("app.js loads from elsewhere: %s", m)
	}
	for _, m := range jsFetch.FindAllStringSubmatch(body["/app.js"], -1) {
		if !strings.HasPrefix(m[3], "/api/") {
			t.Errorf("app.js: %s outside /api/", m[0])
		}
	}
}

func (u *UI) checkHeaders(t *testing.T, what string, resp *http.Response) {
	t.Helper()
	for k, v := range map[string]string{
		"Cache-Control":           "no-store",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'",
		"Referrer-Policy":         "no-referrer",
	} {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("%s: %s %q, want %q", what, k, got, v)
		}
	}
}

// CheckGuards checks the request guards of a Web UI without
// authentication (A-305, A-357): other hosts and sites, Transfer-Encoding,
// a POST without JSON, too large bodies and heads, unknown paths and
// methods. path is a JSON resource of the UI (GET).
func (u *UI) CheckGuards(t *testing.T, path string) {
	t.Helper()
	h := "Host: " + u.Addr + "\r\n"
	big := strings.Repeat("a", 2*UIMaxHead+4096)
	for request, want := range map[string]string{
		"GET " + path + " HTTP/1.1\r\n" + h + "\r\n":                                                                          "200",
		"GET " + path + " HTTP/1.1\r\nHost: localhost\r\n\r\n":                                                                "200",
		"GET " + path + " HTTP/1.1\r\nHost: demo.example\r\n\r\n":                                                             "403",
		"GET " + path + " HTTP/1.1\r\nHost: localhost.example:80\r\n\r\n":                                                     "403",
		"GET " + path + " HTTP/1.1\r\n" + h + "Origin: http://demo.example\r\n\r\n":                                           "403",
		"GET " + path + " HTTP/1.1\r\n" + h + "Origin: http://" + u.Addr + "\r\n\r\n":                                         "200",
		"POST /api/x HTTP/1.1\r\n" + h + "Transfer-Encoding: chunked\r\nContent-Type: application/json\r\n\r\n0\r\n\r\n":      "501",
		"POST /api/x HTTP/1.1\r\n" + h + "Content-Type: text/plain\r\nContent-Length: 2\r\n\r\n{}":                            "415",
		fmt.Sprintf("POST /api/x HTTP/1.1\r\n%sContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", h, UIMaxBody+1): "413",
		"GET " + path + " HTTP/1.1\r\n" + h + "X-Big: " + big + "\r\n\r\n":                                                    "431",
		"GET /nope HTTP/1.1\r\n" + h + "\r\n":                                                                                 "404",
		"DELETE / HTTP/1.1\r\n" + h + "\r\n":                                                                                  "405",
	} {
		got := u.Raw(t, request)
		if f := strings.Fields(got); len(f) < 2 || f[0] != "HTTP/1.1" || f[1] != want {
			if len(request) > 120 {
				request = request[:120] + "…"
			}
			t.Errorf("%q: %q, want %s", request, got, want)
		}
	}
	resp, _ := u.Do(t, "DELETE", "/", "", "")
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") == "" {
		t.Errorf("DELETE /: %d, Allow %q", resp.StatusCode, resp.Header.Get("Allow"))
	}
	resp, _ = u.Do(t, "GET", path, "", "")
	u.checkHeaders(t, "GET "+path, resp)
}

// CheckLimits checks the limits of the event streams (at most UIMaxSSE,
// 503 beyond, a place frees when a client goes) and of the connections
// (at most UIMaxConnections, 503 beyond). The first event of every
// stream has the type first. Streams of earlier checks may still hold a
// place until the UI notices their end: every step retries.
func (u *UI) CheckLimits(t *testing.T, first string) {
	t.Helper()
	var streams []*SSE
	closeAll := func() {
		for _, s := range streams {
			s.Close()
		}
		streams = nil
	}
	// Streams closed before may still count until the server notices.
	Eventually(t, fmt.Sprintf("%d event streams", UIMaxSSE), func() bool {
		for len(streams) < UIMaxSSE {
			s, _ := u.TryEvents(t)
			if s == nil {
				time.Sleep(50 * time.Millisecond)
				return false
			}
			s.Event(t, 0, first, nil)
			streams = append(streams, s)
		}
		return true
	})
	if s, status := u.TryEvents(t); s != nil || !strings.HasPrefix(status, "503") {
		t.Errorf("event stream %d: %s", UIMaxSSE+1, status)
	}
	streams[0].Close()
	streams = streams[1:]
	Eventually(t, "a free event stream after a close", func() bool {
		s, _ := u.TryEvents(t)
		if s != nil {
			streams = append(streams, s)
		}
		return s != nil
	})
	closeAll()

	// Idle connections up to the limit (none refused), then 503.
	Eventually(t, fmt.Sprintf("%d idle connections accepted", UIMaxConnections), func() bool {
		var conns []net.Conn
		defer func() {
			for _, c := range conns {
				c.Close()
			}
		}()
		for i := 0; i < UIMaxConnections; i++ {
			c, err := net.Dial("tcp", u.Addr)
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, c)
		}
		// A refused connection gets 503 at once; connections closed
		// before may still count until the server notices (then retry).
		time.Sleep(300 * time.Millisecond)
		for _, c := range conns {
			_ = c.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
			if n, _ := c.Read(make([]byte, 1)); n > 0 {
				time.Sleep(50 * time.Millisecond)
				return false
			}
		}
		got := u.Raw(t, "GET / HTTP/1.1\r\nHost: "+u.Addr+"\r\n\r\n")
		if !strings.HasPrefix(got, "HTTP/1.1 503") {
			t.Errorf("connection %d: %q", UIMaxConnections+1, got)
		}
		return true
	})
}

// CheckSlow checks the time limit of a request (UIRequestTime): a client
// that does not finish its head is answered with 408 or, by net/http,
// disconnected without an answer – not earlier than half of the limit and
// not much later than the limit.
func (u *UI) CheckSlow(t *testing.T) {
	t.Helper()
	c, err := net.Dial("tcp", u.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	if _, err := c.Write([]byte("GET / HTTP/1.1\r\nHost: " + u.Addr + "\r\n")); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(start.Add(UIRequestTime + 3*time.Second))
	got, err := io.ReadAll(c)
	took := time.Since(start)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("slow request: still open after %v", took)
	}
	if len(got) > 0 && !strings.HasPrefix(string(got), "HTTP/1.1 408") {
		t.Errorf("slow request: %q", got)
	}
	if took < UIRequestTime/2 {
		t.Errorf("slow request: ended after %v, limit %v", took, UIRequestTime)
	}
}

// CheckKeepAlive checks that an event stream gets the comment keep-alive
// (the UI runs with UIKeepAliveEnv set short).
func (u *UI) CheckKeepAlive(t *testing.T) {
	t.Helper()
	s := u.Events(t)
	defer s.Close()
	s.Expect(t, 0, "keep-alive comment", func(i SSEItem) bool { return i.Comment == "keep-alive" })
	for _, i := range s.All() {
		if strings.HasPrefix(i.Type, "invalid") {
			t.Errorf("event stream: %s: %q", i.Type, i.Raw)
		}
	}
}
