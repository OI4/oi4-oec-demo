//! What the config service knows about the applications on the bus: their
//! presence and MAM, Profile, Config DataSets and SubscriptionList, and the
//! results of the last `Set Config`s. Free of I/O: the [`crate::service`]
//! fills it from the bus, the HTTP API reads it.

use std::collections::{BTreeMap, VecDeque};

use serde_json::{Map, Value, json};

use crate::topic;

/// The mask of a sensitive value (REQ-RES-CONFIG-024).
pub(crate) const MASK: &str = "********";

/// An application: ServiceType and AppId as on the topic.
pub(crate) type AppKey = (String, String);

/// A Config DataSet: its asset (DataSetWriterName) and context.
pub(crate) type ConfigKey = (String, String);

/// An error of the API: an HTTP status and a message.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct ApiError {
    pub(crate) status: u16,
    pub(crate) message: String,
}

impl ApiError {
    pub(crate) fn new(status: u16, message: impl Into<String>) -> Self {
        Self {
            status,
            message: message.into(),
        }
    }
}

/// One Config DataSet as last received.
#[derive(Debug, Clone, PartialEq)]
pub(crate) struct ConfigEntry {
    pub(crate) data_set_writer_id: u16,
    /// The Payload: Context and groups, sensitive values masked by the
    /// provider (REQ-RES-CONFIG-024).
    pub(crate) config: Value,
}

/// What a discovery of one application found.
#[derive(Debug, Clone, Default, PartialEq)]
pub(crate) struct Discovery {
    /// The resources of its Profile; `None` if not answered.
    pub(crate) profile: Option<Vec<String>>,
    pub(crate) configs: BTreeMap<ConfigKey, ConfigEntry>,
    /// The TopicPaths of its SubscriptionList; `None` if it has none or
    /// did not answer.
    pub(crate) subscriptions: Option<Vec<String>>,
    /// What failed, e.g. a `Get` without answer.
    pub(crate) errors: Vec<String>,
}

/// One application.
#[derive(Debug, Clone, PartialEq)]
pub(crate) struct AppEntry {
    /// `online`, `offlineGraceful` or `offlineUngraceful`.
    pub(crate) state: &'static str,
    pub(crate) mam: Option<Value>,
    pub(crate) found: Discovery,
    /// Unix ms of the last discovery; `None` before the first.
    pub(crate) discovered_ms: Option<u64>,
}

/// A `Set Config` as the API asks for it.
#[derive(Debug, Clone, PartialEq)]
pub(crate) struct SetRequest {
    pub(crate) service_type: String,
    pub(crate) app: String,
    pub(crate) source: String,
    pub(crate) context: String,
    /// `{"<group>": {"<object>": "<value>"}}`
    pub(crate) values: Map<String, Value>,
}

impl SetRequest {
    /// The request of the JSON body of `POST /api/set`.
    pub(crate) fn parse(body: &Value) -> Result<Self, ApiError> {
        let text = |key: &str| {
            body.get(key)
                .and_then(Value::as_str)
                .filter(|s| !s.is_empty())
                .map(str::to_owned)
                .ok_or_else(|| ApiError::new(400, format!("{key}: a non-empty string is required")))
        };
        let values = body
            .get("values")
            .and_then(Value::as_object)
            .cloned()
            .ok_or_else(|| ApiError::new(400, "values: an object is required"))?;
        Ok(Self {
            service_type: text("serviceType")?,
            app: text("app")?,
            source: text("source")?,
            context: text("context")?,
            values,
        })
    }
}

/// A checked `Set Config`, ready to send.
#[derive(Debug, Clone, PartialEq)]
pub(crate) struct PreparedSet {
    /// The Payload of the one DataSetMessage (REQ-RES-CONFIG-003).
    pub(crate) payload: Value,
    /// The values for the history: sensitive values masked.
    pub(crate) shown: Value,
    pub(crate) data_set_writer_id: u16,
}

/// The outcome of a `Set Config` on the bus.
#[derive(Debug, Clone, PartialEq)]
pub(crate) enum SetOutcome {
    /// The Status event of the target (REQ-RES-CONFIG-021): code, symbol.
    Status(u32, String),
    /// No Status event: a timeout, offline, an error of the request.
    Failed(String),
}

