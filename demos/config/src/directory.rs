//! What the config service knows about the applications on the bus: their
//! presence and MAM, Profile, Config DataSets and SubscriptionList, and the
//! results of the last `Set Config`s. Free of I/O: the [`crate::service`]
//! fills it from the bus, the HTTP API reads it.

use std::collections::{BTreeMap, BTreeSet, VecDeque};
use std::mem::size_of;

use serde_json::{Map, Value, json};

use crate::topic;

/// The mask of a sensitive value (REQ-RES-CONFIG-024).
pub(crate) const MASK: &str = "********";

/// An application: ServiceType and AppId as on the topic.
pub(crate) type AppKey = (String, String);

/// A Config DataSet: its asset (DataSetWriterName) and context.
pub(crate) type ConfigKey = (String, String);

/// A configuration object: its Config DataSet, group and name.
type ObjectKey = (ConfigKey, String, String);

/// What the directory keeps at most (C-113 RC-1). Every client of the bus
/// can publish Births, MAMs and Configs of any number of applications, so
/// the directory bounds them instead of growing until the container runs
/// out of memory. Sizes are estimated by [`weight`].
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) struct Limits {
    /// Applications listed. An offline one makes room for a new one, the
    /// one offline longest first; without one, a new application is not
    /// listed.
    pub(crate) apps: usize,
    /// What is kept of one application: MAM, Profile, Configs and
    /// SubscriptionList. What does not fit is not kept.
    pub(crate) app_bytes: usize,
    /// What is kept of all applications; offline ones make room.
    pub(crate) total_bytes: usize,
}

impl Limits {
    /// 1000 applications are far more than the OEC applications on one
    /// bus (each is a container or a device). 256 KiB per application hold
    /// a MAM (< 2 KiB) and dozens of Configs (those of the demos have
    /// < 8 KiB). 64 MiB in total keep the service with the SDK well below
    /// the memory limit of its container (384 MiB).
    pub(crate) const DEFAULT: Self = Self {
        apps: 1000,
        app_bytes: 256 * 1024,
        total_bytes: 64 * 1024 * 1024,
    };
}

/// The note of a discovery that did not fit [`Limits::app_bytes`].
const OVER_LIMIT: &str = "above the memory limit of the service: not everything is kept";

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
    /// Every object seen as `Sensitive`: it stays masked (C-113 RD-2).
    sensitive: BTreeSet<ObjectKey>,
    /// The estimated size of the entry ([`weight`]).
    weight: usize,
    /// When it went offline: the order of [`Directory::offline`] calls.
    offline_since: u64,
}

impl AppEntry {
    fn new(key: &AppKey) -> Self {
        let mut e = Self {
            state: "online",
            mam: None,
            found: Discovery::default(),
            discovered_ms: None,
            sensitive: BTreeSet::new(),
            weight: 0,
            offline_since: 0,
        };
        e.weight = e.weigh(key);
        e
    }

    fn weigh(&self, key: &AppKey) -> usize {
        size_of::<Self>()
            + ENTRY
            + text(&key.0)
            + text(&key.1)
            + self.mam.as_ref().map_or(0, weight)
            + self.sensitive.iter().map(object_weight).sum::<usize>()
            + found_weight(&self.found)
    }
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
    limits: Limits,
    /// The sum of the weights of the entries.
    stored: usize,
    /// Updates not kept because of the limits.
    dropped: u64,
    /// The last [`AppEntry::offline_since`].
    offline_seq: u64,
}

impl Directory {
    /// An empty directory of the config service `me` that keeps `history`
    /// `Set` results, within [`Limits::DEFAULT`].
    pub(crate) fn new(me: AppKey, history: usize) -> Self {
        Self::with_limits(me, history, Limits::DEFAULT)
    }

    /// An empty directory within `limits`.
    pub(crate) fn with_limits(me: AppKey, history: usize, limits: Limits) -> Self {
        Self {
            me,
            apps: BTreeMap::new(),
            sets: VecDeque::new(),
            history,
            next_set: 1,
            limits,
            stored: 0,
            dropped: 0,
            offline_seq: 0,
        }
    }

    /// The number of updates not kept because of the limits.
    pub(crate) fn dropped(&self) -> u64 {
        self.dropped
    }

    /// Counts an update not kept because of the limits, e.g. a payload too
    /// large to read.
    pub(crate) fn refused(&mut self) {
        self.dropped += 1;
    }

    /// Whether `key` is listed.
    pub(crate) fn knows(&self, key: &AppKey) -> bool {
        self.apps.contains_key(key)
    }

