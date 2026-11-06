//! The routes of the Web UI and its API (ADR-0028 §3, A-357, amendment
//! C-083):
//!
//! | Request | Answer |
//! |---|---|
//! | `GET /`, `/app.css`, `/app.js` | the Web UI (embedded files) |
//! | `GET /api/apps` | the applications, their Configs and whether writable |
//! | `POST /api/refresh` | discover again: `{}` every online one, or `{"serviceType", "app"}` |
//! | `POST /api/set` | a `Set Config`, waits for its Status event |
//! | `GET /api/sets` | the last `Set` results, newest first |
//! | `GET /api/events` | event stream: `{"type": "apps", …}` (as `GET /api/apps`) first and on every change, `{"type": "set", "set": …}` for every `Set` result |
//!
//! A query is refused (400): no route takes parameters.

use std::sync::Arc;

use serde_json::{Value, json};

use crate::directory::{ApiError, AppKey, SetRequest};
use crate::events::Hub;
use crate::http::{Request, Response};

const INDEX_HTML: &str = include_str!("../ui/index.html");
const APP_CSS: &str = include_str!("../ui/app.css");
const APP_JS: &str = include_str!("../ui/app.js");

/// What the routes need of the service.
pub(crate) trait Api {
    fn apps(&self) -> Value;
    fn sets(&self) -> Value;
    fn refresh(&self, key: Option<AppKey>) -> Result<Value, ApiError>;
    fn set(&self, req: &SetRequest) -> Result<(u16, Value), ApiError>;
}

impl Api for crate::service::Service {
    fn apps(&self) -> Value {
        Self::apps(self)
    }
    fn sets(&self) -> Value {
        Self::sets(self)
    }
    fn refresh(&self, key: Option<AppKey>) -> Result<Value, ApiError> {
        Self::refresh(self, key)
    }
    fn set(&self, req: &SetRequest) -> Result<(u16, Value), ApiError> {
        Self::set(self, req)
    }
}

/// The event `apps` of the snapshot `apps` (`GET /api/apps`).
pub(crate) fn apps_event(apps: &Value) -> String {
    let mut event = json!({ "type": "apps" });
    if let (Some(e), Some(a)) = (event.as_object_mut(), apps.as_object()) {
        for (k, v) in a {
            e.insert(k.clone(), v.clone());
        }
    }
    event.to_string()
}

fn set_event(record: &Value) -> String {
    json!({ "type": "set", "set": record }).to_string()
}

fn body(req: &Request) -> Result<Value, ApiError> {
    if req.body.is_empty() {
        return Ok(Value::Object(serde_json::Map::new()));
    }
    let v: Value = serde_json::from_slice(&req.body)
        .map_err(|e| ApiError::new(400, format!("invalid JSON: {e}")))?;
    if v.is_object() {
        Ok(v)
    } else {
        Err(ApiError::new(400, "the body is a JSON object"))
    }
}

fn refresh_key(body: &Value) -> Result<Option<AppKey>, ApiError> {
    let get = |k: &str| body.get(k).and_then(Value::as_str).map(str::to_owned);
    match (get("serviceType"), get("app")) {
        (Some(st), Some(app)) => Ok(Some((st, app))),
        (None, None) if body.get("serviceType").is_none() && body.get("app").is_none() => Ok(None),
        _ => Err(ApiError::new(
            400,
            "serviceType and app: both strings or none",
        )),
    }
}

fn result(r: Result<(u16, Value), ApiError>) -> Response {
    match r {
        Ok((status, v)) => Response::json(status, &v),
        Err(e) => Response::error(e.status, &e.message),
    }
}

fn not_allowed(allow: &str) -> Response {
    let mut r = Response::error(405, "method not allowed");
    r.headers.push(("Allow", allow.to_owned()));
    r
}