/// The state.
#[derive(Debug)]
pub(crate) struct Directory {
    me: AppKey,
    apps: BTreeMap<AppKey, AppEntry>,
    sets: VecDeque<Value>,
    history: usize,
    next_set: u64,
}

impl Directory {
    /// An empty directory of the config service `me` that keeps `history`
    /// `Set` results.
    pub(crate) fn new(me: AppKey, history: usize) -> Self {
        Self {
            me,
            apps: BTreeMap::new(),
            sets: VecDeque::new(),
            history,
            next_set: 1,
        }
    }

    /// Whether `key` is the config service itself.
    pub(crate) fn is_me(&self, key: &AppKey) -> bool {
        *key == self.me
    }

    /// An application is online (Birth, heartbeat or MAM answer); `true`
    /// if it was unknown or offline, i.e. is to be discovered. The config
    /// service itself is not listed.
    pub(crate) fn online(&mut self, key: AppKey, mam: Option<Value>) -> bool {
        if self.is_me(&key) {
            return false;
        }
        let mut new = false;
        let entry = self.apps.entry(key).or_insert_with(|| {
            new = true;
            AppEntry {
                state: "online",
                mam: None,
                found: Discovery::default(),
                discovered_ms: None,
            }
        });
        let changed = new || entry.state != "online";
        entry.state = "online";
        if mam.is_some() {
            entry.mam = mam;
        }
        changed
    }

    /// An application went offline (REQ-LIFE-011, REQ-LIFE-012); what was
    /// found stays visible.
    pub(crate) fn offline(&mut self, key: &AppKey, graceful: bool) {
        if let Some(e) = self.apps.get_mut(key) {
            e.state = if graceful {
                "offlineGraceful"
            } else {
                "offlineUngraceful"
            };
        }
    }

    /// The keys of the online applications.
    pub(crate) fn online_apps(&self) -> Vec<AppKey> {
        self.apps
            .iter()
            .filter(|(_, e)| e.state == "online")
            .map(|(k, _)| k.clone())
            .collect()
    }

    /// Stores a discovery of a known application.
    pub(crate) fn discovered(&mut self, key: &AppKey, found: Discovery, now_ms: u64) {
        if let Some(e) = self.apps.get_mut(key) {
            e.found = found;
            e.discovered_ms = Some(now_ms);
        }
    }

    /// A Config DataSet was published or answered (REQ-RES-CONFIG-022): it
    /// replaces the one known of a known application.
    pub(crate) fn config(&mut self, key: &AppKey, config: ConfigKey, entry: ConfigEntry) {
        if let Some(e) = self.apps.get_mut(key) {
            e.found.configs.insert(config, entry);
        }
    }

    /// Checks a `Set Config` against what is known and builds its Payload:
    /// the application is online, the Config known and writable (SF-107),
    /// every group and object exists, every value is a string
    /// (REQ-RES-CONFIG-007). The mandatory objects of every contained group
    /// are added with their current value (REQ-RES-CONFIG-011); a sensitive
    /// one keeps the mask, which keeps its value (REQ-RES-CONFIG-025). Type
    /// and Validation are left to the target, which decides (REQ-RES-CONFIG-023,
    /// A-356).
    pub(crate) fn prepare_set(&self, req: &SetRequest) -> Result<PreparedSet, ApiError> {
        let key = (req.service_type.clone(), req.app.clone());
        let app = self
            .apps
            .get(&key)
            .ok_or_else(|| ApiError::new(404, "unknown application"))?;
        if app.state != "online" {
            return Err(ApiError::new(
                409,
                format!("the application is {}", app.state),
            ));
        }
        let entry = app
            .found
            .configs
            .get(&(req.source.clone(), req.context.clone()))
            .ok_or_else(|| ApiError::new(404, "unknown Config (source, context)"))?;
        if !writable(&key, &app.found, &req.source, &req.context) {
            return Err(ApiError::new(
                409,
                "not writable: the SubscriptionList does not list its Set topic (SF-107)",
            ));
        }
        let (payload, shown) = build_set(&entry.config, &req.values)?;
        Ok(PreparedSet {
            payload,
            shown,
            data_set_writer_id: entry.data_set_writer_id,
        })
    }