    #[cfg(test)]
    pub(crate) fn stored(&self) -> usize {
        self.stored
    }

    /// Removes the application offline longest but `keep`; `false` if
    /// there is none.
    fn evict_offline(&mut self, keep: &AppKey) -> bool {
        let oldest = self
            .apps
            .iter()
            .filter(|(k, e)| e.state != "online" && *k != keep)
            .min_by_key(|(_, e)| e.offline_since)
            .map(|(k, _)| k.clone());
        let Some(k) = oldest else {
            return false;
        };
        if let Some(e) = self.apps.remove(&k) {
            self.stored -= e.weight;
        }
        true
    }

    /// Whether the entry of `key` may grow from `old` to `new`; offline
    /// applications make room. Counts what is refused.
    fn make_room(&mut self, key: &AppKey, old: usize, new: usize) -> bool {
        let fits = new <= self.limits.app_bytes
            && loop {
                if self.stored - old + new <= self.limits.total_bytes {
                    break true;
                }
                if !self.evict_offline(key) {
                    break false;
                }
            };
        if fits {
            self.stored = self.stored - old + new;
        } else {
            self.dropped += 1;
        }
        fits
    }

    /// Whether `key` is the config service itself.
    pub(crate) fn is_me(&self, key: &AppKey) -> bool {
        *key == self.me
    }

    /// An application is online (Birth, heartbeat or MAM answer); `true`
    /// if it was unknown or offline, i.e. is to be discovered. The config
    /// service itself is not listed.
    ///
    /// Within the [`Limits`]: a new application finds no room if the
    /// directory is full of online ones; a MAM that does not fit is not
    /// kept (C-113 RC-1).
    pub(crate) fn online(&mut self, key: &AppKey, mam: Option<Value>) -> bool {
        if self.is_me(key) {
            return false;
        }
        let new = !self.apps.contains_key(key);
        if new {
            if self.apps.len() >= self.limits.apps && !self.evict_offline(key) {
                self.dropped += 1;
                return false;
            }
            let entry = AppEntry::new(key);
            if !self.make_room(key, 0, entry.weight) {
                return false;
            }
            self.apps.insert(key.clone(), entry);
        }
        let Some(entry) = self.apps.get_mut(key) else {
            return false;
        };
        let changed = new || entry.state != "online";
        entry.state = "online";
        if let Some(mam) = mam {
            let old = entry.weight;
            let grown = old - entry.mam.as_ref().map_or(0, weight) + weight(&mam);
            if self.make_room(key, old, grown)
                && let Some(entry) = self.apps.get_mut(key)
            {
                entry.mam = Some(mam);
                entry.weight = grown;
            }
        }
        changed
    }

