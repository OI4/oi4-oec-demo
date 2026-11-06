//! Settings of the config service (A-304, ADR-0028 §2): not on the bus, so
//! no OEC `Config` of its own (A-354). They come from an optional JSON file
//! and the environment; the broker comes from the container storage
//! (`broker.json`, REQ-CNT-003) through the SDK.
//!
//! The file is `<root>/etc/oi4/app/config-service.json` or the path in
//! `OEC_CONFIG_FILE`:
//!
//! ```json
//! {
//!   "listen": "127.0.0.1:8783",
//!   "requestTimeoutSeconds": 5,
//!   "apps": ["Utility/oi4.org/OEC,20Logger/OEC-LOGGER/logger-1"],
//!   "setHistory": 50
//! }
//! ```
//!
//! `OEC_UI_LISTEN` overrides `listen` (empty: no Web UI and no HTTP API).

use std::path::{Path, PathBuf};
use std::time::Duration;

use oec::ServiceType;
use oec::identity::IdentificationLink;
use serde_json::Value;

/// The address of the Web UI without file and environment (ADR-0028 §3: port 8783,
/// loopback; the container sets `OEC_UI_LISTEN=0.0.0.0:8783`).
pub(crate) const DEFAULT_LISTEN: &str = "127.0.0.1:8783";

/// The settings.
#[derive(Debug, Clone, PartialEq)]
pub(crate) struct Settings {
    /// Address of the Web UI and its HTTP API; `None`: off.
    pub(crate) listen: Option<String>,
    /// Timeout of every request on the bus.
    pub(crate) request_timeout: Duration,
    /// Applications asked with `Get MAM` at the start (A-355), as
    /// ServiceType and AppId.
    pub(crate) apps: Vec<(ServiceType, IdentificationLink)>,
    /// Number of `Set` results kept for the API.
    pub(crate) set_history: usize,
}

impl Default for Settings {
    fn default() -> Self {
        Self {
            listen: Some(DEFAULT_LISTEN.to_owned()),
            request_timeout: Duration::from_secs(5),
            apps: Vec::new(),
            set_history: 50,
        }
    }
}

impl Settings {
    /// The settings of the container storage below `root`: the file (if it
    /// exists), then the environment `env` (a lookup, for tests).
    ///
    /// # Errors
    ///
    /// A message for an unreadable or invalid file.
    pub(crate) fn load(root: &Path, env: impl Fn(&str) -> Option<String>) -> Result<Self, String> {
        let (path, explicit) = match env("OEC_CONFIG_FILE") {
            Some(p) if !p.is_empty() => (PathBuf::from(p), true),
            _ => (root.join("etc/oi4/app/config-service.json"), false),
        };
        let mut settings = match std::fs::read_to_string(&path) {
            Ok(text) => Self::parse(&text).map_err(|e| format!("{}: {e}", path.display()))?,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound && !explicit => Self::default(),
            Err(e) => return Err(format!("{}: {e}", path.display())),
        };
        if let Some(listen) = env("OEC_UI_LISTEN") {
            settings.listen = (!listen.is_empty()).then_some(listen);
        }
        Ok(settings)
    }

    /// The settings of a file; missing keys keep their default, unknown
    /// keys are an error (a typo should not pass silently).
    ///
    /// # Errors
    ///
    /// A message naming the key.
    pub(crate) fn parse(text: &str) -> Result<Self, String> {
        let value: Value = serde_json::from_str(text).map_err(|e| e.to_string())?;
        let Value::Object(map) = value else {
            return Err("not a JSON object".to_owned());
        };
        let mut s = Self::default();
        for (key, v) in &map {
            match key.as_str() {
                "listen" => {
                    let listen = v.as_str().ok_or("listen: not a string")?;
                    s.listen = (!listen.is_empty()).then(|| listen.to_owned());
                }
                "requestTimeoutSeconds" => {
                    let secs = v
                        .as_u64()
                        .filter(|n| (1..=60).contains(n))
                        .ok_or("requestTimeoutSeconds: a whole number 1 … 60")?;
                    s.request_timeout = Duration::from_secs(secs);
                }
                "apps" => {
                    let list = v.as_array().ok_or("apps: not an array")?;
                    s.apps = list
                        .iter()
                        .map(|a| a.as_str().ok_or("apps: not a string".to_owned()))
                        .map(|a| a.and_then(parse_app))
                        .collect::<Result<_, _>>()?;
                }
                "setHistory" => {
                    let n = v
                        .as_u64()
                        .filter(|n| (1..=1000).contains(n))
                        .ok_or("setHistory: a whole number 1 … 1000")?;
                    s.set_history = usize::try_from(n).map_err(|e| e.to_string())?;
                }
                other => return Err(format!("unknown key {other:?}")),
            }
        }
        Ok(s)
    }
}

