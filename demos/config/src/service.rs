//! The config service on the bus (A-304): finds the applications, reads
//! their Profile, Config and SubscriptionList and sends `Set Config`.
//!
//! No tokio here (A-355): the SDK runtime runs in `App::run` on the main
//! thread; the requests use the blocking forms on a worker thread (the
//! discoveries) and on the threads of the HTTP API (a `Set`, a refresh).
//! The callbacks of the SDK only update the [`Directory`] and queue jobs.

use std::collections::{BTreeSet, HashMap};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{Receiver, Sender};
use std::sync::{Arc, Mutex, MutexGuard, PoisonError, Weak};
use std::time::{SystemTime, UNIX_EPOCH};

use oec::consumer::{GetOptions, Presence, Received, Subscription, WriteOptions};
use oec::prelude::*;
use serde_json::{Value, json};

use crate::directory::{
    ApiError, AppKey, ConfigEntry, Directory, Discovery, SetOutcome, SetRequest,
};
use crate::settings::Settings;

/// A job of the worker thread.
#[derive(Debug)]
pub(crate) enum Job {
    /// (Re)connected: `Get MAM` of the configured applications, then
    /// discover every online application.
    Connected,
    /// Discover one application.
    Discover(AppKey),
}

/// The service.
pub(crate) struct Service {
    consumer: Consumer,
    settings: Settings,
    dir: Mutex<Directory>,
    connected: AtomicBool,
    jobs: Mutex<Sender<Job>>,
    /// Applications queued for a discovery (no second job for one).
    pending: Mutex<BTreeSet<AppKey>>,
    subscriptions: Mutex<Vec<Subscription>>,
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(PoisonError::into_inner)
}

/// Unix time in ms.
pub(crate) fn now_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| u64::try_from(d.as_millis()).unwrap_or(u64::MAX))
}

/// The symbol of a StatusCode, e.g. `BadOutOfRange`; an unknown code in
/// hex.
pub(crate) fn symbol(code: StatusCode) -> String {
    match code {
        StatusCode::Other(c) => format!("0x{c:08X}"),
        known => format!("{known:?}"),
    }
}

fn key_of(service_type: ServiceType, app: &IdentificationLink) -> AppKey {
    (service_type.as_str().to_owned(), app.as_str().to_owned())
}

fn target_of(key: &AppKey) -> Result<Target, ApiError> {
    let st: ServiceType = key
        .0
        .parse()
        .map_err(|_| ApiError::new(400, format!("unknown ServiceType {:?}", key.0)))?;
    let app =
        IdentificationLink::parse(&key.1).map_err(|e| ApiError::new(400, format!("app: {e}")))?;
    Ok(Target::app(st, app))
}

/// The context of a received Config DataSet: its WriterGroupName, which is
/// its Filter also on a topic without Filter (REQ-MSG-024).
fn config_key(r: &Received) -> Option<(String, String)> {
    let context = r.writer_group_name().or_else(|| r.filter())?;
    Some((r.data_set_writer_name().as_str().to_owned(), context))
}

fn config_entry(r: &Received) -> Option<ConfigEntry> {
    let config: Value = serde_json::from_str(&r.payload_json()).ok()?;
    Some(ConfigEntry {
        data_set_writer_id: r.data_set_writer_id(),
        config,
    })
}

impl Service {
    /// The service of the application `me` with its jobs queue `jobs`.
    pub(crate) fn new(
        consumer: Consumer,
        settings: Settings,
        me: AppKey,
        jobs: Sender<Job>,
    ) -> Arc<Self> {
        let history = settings.set_history;
        Arc::new(Self {
            consumer,
            settings,
            dir: Mutex::new(Directory::new(me, history)),
            connected: AtomicBool::new(false),
            jobs: Mutex::new(jobs),
            pending: Mutex::new(BTreeSet::new()),
            subscriptions: Mutex::new(Vec::new()),
        })
    }

    /// Subscribes to the presence of the applications and to every
    /// published Config (REQ-RES-CONFIG-022: a changed Config is
    /// published).
    ///
    /// # Errors
    ///
    /// An error of the SDK.
    pub(crate) fn watch(self: &Arc<Self>) -> oec::Result<()> {
        let me = Arc::downgrade(self);
        let presence = self.consumer.on_presence(move |p| {
            if let Some(s) = Weak::upgrade(&me) {
                s.presence(p);
            }
        })?;
        let me = Arc::downgrade(self);
        let configs = self
            .consumer
            .subscribe(Resource::Config, &Target::any(), move |r| {
                if let Some(s) = Weak::upgrade(&me) {
                    s.published_config(r);
                }
            })?;
        lock(&self.subscriptions).extend([presence, configs]);
        Ok(())
    }