    /// An application went offline (REQ-LIFE-011, REQ-LIFE-012); what was
    /// found stays visible.
    pub(crate) fn offline(&mut self, key: &AppKey, graceful: bool) {
        if let Some(e) = self.apps.get_mut(key) {
            if e.state == "online" {
                self.offline_seq += 1;
                e.offline_since = self.offline_seq;
            }
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

    /// Stores a discovery of a known application; what does not fit
    /// [`Limits::app_bytes`] is left out with a note in its errors (C-113
    /// RC-1). An object once `Sensitive` stays masked (C-113 RD-2). Returns
    /// the objects that are no longer `Sensitive` (`<context>:
    /// <group>/<object>`).
    pub(crate) fn discovered(
        &mut self,
        key: &AppKey,
        mut found: Discovery,
        now_ms: u64,
    ) -> Vec<String> {
        let Some(e) = self.apps.get(key) else {
            return Vec::new();
        };
        let mut sensitive = e.sensitive.clone();
        let budget = self.limits.app_bytes.saturating_sub(text(OVER_LIMIT));
        let base = |found: &Discovery, sensitive: &BTreeSet<ObjectKey>| {
            AppEntry {
                found: Discovery {
                    configs: BTreeMap::new(),
                    ..found.clone()
                },
                mam: None,
                sensitive: sensitive.clone(),
                ..AppEntry::new(key)
            }
            .weigh(key)
                + e.mam.as_ref().map_or(0, weight)
        };
        let mut used = base(&found, &sensitive);
        let mut trimmed = false;
        if used > budget {
            found.profile = None;
            found.subscriptions = None;
            used = base(&found, &sensitive);
            trimmed = true;
        }
        let mut downgraded = Vec::new();
        let configs = std::mem::take(&mut found.configs);
        for (ck, mut c) in configs {
            let (added, down) = keep_sensitive(&sensitive, &ck, &mut c.config);
            let w = config_weight(&ck, &c) + added.iter().map(object_weight).sum::<usize>();
            if used + w > budget {
                trimmed = true;
                continue;
            }
            used += w;
            downgraded.extend(down);
            sensitive.extend(added);
            found.configs.insert(ck, c);
        }
        if trimmed {
            found.errors.push(OVER_LIMIT.to_owned());
        }
        let old = e.weight;
        let new = AppEntry {
            found,
            mam: e.mam.clone(),
            sensitive,
            ..AppEntry::new(key)
        };
        let new_weight = new.weigh(key);
        if trimmed {
            self.dropped += 1;
        }
        if !self.make_room(key, old, new_weight) {
            return Vec::new();
        }
        let Some(e) = self.apps.get_mut(key) else {
            return Vec::new();
        };
        e.found = new.found;
        e.sensitive = new.sensitive;
        e.weight = new_weight;
        e.discovered_ms = Some(now_ms);
        downgraded
    }

    /// A Config DataSet was published or answered (REQ-RES-CONFIG-022): it
    /// replaces the one known of a known application if it fits
    /// [`Limits::app_bytes`] (C-113 RC-1). An object once `Sensitive` stays
    /// masked (C-113 RD-2). Returns the objects that are no longer
    /// `Sensitive` (`<context>: <group>/<object>`).
    pub(crate) fn config(
        &mut self,
        key: &AppKey,
        config: ConfigKey,
        mut entry: ConfigEntry,
    ) -> Vec<String> {
        let Some(e) = self.apps.get(key) else {
            return Vec::new();
        };
        let (added, downgraded) = keep_sensitive(&e.sensitive, &config, &mut entry.config);
        let old = e.weight;
        let replaced = e
            .found
            .configs
            .get(&config)
            .map_or(0, |c| config_weight(&config, c));
        let new = old - replaced
            + config_weight(&config, &entry)
            + added.iter().map(object_weight).sum::<usize>();
        if !self.make_room(key, old, new) {
            return Vec::new();
        }
        let Some(e) = self.apps.get_mut(key) else {
            return Vec::new();
        };
        e.sensitive.extend(added);
        e.found.configs.insert(config, entry);
        e.weight = new;
        downgraded
    }

    /// Checks a `Set Config` against what is known and builds its Payload:
    /// the application is online, the Config known and writable (SF-107),
    /// every group and object exists, every value is a string
    /// (REQ-RES-CONFIG-007). The mandatory objects of every contained group
    /// not in the request are added with their cached value
    /// (REQ-RES-CONFIG-011; the Web UI sends the ones it shows, C-113 RD-2);
    /// a sensitive one keeps the mask, which keeps its value
    /// (REQ-RES-CONFIG-025). Type
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

/// The overhead of an entry of a map (estimated).
const ENTRY: usize = 32;

/// The estimated memory of a string.
fn text(s: &str) -> usize {
    size_of::<String>() + s.len()
}

/// The estimated memory of a JSON value: its nodes, keys and strings. The
/// JSON text alone would underestimate a payload of many small members
/// many times.
pub(crate) fn weight(v: &Value) -> usize {
    size_of::<Value>()
        + match v {
            Value::String(s) => s.len(),
            Value::Array(a) => a.iter().map(weight).sum(),
            Value::Object(o) => o.iter().map(|(k, v)| ENTRY + text(k) + weight(v)).sum(),
            _ => 0,
        }
}

fn texts(list: Option<&Vec<String>>) -> usize {
    list.into_iter().flatten().map(|s| text(s)).sum()
}

fn config_weight(key: &ConfigKey, c: &ConfigEntry) -> usize {
    ENTRY + text(&key.0) + text(&key.1) + weight(&c.config)
}

fn object_weight(o: &ObjectKey) -> usize {
    ENTRY + text(&o.0.0) + text(&o.0.1) + text(&o.1) + text(&o.2)
}

fn found_weight(found: &Discovery) -> usize {
    texts(found.profile.as_ref())
        + texts(found.subscriptions.as_ref())
        + texts(Some(&found.errors))
        + found
            .configs
            .iter()
            .map(|(k, c)| config_weight(k, c))
            .sum::<usize>()
}

/// Keeps every object of the Config `config` (DataSet `key`) masked that
/// was `Sensitive` once (`known`), even if `config` says otherwise: a
/// Config published by anyone on the bus must not unmask the next secret
/// typed in (C-113 RD-2, REQ-RES-CONFIG-024). Returns the objects
/// `Sensitive` for the first time and the ones no longer `Sensitive`
/// (`<context>: <group>/<object>`).
fn keep_sensitive(
    known: &BTreeSet<ObjectKey>,
    key: &ConfigKey,
    config: &mut Value,
) -> (Vec<ObjectKey>, Vec<String>) {
    let (mut added, mut downgraded) = (Vec::new(), Vec::new());
    let groups = config.as_object_mut().into_iter().flatten();
    for (group, g) in groups.filter(|(k, _)| *k != "Context") {
        let objects = g.as_object_mut().into_iter().flatten();
        for (name, o) in objects.filter(|(k, _)| *k != "Name" && *k != "Description") {
            let Some(o) = o.as_object_mut() else {
                continue;
            };
            let id = (key.clone(), group.clone(), name.clone());
            if is_true(o, "Sensitive") {
                if !known.contains(&id) {
                    added.push(id);
                }
            } else if known.contains(&id) {
                o.insert("Sensitive".to_owned(), Value::Bool(true));
                o.insert("Value".to_owned(), Value::String(MASK.to_owned()));
                downgraded.push(format!("{}: {group}/{name}", key.1));
            }
        }
    }
    (added, downgraded)
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
        assert!(!dir.online(&me, None), "itself is not listed");
        assert_eq!(dir.prepare_set(&request()).unwrap_err().status, 404);
        assert!(dir.online(&key(), None));
        assert!(!dir.online(&key(), Some(json!({"Model": {"Text": "OEC Logger"}}))));
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
        assert!(dir.online(&key(), None), "online again");
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

    fn app(i: usize) -> AppKey {
        ("Utility".to_owned(), format!("oi4.org/Fake/FAKE/{i}"))
    }

    // C-113 RC-1: 1500 fake applications with a MAM of ~60 KB each.
    #[test]
    fn a_flood_of_applications_is_bounded() {
        let mut dir = Directory::new(("Utility".into(), "x/y/z/1".into()), 2);
        let mam = json!({"Description": {"Text": "x".repeat(60_000)}});
        for i in 0..1500 {
            dir.online(&app(i), Some(mam.clone()));
        }
        let snap = dir.snapshot(|_| (Value::Null, Value::Null));
        let apps = snap["apps"].as_array().unwrap();
        assert_eq!(apps.len(), Limits::DEFAULT.apps);
        assert!(dir.stored() <= Limits::DEFAULT.total_bytes);
        assert!(!dir.online(&app(2000), None), "a new one is ignored");
        assert_eq!(dir.dropped(), 501);
        dir.offline(&app(7), false);
        dir.offline(&app(3), true);
        assert!(dir.online(&app(2000), None), "an offline one makes room");
        assert!(dir.online(&app(2001), None));
        assert!(!dir.online(&app(2002), None));
        let listed: Vec<String> = dir.snapshot(|_| (Value::Null, Value::Null))["apps"]
            .as_array()
            .unwrap()
            .iter()
            .map(|a| a["app"].as_str().unwrap().to_owned())
            .collect();
        assert!(!listed.contains(&app(7).1) && !listed.contains(&app(3).1));
        assert!(listed.contains(&app(2000).1) && listed.contains(&app(2001).1));
    }

    #[test]
    fn the_oldest_offline_application_is_evicted_first() {
        let limits = Limits {
            apps: 3,
            ..Limits::DEFAULT
        };
        let mut dir = Directory::with_limits(("Utility".into(), "x/y/z/1".into()), 2, limits);
        for i in 0..3 {
            assert!(dir.online(&app(i), None));
        }
        dir.offline(&app(2), true);
        dir.offline(&app(0), true);
        assert!(dir.online(&app(3), None));
        assert!(!dir.knows(&app(2)) && dir.knows(&app(0)));
        assert_eq!(dir.online_apps(), vec![app(1), app(3)]);
        assert!(dir.online(&app(0), None), "online again");
        dir.offline(&app(1), true);
        assert!(dir.online(&app(4), None));
        assert_eq!(dir.online_apps(), vec![app(0), app(3), app(4)]);
    }

    #[test]
    fn an_application_cannot_pin_more_than_its_budget() {
        let limits = Limits {
            app_bytes: 4096,
            ..Limits::DEFAULT
        };
        let mut dir = Directory::with_limits(("Utility".into(), "x/y/z/1".into()), 2, limits);
        let big = json!({"Description": {"Text": "x".repeat(5000)}});
        assert!(
            dir.online(&key(), Some(big.clone())),
            "listed without its MAM"
        );
        assert_eq!(dir.dropped(), 1);
        assert!(!dir.online(&key(), Some(json!({"Model": {"Text": "Logger"}}))));
        let set = topic::set_config(&key().0, &key().1, &key().1, "logger");
        dir.discovered(&key(), found(Some(vec![set])), 1);
        let small = ConfigEntry {
            data_set_writer_id: 8,
            config: json!({"Context": {"Name": "a"}}),
        };
        let large = ConfigEntry {
            data_set_writer_id: 9,
            config: json!({"Context": {"Name": "b"}, "g": big}),
        };
        dir.config(&key(), (key().1, "a".into()), small);
        dir.config(&key(), (key().1, "b".into()), large.clone());
        assert_eq!(dir.dropped(), 2);
        let mut d = found(None);
        d.configs.insert((key().1, "b".into()), large);
        dir.discovered(&key(), d, 2);
        assert_eq!(dir.dropped(), 3);
        let snap = dir.snapshot(|_| (Value::Null, Value::Null));
        let a = &snap["apps"][0];
        assert_eq!(a["mam"]["Model"]["Text"], "Logger");
        let contexts: Vec<_> = a["configs"]
            .as_array()
            .unwrap()
            .iter()
            .map(|c| c["context"].clone())
            .collect();
        assert_eq!(contexts, vec![json!("logger")]);
        assert!(a["errors"][0].as_str().unwrap().contains("memory limit"));
        assert!(dir.stored() <= 4096);
    }

    #[test]
    fn the_total_budget_evicts_offline_applications() {
        let mam = json!({"Description": {"Text": "x".repeat(3000)}});
        let limits = Limits {
            total_bytes: 3 * (weight(&mam) + 1000),
            ..Limits::DEFAULT
        };
        let mut dir = Directory::with_limits(("Utility".into(), "x/y/z/1".into()), 2, limits);
        for i in 0..3 {
            dir.online(&app(i), Some(mam.clone()));
        }
        assert_eq!(dir.dropped(), 0);
        dir.online(&app(3), Some(mam.clone()));
        assert_eq!(dir.dropped(), 1, "listed without its MAM");
        dir.offline(&app(1), true);
        dir.online(&app(4), Some(mam.clone()));
        assert_eq!(dir.dropped(), 1, "the offline one made room");
        let snap = dir.snapshot(|_| (Value::Null, Value::Null));
        let apps: Vec<_> = snap["apps"]
            .as_array()
            .unwrap()
            .iter()
            .map(|a| a["app"].clone())
            .collect();
        assert!(!apps.contains(&json!(app(1).1)));
        assert!(dir.stored() <= limits.total_bytes);
    }

    // C-113 RD-2: a spoofed Pub/Config that drops `Sensitive`.
    #[test]
    fn sensitive_stays_sensitive() {
        let mut dir = Directory::new(("Utility".into(), "x/y/z/1".into()), 2);
        dir.online(&key(), None);
        let set = topic::set_config(&key().0, &key().1, &key().1, "logger");
        dir.discovered(&key(), found(Some(vec![set.clone()])), 1);
        let mut forged = logger_config();
        forged["common"]["token"]["Sensitive"] = json!(false);
        forged["common"]["token"]["Value"] = json!("evil");
        forged["common"]["file"]["Sensitive"] = json!(false);
        let ck = (key().1, "logger".to_owned());
        let entry = ConfigEntry {
            data_set_writer_id: 7,
            config: forged.clone(),
        };
        assert_eq!(
            dir.config(&key(), ck.clone(), entry),
            vec!["logger: common/token".to_owned()]
        );
        let check = |dir: &Directory| {
            let snap = dir.snapshot(|_| (Value::Null, Value::Null));
            let token = &snap["apps"][0]["configs"][0]["config"]["common"]["token"];
            assert_eq!(
                (&token["Sensitive"], &token["Value"]),
                (&json!(true), &json!(MASK))
            );
            let mut req = request();
            req.values = values(&json!({"common": {"token": "n3w-secret"}}));
            let ok = dir.prepare_set(&req).unwrap();
            assert_eq!(
                ok.shown["common"]["token"], MASK,
                "the new secret never shows"
            );
            let ok = dir.prepare_set(&request()).unwrap();
            assert_eq!(
                ok.payload["common"]["token"],
                json!({"Value": MASK}),
                "no injected value"
            );
        };
        check(&dir);
        let mut d = found(Some(vec![set]));
        d.configs.get_mut(&ck).unwrap().config = forged;
        assert_eq!(
            dir.discovered(&key(), d, 2),
            vec!["logger: common/token".to_owned()]
        );
        check(&dir);
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