/// `<ServiceType>/<AppId>`, e.g. `Utility/oi4.org/OEC,20Logger/OEC-LOGGER/logger-1`
/// (the PublisherId form, REQ-MSG-006).
///
/// # Errors
///
/// A message for an unknown ServiceType or an invalid AppId.
pub(crate) fn parse_app(text: &str) -> Result<(ServiceType, IdentificationLink), String> {
    let (st, app) = text
        .split_once('/')
        .ok_or_else(|| format!("{text:?}: not <ServiceType>/<AppId>"))?;
    let st: ServiceType = st
        .parse()
        .map_err(|_| format!("{text:?}: unknown ServiceType {st:?}"))?;
    let app = IdentificationLink::parse(app).map_err(|e| format!("{text:?}: {e}"))?;
    Ok((st, app))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn no_env(_: &str) -> Option<String> {
        None
    }

    #[test]
    fn defaults_without_file() {
        let root = std::env::temp_dir().join("oec-config-test-none");
        assert_eq!(Settings::load(&root, no_env).unwrap(), Settings::default());
        assert_eq!(
            Settings::default().listen.as_deref(),
            Some("127.0.0.1:8783")
        );
    }

    #[test]
    fn file_and_environment() {
        let dir = std::env::temp_dir().join(format!("oec-config-test-{}", std::process::id()));
        let file = dir.join("etc/oi4/app/config-service.json");
        std::fs::create_dir_all(file.parent().unwrap()).unwrap();
        std::fs::write(
            &file,
            r#"{"listen": "127.0.0.1:1", "requestTimeoutSeconds": 2, "setHistory": 3,
                "apps": ["Utility/oi4.org/OEC,20Logger/OEC-LOGGER/logger-1"]}"#,
        )
        .unwrap();
        let s = Settings::load(&dir, no_env).unwrap();
        assert_eq!(s.listen.as_deref(), Some("127.0.0.1:1"));
        assert_eq!(s.request_timeout, Duration::from_secs(2));
        assert_eq!(s.set_history, 3);
        assert_eq!(s.apps[0].0, ServiceType::Utility);
        assert_eq!(
            s.apps[0].1.as_str(),
            "oi4.org/OEC,20Logger/OEC-LOGGER/logger-1"
        );
        let off = Settings::load(&dir, |k| (k == "OEC_UI_LISTEN").then(String::new)).unwrap();
        assert_eq!(off.listen, None);
        let other = Settings::load(&dir, |k| {
            (k == "OEC_CONFIG_FILE").then(|| dir.join("missing.json").display().to_string())
        });
        assert!(other.unwrap_err().contains("missing.json"));
        std::fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn invalid_files() {
        for (text, part) in [
            ("[]", "not a JSON object"),
            (r#"{"listn": ""}"#, "unknown key"),
            (r#"{"requestTimeoutSeconds": 0}"#, "requestTimeoutSeconds"),
            (r#"{"requestTimeoutSeconds": 61}"#, "requestTimeoutSeconds"),
            (r#"{"setHistory": 0}"#, "setHistory"),
            (r#"{"apps": ["Nope/oi4.org/A/B/C"]}"#, "unknown ServiceType"),
            (r#"{"apps": ["Utility"]}"#, "not <ServiceType>/<AppId>"),
            (r#"{"apps": [1]}"#, "not a string"),
        ] {
            let err = Settings::parse(text).unwrap_err();
            assert!(err.contains(part), "{text}: {err}");
        }
    }
}