    /// The connection state changed.
    pub(crate) fn connection(&self, online: bool) {
        self.connected.store(online, Ordering::SeqCst);
        if online {
            let _ = lock(&self.jobs).send(Job::Connected);
        }
    }

    fn presence(&self, p: &Presence) {
        match p {
            Presence::Online {
                service_type,
                app,
                mam,
            } => {
                let key = key_of(*service_type, app);
                let mam = mam.as_ref().and_then(|m| serde_json::to_value(m).ok());
                if lock(&self.dir).online(key.clone(), mam) {
                    self.queue(key);
                }
            }
            Presence::OfflineGraceful { service_type, app } => {
                lock(&self.dir).offline(&key_of(*service_type, app), true);
            }
            Presence::OfflineUngraceful { service_type, app } => {
                lock(&self.dir).offline(&key_of(*service_type, app), false);
            }
            _ => {}
        }
    }

    fn published_config(&self, r: &Received) {
        let key = key_of(r.service_type(), &r.app());
        if let (Some(config), Some(entry)) = (config_key(r), config_entry(r)) {
            lock(&self.dir).config(&key, config, entry);
        }
    }

    fn queue(&self, key: AppKey) {
        if lock(&self.pending).insert(key.clone()) {
            let _ = lock(&self.jobs).send(Job::Discover(key));
        }
    }

    /// The worker: runs the jobs until the queue closes.
    pub(crate) fn work(&self, jobs: &Receiver<Job>) {
        for job in jobs {
            match job {
                Job::Connected => self.connected_job(),
                Job::Discover(key) => {
                    lock(&self.pending).remove(&key);
                    self.discover(&key);
                }
            }
        }
    }

    fn options(&self) -> GetOptions {
        GetOptions::new().timeout(self.settings.request_timeout)
    }

    /// `Get MAM` of an application: its MAM about itself.
    fn get_mam(&self, key: &AppKey) -> Result<Option<Value>, String> {
        let target = target_of(key).map_err(|e| e.message)?;
        let answers = self
            .consumer
            .get_blocking(Resource::Mam, &target, &self.options())
            .map_err(|e| e.to_string())?;
        Ok(answers
            .iter()
            .find(|r| r.data_set_writer_name().as_str() == key.1)
            .and_then(|r| serde_json::from_str(&r.payload_json()).ok()))
    }

    /// `Get MAM` of the configured applications (A-355); the ones that
    /// answer and every other online application are discovered again.
    fn connected_job(&self) {
        for (st, app) in &self.settings.apps {
            let key = key_of(*st, app);
            match self.get_mam(&key) {
                Ok(mam) => {
                    lock(&self.dir).online(key, mam);
                }
                Err(e) => log::info!("Get MAM of {}: {e}", key.1),
            }
        }
        let apps = lock(&self.dir).online_apps();
        for key in apps {
            self.queue(key);
        }
    }

    /// Reads Profile, Config and SubscriptionList of an application.
    pub(crate) fn discover(&self, key: &AppKey) {
        let Ok(target) = target_of(key) else {
            return;
        };
        let mut found = Discovery::default();
        let get = |resource: Resource, found: &mut Discovery| match self.consumer.get_blocking(
            resource,
            &target,
            &self.options(),
        ) {
            Ok(answers) => Some(answers),
            Err(e) => {
                found.errors.push(format!("Get {resource:?}: {e}"));
                None
            }
        };
        if let Some(answers) = get(Resource::Profile, &mut found) {
            found.profile = answers.first().map(|r| {
                serde_json::from_str::<Value>(&r.payload_json())
                    .ok()
                    .and_then(|p| p.get("Resources").and_then(Value::as_array).cloned())
                    .unwrap_or_default()
                    .iter()
                    .filter_map(|v| v.as_str().map(str::to_owned))
                    .collect()
            });
        }
        let has = |name: &str| {
            found
                .profile
                .as_ref()
                .is_some_and(|p| p.iter().any(|r| r == name))
        };
        let (config, sublist) = (has("Config"), has("SubscriptionList"));
        if config {
            for r in get(Resource::Config, &mut found).into_iter().flatten() {
                if let (Some(k), Some(e)) = (config_key(&r), config_entry(&r)) {
                    found.configs.insert(k, e);
                }
            }
        }
        if sublist {
            found.subscriptions = get(Resource::SubscriptionList, &mut found).map(|answers| {
                answers
                    .iter()
                    .filter_map(|r| serde_json::from_str::<Value>(&r.payload_json()).ok())
                    .filter_map(|e| e.get("TopicPath")?.as_str().map(str::to_owned))
                    .collect()
            });
        }
        log::debug!(
            "discovered {}: {} Config(s), {} error(s)",
            key.1,
            found.configs.len(),
            found.errors.len()
        );
        lock(&self.dir).discovered(key, found, now_ms());
    }