    /// Records the result of a `Set` and returns it as JSON.
    pub(crate) fn record_set(
        &mut self,
        req: &SetRequest,
        shown: Value,
        outcome: &SetOutcome,
        now_ms: u64,
    ) -> Value {
        let (status, error) = match outcome {
            SetOutcome::Status(code, symbol) => (
                json!({"code": code, "symbol": symbol, "good": code >> 30 == 0}),
                Value::Null,
            ),
            SetOutcome::Failed(e) => (Value::Null, Value::String(e.clone())),
        };
        let mut record = json!({
            "id": self.next_set,
            "timeMs": now_ms,
            "serviceType": req.service_type,
            "app": req.app,
            "source": req.source,
            "context": req.context,
            "values": null,
            "status": status,
            "error": error,
        });
        record["values"] = shown;
        self.next_set += 1;
        self.sets.push_front(record.clone());
        self.sets.truncate(self.history);
        record
    }

    /// The `Set` results, newest first.
    pub(crate) fn sets(&self) -> Value {
        json!({ "sets": self.sets })
    }

    /// The applications for the API; `health` gives the last Health
    /// (HealthState, HealthScore) of an application.
    pub(crate) fn snapshot(&self, health: impl Fn(&AppKey) -> (Value, Value)) -> Value {
        let apps: Vec<Value> = self
            .apps
            .iter()
            .map(|(key, e)| {
                let configs: Vec<Value> = e
                    .found
                    .configs
                    .iter()
                    .map(|((source, context), c)| {
                        json!({
                            "source": source,
                            "context": context,
                            "writable": writable(key, &e.found, source, context),
                            "dataSetWriterId": c.data_set_writer_id,
                            "config": c.config,
                        })
                    })
                    .collect();
                let (state, score) = health(key);
                json!({
                    "serviceType": key.0,
                    "app": key.1,
                    "state": e.state,
                    "health": state,
                    "healthScore": score,
                    "mam": e.mam,
                    "profile": e.found.profile,
                    "subscriptionList": e.found.subscriptions.is_some(),
                    "configs": configs,
                    "errors": e.found.errors,
                    "discoveredMs": e.discovered_ms,
                })
            })
            .collect();
        json!({ "apps": apps })
    }
}

/// Whether a Config is writable: the SubscriptionList of its application
/// lists its `Set` topic, also by a wildcard (SF-107, REQ-RES-SUBLIST-016).
pub(crate) fn writable(app: &AppKey, found: &Discovery, source: &str, context: &str) -> bool {
    let set = topic::set_config(&app.0, &app.1, source, context);
    found
        .subscriptions
        .iter()
        .flatten()
        .any(|path| topic::matches(path, &set))
}

/// The groups of a Config payload: every member but `Context`.
fn groups(config: &Value) -> impl Iterator<Item = (&String, &Map<String, Value>)> {
    config
        .as_object()
        .into_iter()
        .flatten()
        .filter(|(k, _)| *k != "Context")
        .filter_map(|(k, v)| v.as_object().map(|g| (k, g)))
}

/// The configuration objects of a group: every member but `Name` and
/// `Description`.
fn objects(group: &Map<String, Value>) -> impl Iterator<Item = (&String, &Map<String, Value>)> {
    group
        .iter()
        .filter(|(k, _)| *k != "Name" && *k != "Description")
        .filter_map(|(k, v)| v.as_object().map(|o| (k, o)))
}

fn is_true(object: &Map<String, Value>, key: &str) -> bool {
    object.get(key).and_then(Value::as_bool) == Some(true)
}

