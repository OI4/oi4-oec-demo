//! The event stream of the Web UI (`GET /api/events`, ADR-0028 §3): a hub
//! that hands every event to at most [`MAX_STREAMS`] subscribers. A
//! subscriber that does not take its events in time is dropped (its
//! stream ends, the browser connects again and gets a fresh snapshot).

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{Receiver, SyncSender, TrySendError, sync_channel};
use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;

/// At most this many event streams at a time; more get a 503.
pub(crate) const MAX_STREAMS: usize = 8;
/// Events queued for one subscriber.
const QUEUE: usize = 64;
/// The keep-alive comment after this long without an event.
pub(crate) const KEEP_ALIVE: Duration = Duration::from_secs(15);

/// The hub.
#[derive(Debug)]
pub(crate) struct Hub {
    clients: Mutex<Vec<(u64, SyncSender<Arc<str>>)>>,
    next: AtomicU64,
    keep_alive: Duration,
}

/// A subscriber: its first event and the following ones; it leaves the
/// hub when dropped.
#[derive(Debug)]
pub(crate) struct Subscription {
    hub: Arc<Hub>,
    id: u64,
    pub(crate) first: String,
    pub(crate) events: Receiver<Arc<str>>,
}

impl Subscription {
    /// The time without an event after which the stream sends a comment.
    pub(crate) fn keep_alive(&self) -> Duration {
        self.hub.keep_alive
    }
}

impl Drop for Subscription {
    fn drop(&mut self) {
        self.hub.clients().retain(|(id, _)| *id != self.id);
    }
}

impl Hub {
    /// A hub whose streams send a keep-alive after `keep_alive`.
    pub(crate) fn new(keep_alive: Duration) -> Arc<Self> {
        Arc::new(Self {
            clients: Mutex::new(Vec::new()),
            next: AtomicU64::new(1),
            keep_alive,
        })
    }

    fn clients(&self) -> std::sync::MutexGuard<'_, Vec<(u64, SyncSender<Arc<str>>)>> {
        self.clients.lock().unwrap_or_else(PoisonError::into_inner)
    }

    /// A new subscriber with the event `first`; `None` if there are
    /// [`MAX_STREAMS`] already.
    pub(crate) fn subscribe(self: &Arc<Self>, first: String) -> Option<Subscription> {
        let mut clients = self.clients();
        if clients.len() >= MAX_STREAMS {
            return None;
        }
        let id = self.next.fetch_add(1, Ordering::Relaxed);
        let (tx, rx) = sync_channel(QUEUE);
        clients.push((id, tx));
        Some(Subscription {
            hub: Arc::clone(self),
            id,
            first,
            events: rx,
        })
    }

    /// The number of subscribers.
    pub(crate) fn len(&self) -> usize {
        self.clients().len()
    }

    /// Hands `data` to every subscriber; a full or closed one is dropped.
    pub(crate) fn send(&self, data: &str) {
        let data: Arc<str> = Arc::from(data);
        self.clients()
            .retain(|(_, tx)| match tx.try_send(Arc::clone(&data)) {
                Ok(()) => true,
                Err(TrySendError::Full(_) | TrySendError::Disconnected(_)) => false,
            });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn subscribers_are_limited_and_leave_when_dropped() {
        let hub = Hub::new(KEEP_ALIVE);
        let subs: Vec<Subscription> = (0..MAX_STREAMS)
            .map(|i| hub.subscribe(format!("first {i}")).unwrap())
            .collect();
        assert!(hub.subscribe("x".to_owned()).is_none());
        assert_eq!(hub.len(), MAX_STREAMS);
        hub.send("one");
        for s in &subs {
            assert_eq!(&*s.events.try_recv().unwrap(), "one");
        }
        drop(subs);
        assert_eq!(hub.len(), 0);
        let s = hub.subscribe("again".to_owned()).unwrap();
        assert_eq!((s.first.as_str(), s.keep_alive()), ("again", KEEP_ALIVE));
    }

    #[test]
    fn a_slow_subscriber_is_dropped() {
        let hub = Hub::new(KEEP_ALIVE);
        let slow = hub.subscribe(String::new()).unwrap();
        let fast = hub.subscribe(String::new()).unwrap();
        for i in 0..=QUEUE {
            hub.send(&i.to_string());
            assert_eq!(&*fast.events.try_recv().unwrap(), i.to_string());
        }
        assert_eq!(hub.len(), 1);
        let got: Vec<_> = slow.events.try_iter().collect();
        assert_eq!(got.len(), QUEUE);
        assert!(slow.events.try_recv().is_err());
    }
}