    /// `POST /api/refresh`: discovers one application (`key`) or every
    /// online one, now.
    pub(crate) fn refresh(&self, key: Option<AppKey>) -> Result<Value, ApiError> {
        if !self.connected.load(Ordering::SeqCst) {
            return Err(ApiError::new(503, "not connected"));
        }
        let keys = match key {
            Some(k) => {
                target_of(&k)?;
                if lock(&self.dir).is_me(&k) {
                    return Err(ApiError::new(404, "unknown application"));
                }
                let mam = self
                    .get_mam(&k)
                    .map_err(|e| ApiError::new(404, format!("Get MAM: {e}")))?;
                lock(&self.dir).online(k.clone(), mam);
                vec![k]
            }
            None => lock(&self.dir).online_apps(),
        };
        for k in &keys {
            self.discover(k);
        }
        Ok(self.apps())
    }

    /// `POST /api/set`: sends a `Set Config` and waits for its Status
    /// event (REQ-RES-CONFIG-021); after Good, reads the Config again.
    /// Returns the HTTP status and the recorded result.
    pub(crate) fn set(&self, req: &SetRequest) -> Result<(u16, Value), ApiError> {
        if !self.connected.load(Ordering::SeqCst) {
            return Err(ApiError::new(503, "not connected"));
        }
        let key = (req.service_type.clone(), req.app.clone());
        let prepared = lock(&self.dir).prepare_set(req)?;
        let source = IdentificationLink::parse(&req.source)
            .map_err(|e| ApiError::new(400, format!("source: {e}")))?;
        let target = target_of(&key)?.source(source).filter(&req.context);
        let options = WriteOptions::new()
            .timeout(self.settings.request_timeout)
            .data_set_writer_id(prepared.data_set_writer_id);
        let result = self.consumer.set_blocking(
            Resource::Config,
            &target,
            &prepared.payload.to_string(),
            &options,
        );
        let (http, outcome) = match result {
            Ok(code) | Err(oec::Error::Status(code)) => {
                (200, SetOutcome::Status(code.code(), symbol(code)))
            }
            Err(oec::Error::Timeout) => (504, SetOutcome::Failed("timeout".to_owned())),
            Err(oec::Error::NotConnected) => (503, SetOutcome::Failed("not connected".to_owned())),
            Err(e) => (500, SetOutcome::Failed(e.to_string())),
        };
        if matches!(outcome, SetOutcome::Status(code, _) if code >> 30 == 0) {
            match self
                .consumer
                .get_blocking(Resource::Config, &target, &self.options())
            {
                Ok(answers) => {
                    let mut dir = lock(&self.dir);
                    for r in &answers {
                        if let Some(e) = config_entry(r) {
                            let k = (req.source.clone(), req.context.clone());
                            dir.config(&key, k, e);
                        }
                    }
                }
                Err(e) => log::warn!("Get Config after Set: {e}"),
            }
        }
        let record = lock(&self.dir).record_set(req, prepared.shown, &outcome, now_ms());
        match &outcome {
            SetOutcome::Status(code, sym) => {
                log::info!(
                    "Set Config {} {}: {sym} (0x{code:08X})",
                    req.app,
                    req.context
                );
            }
            SetOutcome::Failed(e) => log::warn!("Set Config {} {}: {e}", req.app, req.context),
        }
        Ok((http, record))
    }

    /// `GET /api/apps`.
    pub(crate) fn apps(&self) -> Value {
        let health: HashMap<AppKey, (Value, Value)> = self
            .consumer
            .apps()
            .into_iter()
            .map(|a| {
                let state = a
                    .health
                    .and_then(|h| serde_json::to_value(h).ok())
                    .unwrap_or(Value::Null);
                (
                    key_of(a.service_type, &a.app),
                    (state, json!(a.health_score)),
                )
            })
            .collect();
        let mut out = lock(&self.dir)
            .snapshot(|k| health.get(k).cloned().unwrap_or((Value::Null, Value::Null)));
        out["connected"] = Value::Bool(self.connected.load(Ordering::SeqCst));
        out
    }

    /// `GET /api/sets`.
    pub(crate) fn sets(&self) -> Value {
        lock(&self.dir).sets()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn symbols() {
        assert_eq!(symbol(StatusCode::Good), "Good");
        assert_eq!(symbol(StatusCode::BadOutOfRange), "BadOutOfRange");
        assert_eq!(symbol(StatusCode::Other(0x8034_0000)), "0x80340000");
    }
}