/// Answers one request; `hub` takes the events.
pub(crate) fn route(api: &dyn Api, hub: &Arc<Hub>, req: &Request) -> Response {
    if req.query.is_some() {
        return Response::error(400, "no query is supported");
    }
    match (req.path.as_str(), req.method.as_str()) {
        ("/", "GET") => Response::content(200, "text/html; charset=utf-8", INDEX_HTML.as_bytes()),
        ("/app.css", "GET") => {
            Response::content(200, "text/css; charset=utf-8", APP_CSS.as_bytes())
        }
        ("/app.js", "GET") => {
            Response::content(200, "text/javascript; charset=utf-8", APP_JS.as_bytes())
        }
        ("/api/events", "GET") => match hub.subscribe(apps_event(&api.apps())) {
            Some(sub) => Response::events(sub),
            None => Response::error(503, "too many event streams"),
        },
        ("/api/apps", "GET") => Response::json(200, &api.apps()),
        ("/api/sets", "GET") => Response::json(200, &api.sets()),
        ("/api/refresh", "POST") => result(
            body(req)
                .and_then(|b| refresh_key(&b))
                .and_then(|k| api.refresh(k))
                .map(|v| (200, v)),
        ),
        ("/api/set", "POST") => {
            let r = body(req)
                .and_then(|b| SetRequest::parse(&b))
                .and_then(|r| api.set(&r));
            if let Ok((_, record)) = &r {
                hub.send(&set_event(record));
            }
            result(r)
        }
        ("/" | "/app.css" | "/app.js" | "/api/apps" | "/api/sets" | "/api/events", _) => {
            not_allowed("GET")
        }
        ("/api/refresh" | "/api/set", _) => not_allowed("POST"),
        _ => Response::error(404, "not found"),
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Mutex;

    use serde_json::json;

    use super::*;
    use crate::events::{KEEP_ALIVE, MAX_STREAMS};

    #[derive(Default)]
    struct Fake {
        refreshed: Mutex<Vec<Option<AppKey>>>,
        set: Mutex<Vec<SetRequest>>,
    }

    impl Api for Fake {
        fn apps(&self) -> Value {
            json!({"apps": []})
        }
        fn sets(&self) -> Value {
            json!({"sets": []})
        }
        fn refresh(&self, key: Option<AppKey>) -> Result<Value, ApiError> {
            self.refreshed.lock().unwrap().push(key);
            Ok(self.apps())
        }
        fn set(&self, req: &SetRequest) -> Result<(u16, Value), ApiError> {
            self.set.lock().unwrap().push(req.clone());
            if req.app == "offline" {
                return Err(ApiError::new(409, "the application is offlineGraceful"));
            }
            Ok((200, json!({"id": 1})))
        }
    }

    fn req(method: &str, path: &str, body: &str) -> Request {
        Request {
            method: method.to_owned(),
            path: path.to_owned(),
            query: None,
            body: body.as_bytes().to_vec(),
        }
    }

    fn json_of(r: &Response) -> Value {
        serde_json::from_slice(&r.body).unwrap()
    }

    #[test]
    fn routes() {
        let fake = Fake::default();
        let hub = Hub::new(KEEP_ALIVE);
        let route = |api: &Fake, r: &Request| route(api, &hub, r);
        for (path, content_type, start) in [
            ("/", "text/html; charset=utf-8", "<!DOCTYPE html>"),
            ("/app.css", "text/css; charset=utf-8", ""),
            ("/app.js", "text/javascript; charset=utf-8", ""),
        ] {
            let r = route(&fake, &req("GET", path, ""));
            assert_eq!((r.status, r.content_type), (200, content_type), "{path}");
            assert!(r.body.starts_with(start.as_bytes()), "{path}");
        }
        let watcher = hub.subscribe(String::new()).unwrap();
        let r = route(&fake, &req("GET", "/api/events", ""));
        let sub = r.stream.unwrap();
        assert_eq!(
            serde_json::from_str::<Value>(&sub.first).unwrap(),
            json!({"type": "apps", "apps": []})
        );
        assert_eq!(
            json_of(&route(&fake, &req("GET", "/api/apps", ""))),
            json!({"apps": []})
        );
        assert_eq!(
            json_of(&route(&fake, &req("GET", "/api/sets", ""))),
            json!({"sets": []})
        );
        assert_eq!(route(&fake, &req("POST", "/api/refresh", "")).status, 200);
        assert_eq!(
            route(
                &fake,
                &req(
                    "POST",
                    "/api/refresh",
                    r#"{"serviceType":"Utility","app":"a/b/c/d"}"#
                )
            )
            .status,
            200
        );
        assert_eq!(
            *fake.refreshed.lock().unwrap(),
            vec![None, Some(("Utility".to_owned(), "a/b/c/d".to_owned()))]
        );
        let set = r#"{"serviceType":"Utility","app":"a","source":"a","context":"c","values":{"g":{"o":"v"}}}"#;
        let r = route(&fake, &req("POST", "/api/set", set));
        assert_eq!((r.status, json_of(&r)), (200, json!({"id": 1})));
        for s in [&watcher, &sub] {
            let event: Value = serde_json::from_str(&s.events.try_recv().unwrap()).unwrap();
            assert_eq!(event, json!({"type": "set", "set": {"id": 1}}));
        }
        let r = route(
            &fake,
            &req(
                "POST",
                "/api/set",
                &set.replace(r#""app":"a""#, r#""app":"offline""#),
            ),
        );
        assert_eq!(
            (r.status, json_of(&r)),
            (409, json!({"error": "the application is offlineGraceful"}))
        );
        assert_eq!(fake.set.lock().unwrap().len(), 2);
        assert!(watcher.events.try_recv().is_err(), "no event for an error");
    }

    #[test]
    fn event_streams_are_limited() {
        let fake = Fake::default();
        let hub = Hub::new(KEEP_ALIVE);
        let streams: Vec<Response> = (0..MAX_STREAMS)
            .map(|_| route(&fake, &hub, &req("GET", "/api/events", "")))
            .collect();
        assert!(streams.iter().all(|r| r.stream.is_some()));
        let r = route(&fake, &hub, &req("GET", "/api/events", ""));
        assert_eq!((r.status, r.stream.is_none()), (503, true));
        drop(streams);
        assert!(
            route(&fake, &hub, &req("GET", "/api/events", ""))
                .stream
                .is_some()
        );
    }

    #[test]
    fn refused() {
        let fake = Fake::default();
        let hub = Hub::new(KEEP_ALIVE);
        let route = |api: &Fake, r: &Request| route(api, &hub, r);
        for (method, path, body, status) in [
            ("GET", "/nope", "", 404),
            ("POST", "/api/apps", "", 405),
            ("GET", "/api/set", "", 405),
            ("POST", "/api/events", "{}", 405),
            ("POST", "/app.js", "{}", 405),
            ("POST", "/api/set", "", 400),
            ("POST", "/api/set", "[]", 400),
            ("POST", "/api/set", "{", 400),
            ("POST", "/api/set", r#"{"serviceType":"Utility"}"#, 400),
            ("POST", "/api/refresh", r#"{"app":"a"}"#, 400),
            (
                "POST",
                "/api/refresh",
                r#"{"serviceType":1,"app":"a"}"#,
                400,
            ),
        ] {
            let r = route(&fake, &req(method, path, body));
            assert_eq!(r.status, status, "{method} {path} {body}");
            assert!(json_of(&r)["error"].is_string());
        }
        for path in ["/api/apps", "/api/events", "/"] {
            let mut q = req("GET", path, "");
            q.query = Some("x=1".to_owned());
            assert_eq!(route(&fake, &q).status, 400, "{path}?x=1");
        }
        let r = route(&fake, &req("DELETE", "/api/set", ""));
        assert_eq!(r.headers, vec![("Allow", "POST".to_owned())]);
        assert!(fake.set.lock().unwrap().is_empty());
    }
}