/// The Payload of a `Set Config` (REQ-RES-CONFIG-003) for `values` of the
/// Config `config` and the values to show (sensitive ones masked); see
/// [`Directory::prepare_set`].
pub(crate) fn build_set(
    config: &Value,
    values: &Map<String, Value>,
) -> Result<(Value, Value), ApiError> {
    let bad = |m: String| ApiError::new(400, m);
    if values.is_empty() {
        return Err(bad("values: no value".to_owned()));
    }
    let known: BTreeMap<&String, &Map<String, Value>> = groups(config).collect();
    let mut payload = Map::new();
    let mut shown = Map::new();
    for (group, objs) in values {
        let g = known
            .get(group)
            .ok_or_else(|| bad(format!("unknown group {group:?}")))?;
        let objs = objs
            .as_object()
            .filter(|o| !o.is_empty())
            .ok_or_else(|| bad(format!("{group}: an object with at least one value")))?;
        let defs: BTreeMap<&String, &Map<String, Value>> = objects(g).collect();
        let mut out = Map::new();
        let mut show = Map::new();
        for (name, value) in objs {
            let def = defs
                .get(name)
                .ok_or_else(|| bad(format!("unknown object {group}/{name}")))?;
            let text = value.as_str().ok_or_else(|| {
                bad(format!(
                    "{group}/{name}: the value is a string (REQ-RES-CONFIG-007)"
                ))
            })?;
            out.insert(name.clone(), json!({ "Value": text }));
            let visible = if is_true(def, "Sensitive") {
                MASK
            } else {
                text
            };
            show.insert(name.clone(), Value::String(visible.to_owned()));
        }
        for (name, def) in &defs {
            if is_true(def, "Mandatory") && !out.contains_key(*name) {
                let current = def.get("Value").and_then(Value::as_str).unwrap_or_default();
                out.insert((*name).clone(), json!({ "Value": current }));
            }
        }
        payload.insert(group.clone(), Value::Object(out));
        shown.insert(group.clone(), Value::Object(show));
    }
    Ok((Value::Object(payload), Value::Object(shown)))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn logger_config() -> Value {
        json!({
            "Context": {"Name": "logger"},
            "common": {
                "Name": "Common",
                "format": {"Type": "String", "Value": "json", "Name": "Format", "Mandatory": true},
                "file": {"Type": "Boolean", "Value": "false", "Name": "File"},
                "token": {"Type": "String", "Value": MASK, "Name": "Token", "Sensitive": true, "Mandatory": true}
            },
            "other": {"Name": "Other", "x": {"Type": "Number", "Value": "1", "Name": "X"}}
        })
    }

    fn values(v: &Value) -> Map<String, Value> {
        v.as_object().unwrap().clone()
    }

    #[test]
    fn set_payload_adds_mandatory_objects_and_keeps_the_mask() {
        let (payload, shown) = build_set(
            &logger_config(),
            &values(&json!({"common": {"file": "true"}})),
        )
        .unwrap();
        assert_eq!(
            payload,
            json!({"common": {
                "file": {"Value": "true"},
                "format": {"Value": "json"},
                "token": {"Value": MASK}
            }})
        );
        assert_eq!(shown, json!({"common": {"file": "true"}}));
        let (payload, shown) = build_set(
            &logger_config(),
            &values(&json!({"common": {"token": "s3cret"}, "other": {"x": "2"}})),
        )
        .unwrap();
        assert_eq!(payload["common"]["token"], json!({"Value": "s3cret"}));
        assert_eq!(payload["other"], json!({"x": {"Value": "2"}}));
        assert_eq!(shown["common"]["token"], MASK, "a secret never shows");
    }

    #[test]
    fn set_payload_errors() {
        for (v, part) in [
            (json!({}), "no value"),
            (json!({"nope": {"x": "1"}}), "unknown group"),
            (json!({"Context": {"Name": "x"}}), "unknown group"),
            (json!({"common": {}}), "at least one value"),
            (json!({"common": {"nope": "1"}}), "unknown object"),
            (json!({"common": {"Name": "x"}}), "unknown object"),
            (json!({"common": {"file": true}}), "is a string"),
        ] {
            let e = build_set(&logger_config(), &values(&v)).unwrap_err();
            assert_eq!(e.status, 400);
            assert!(e.message.contains(part), "{v}: {}", e.message);
        }
    }

    fn key() -> AppKey {
        (
            "Utility".to_owned(),
            "oi4.org/OEC,20Logger/OEC-LOGGER/l-1".to_owned(),
        )
    }

    fn found(subscriptions: Option<Vec<String>>) -> Discovery {
        let mut d = Discovery {
            profile: Some(vec!["MAM".into(), "Config".into()]),
            subscriptions,
            ..Discovery::default()
        };
        d.configs.insert(
            (key().1, "logger".to_owned()),
            ConfigEntry {
                data_set_writer_id: 7,
                config: logger_config(),
            },
        );
        d
    }

    fn request() -> SetRequest {
        SetRequest {
            service_type: key().0,
            app: key().1,
            source: key().1,
            context: "logger".to_owned(),
            values: values(&json!({"common": {"file": "true"}})),
        }
    }

    #[test]
    fn writable_by_the_subscription_list() {
        let set = topic::set_config(&key().0, &key().1, &key().1, "logger");
        let wildcard = set.rsplit_once('/').unwrap().0.to_owned() + "/+";
        for (list, want) in [
            (None, false),
            (Some(vec![]), false),
            (Some(vec![set.clone()]), true),
            (Some(vec![wildcard]), true),
            (Some(vec![set.replace("/Set/", "/Get/")]), false),
        ] {
            assert_eq!(
                writable(&key(), &found(list.clone()), &key().1, "logger"),
                want,
                "{list:?}"
            );
        }
    }

    #[test]
    fn prepare_set_checks_the_application() {
        let me = (
            "Utility".to_owned(),
            "oi4.org/OEC,20Config/OEC-CONFIG/c-1".to_owned(),
        );
        let mut dir = Directory::new(me.clone(), 2);
        assert!(!dir.online(me, None), "itself is not listed");
        assert_eq!(dir.prepare_set(&request()).unwrap_err().status, 404);
        assert!(dir.online(key(), None));
        assert!(!dir.online(key(), Some(json!({"Model": {"Text": "OEC Logger"}}))));
        dir.discovered(&key(), found(None), 1);
        let e = dir.prepare_set(&request()).unwrap_err();
        assert_eq!((e.status, e.message.contains("SF-107")), (409, true));
        let set = topic::set_config(&key().0, &key().1, &key().1, "logger");
        dir.discovered(&key(), found(Some(vec![set])), 2);
        let ok = dir.prepare_set(&request()).unwrap();
        assert_eq!(ok.data_set_writer_id, 7);
        let mut other = request();
        other.context = "nope".to_owned();
        assert_eq!(dir.prepare_set(&other).unwrap_err().status, 404);
        dir.offline(&key(), true);
        let e = dir.prepare_set(&request()).unwrap_err();
        assert_eq!(
            (e.status, e.message.as_str()),
            (409, "the application is offlineGraceful")
        );
        assert!(dir.online_apps().is_empty());
        assert!(dir.online(key(), None), "online again");
        assert_eq!(dir.online_apps(), vec![key()]);
        let snap = dir.snapshot(|_| (json!("NORMAL_0"), json!(100)));
        let app = &snap["apps"][0];
        assert_eq!(app["mam"]["Model"]["Text"], "OEC Logger", "the MAM stays");
        assert_eq!(app["configs"][0]["writable"], true);
        assert_eq!(app["configs"][0]["context"], "logger");
        assert_eq!(app["health"], "NORMAL_0");
    }

    #[test]
    fn set_history_is_bounded_and_newest_first() {
        let mut dir = Directory::new(("Utility".into(), "x/y/z/1".into()), 2);
        let good = SetOutcome::Status(0, "Good".into());
        let bad = SetOutcome::Status(0x803C_0000, "BadOutOfRange".into());
        let r1 = dir.record_set(&request(), json!({}), &good, 1);
        assert_eq!(r1["status"]["good"], true);
        let r2 = dir.record_set(&request(), json!({}), &bad, 2);
        assert_eq!(r2["status"]["good"], false);
        let r3 = dir.record_set(
            &request(),
            json!({}),
            &SetOutcome::Failed("timeout".into()),
            3,
        );
        assert_eq!(
            (r3["status"].is_null(), r3["error"].as_str()),
            (true, Some("timeout"))
        );
        let ids: Vec<_> = dir.sets()["sets"]
            .as_array()
            .unwrap()
            .iter()
            .map(|s| s["id"].as_u64().unwrap())
            .collect();
        assert_eq!(ids, vec![3, 2]);
    }

    #[test]
    fn set_request_of_json() {
        let body = json!({"serviceType": "Utility", "app": "a", "source": "s", "context": "c",
                          "values": {"g": {"o": "v"}}});
        let r = SetRequest::parse(&body).unwrap();
        assert_eq!((r.app.as_str(), r.context.as_str()), ("a", "c"));
        for missing in ["serviceType", "app", "source", "context", "values"] {
            let mut b = body.clone();
            b.as_object_mut().unwrap().remove(missing);
            let e = SetRequest::parse(&b).unwrap_err();
            assert!(e.message.starts_with(missing), "{}", e.message);
        }
    }
}
