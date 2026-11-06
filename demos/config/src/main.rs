//! `oec-config`: the config service D10 of the OEC demos (ADR-0028 §7,
//! A-304). It finds the OEC applications on the Message Bus, reads their
//! Config and SubscriptionList, and changes writable Configs with
//! `Set Config`; a Web UI with its HTTP API (ADR-0028 §3, A-357) shows
//! them and edits the writable ones.
//!
//! Environment: `OEC_ROOT` (container storage, default `/`),
//! `OEC_CONFIG_FILE`, `OEC_UI_LISTEN` (see [`settings`]),
//! `OEC_UI_KEEPALIVE_MS` (time between the keep-alive comments of the
//! event streams, 1 … 15000 ms, default 15000; for tests) and
//! `OEC_CONFIG_LOG` (`error`, `warn`, `info` (default), `debug`) for the
//! local log on stderr.

mod api;
mod directory;
mod events;
mod http;
mod service;
mod settings;
mod topic;

use std::net::TcpListener;
use std::path::PathBuf;
use std::process::ExitCode;
use std::sync::mpsc;
use std::sync::{Arc, OnceLock, Weak};
use std::time::Duration;

use log::{LevelFilter, Log, Metadata, Record};
use oec::app::ConnectionState;
use oec::prelude::*;

use crate::events::Hub;
use crate::service::Service;
use crate::settings::Settings;

/// The local log on stderr (ADR-0028 §2); the SDK bridges the records to
/// Syslog events on the bus too.
struct Stderr(LevelFilter);

impl Log for Stderr {
    fn enabled(&self, m: &Metadata<'_>) -> bool {
        m.level() <= self.0
    }
    fn log(&self, r: &Record<'_>) {
        if self.enabled(r.metadata()) {
            eprintln!("{} {}: {}", r.level(), r.target(), r.args());
        }
    }
    fn flush(&self) {}
}

fn product() -> Product {
    Product::new("oi4.org", "OEC Config", "OEC-CONFIG")
        .manufacturer("Open Industry 4.0 Alliance")
        .description("Demo config service of OI4 Open Edge Computing")
        .software_revision(env!("CARGO_PKG_VERSION"))
}

/// `OEC_UI_KEEPALIVE_MS`: the time between the keep-alive comments.
fn keep_alive(value: Option<&str>) -> Result<Duration, String> {
    let Some(v) = value else {
        return Ok(events::KEEP_ALIVE);
    };
    match v.parse::<u64>() {
        Ok(ms @ 1..=15_000) => Ok(Duration::from_millis(ms)),
        _ => Err(format!(
            "Web UI: OEC_UI_KEEPALIVE_MS {v:?} is no number 1 … 15000"
        )),
    }
}

/// Binds the Web UI to `addr` (`host:port`, the host not empty).
fn listen(addr: &str) -> Result<TcpListener, String> {
    let host = addr.rsplit_once(':').map_or("", |(h, _)| h);
    if host.is_empty() {
        return Err(format!("Web UI: listen {addr:?}: host:port is required"));
    }
    TcpListener::bind(addr).map_err(|e| format!("Web UI: listen {addr:?}: {e}"))
}

/// Sends the event `apps` to the event streams whenever the snapshot
/// changes (looked at every 250 ms while a stream is open).
fn broadcast(service: &Weak<Service>, hub: &Hub) {
    let mut last: Option<String> = None;
    loop {
        std::thread::sleep(Duration::from_millis(250));
        let Some(service) = service.upgrade() else {
            return;
        };
        if hub.len() == 0 {
            last = None;
            continue;
        }
        let event = api::apps_event(&service.apps());
        if last.as_ref() != Some(&event) {
            hub.send(&event);
            last = Some(event);
        }
    }
}

fn run() -> Result<(), String> {
    let level = match std::env::var("OEC_CONFIG_LOG").as_deref() {
        Ok("error") => LevelFilter::Error,
        Ok("warn") => LevelFilter::Warn,
        Ok("debug") => LevelFilter::Debug,
        _ => LevelFilter::Info,
    };
    oec::event::install_bridge(Some(Box::new(Stderr(level)))).map_err(|e| e.to_string())?;
    let root = std::env::var_os("OEC_ROOT").map_or_else(|| PathBuf::from("/"), PathBuf::from);
    let settings = Settings::load(&root, |k| std::env::var(k).ok())?;
    let keep_alive = keep_alive(std::env::var("OEC_UI_KEEPALIVE_MS").ok().as_deref())?;
    let listener = settings.listen.as_deref().map(listen).transpose()?;

    let slot: Arc<OnceLock<Weak<Service>>> = Arc::new(OnceLock::new());
    let on_state = Arc::clone(&slot);
    let app = App::builder(Level::L1, ServiceType::Utility, product())
        .bootstrap(Bootstrap::ContainerStorage { root })
        .on_connection(move |state| {
            if let Some(s) = on_state.get().and_then(Weak::upgrade) {
                s.connection(state == ConnectionState::Online);
            }
        })
        .build()
        .map_err(|e| e.to_string())?;
    let me = (
        ServiceType::Utility.as_str().to_owned(),
        app.identification_link().as_str().to_owned(),
    );
    let (jobs, queue) = mpsc::channel();
    let service = Service::new(app.consumer(), settings, me, jobs);
    let _ = slot.set(Arc::downgrade(&service));
    service.watch().map_err(|e| e.to_string())?;

    let worker = Arc::clone(&service);
    std::thread::Builder::new()
        .name("oec-config-discovery".to_owned())
        .spawn(move || worker.work(&queue))
        .map_err(|e| e.to_string())?;
    if let Some(listener) = listener {
        log::info!(
            "Web UI on http://{}/",
            listener.local_addr().map_err(|e| e.to_string())?
        );
        let hub = Hub::new(keep_alive);
        let (source, sink) = (Arc::downgrade(&service), Arc::clone(&hub));
        std::thread::Builder::new()
            .name("oec-config-events".to_owned())
            .spawn(move || broadcast(&source, &sink))
            .map_err(|e| e.to_string())?;
        let routed = Arc::clone(&service);
        let handler: http::Handler = Arc::new(move |r| api::route(&*routed, &hub, r));
        std::thread::Builder::new()
            .name("oec-config-http".to_owned())
            .spawn(move || http::serve(&listener, &handler))
            .map_err(|e| e.to_string())?;
    } else {
        log::info!("Web UI off");
    }
    app.run().map_err(|e| e.to_string())
}

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("oec-config: {e}");
            ExitCode::FAILURE
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keep_alive_values() {
        assert_eq!(keep_alive(None), Ok(events::KEEP_ALIVE));
        assert_eq!(keep_alive(Some("1")), Ok(Duration::from_millis(1)));
        assert_eq!(keep_alive(Some("15000")), Ok(Duration::from_secs(15)));
        for bad in ["0", "15001", "x", "", "-1"] {
            assert!(keep_alive(Some(bad)).is_err(), "{bad}");
        }
    }

    #[test]
    fn listen_addresses() {
        for bad in [
            ":8783",
            "no-port",
            "127.0.0.1:",
            "127.0.0.1:99999",
            "[::1:80",
        ] {
            let e = listen(bad).unwrap_err();
            assert!(e.starts_with("Web UI: listen"), "{bad}: {e}");
        }
        let l = listen("127.0.0.1:0").unwrap();
        let busy = l.local_addr().unwrap().to_string();
        assert!(listen(&busy).is_err());
    }
}
