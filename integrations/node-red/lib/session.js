'use strict';
// The MQTT session of an OEC application (MG 6): connect with a Last Will
// created anew for every connection, subscribe the requests, publish the
// Birth and everything else of Provider.connected(), the Health heartbeat,
// answers to requests and NORMAL_0/0 before a graceful disconnect. QoS 0,
// no retain (REQ-MQTT-018/019).

const { EventEmitter } = require('node:events');
const oec = require('./oec');

const HEARTBEAT_MS = 30000;
const FLUSH_MS = 250;
const STOP_MS = 3000;

class Session extends EventEmitter {
  /**
   * @param {import('./provider').Provider} provider
   * @param {object} options  mqtt.connect() options (lib/broker.js connectOptions)
   * @param {object} [o]
   * @param {object} [o.mqtt]        the mqtt module (tests)
   * @param {number} [o.heartbeatMs] interval of the Health heartbeat
   */
  constructor(provider, options, { mqtt = require('mqtt'), heartbeatMs = HEARTBEAT_MS } = {}) {
    super();
    this.provider = provider;
    this.options = options;
    this.mqtt = mqtt;
    this.heartbeatMs = heartbeatMs;
    this.client = null;
    this.connected = false;
    this.subscriptions = new Map();
    this.unsubscribing = new Map();
    this.timer = null;
    this.flushTimer = null;
    this.closing = false;
  }

  start() {
    const will = this.provider.will();
    const options = { ...this.options, will: { topic: will.topic, payload: will.payload, qos: 0, retain: false } };
    const client = this.mqtt.connect(options);
    this.client = client;
    client.on('connect', () => this._connected());
    // REQ-LIFE-016: a new Last Will (new MessageId) for every connection;
    // mqtt.js emits 'reconnect' right before it opens the next connection.
    client.on('reconnect', () => {
      const w = this.provider.will();
      client.options.will = { topic: w.topic, payload: w.payload, qos: 0, retain: false };
    });
    client.on('close', () => this._closed());
    client.on('offline', () => this._closed());
    client.on('error', (err) => this.emit('error', err));
    client.on('message', (topic, payload) => this._message(topic, payload));
  }

  _connected() {
    const filters = [...this.provider.requestFilters(), ...this.subscriptions.keys()];
    this.client.subscribe(filters, { qos: 0 }, (err) => {
      if (err) {
        this.emit('error', err);
        return;
      }
      this.connected = true;
      this.publish(this.provider.connected());
      clearInterval(this.timer);
      this.timer = setInterval(() => this.publish(this.provider.heartbeat()), this.heartbeatMs);
      this.emit('state', 'connected');
    });
  }

  _closed() {
    clearInterval(this.timer);
    this.timer = null;
    if (!this.connected) return;
    this.connected = false;
    this.provider.disconnected();
    if (!this.closing) this.emit('state', 'disconnected');
  }

  _message(topic, payload) {
    if (this.provider.requestFilters().some((f) => oec.topicMatches(f, topic))) {
      const { out, events } = this.provider.handle(topic, payload);
      this.publish(out);
      for (const e of events) this.emit('set', e);
    }
    for (const [filter, handlers] of this.subscriptions) {
      if (!oec.topicMatches(filter, topic)) continue;
      for (const h of [...handlers]) {
        try {
          h(topic, payload);
        } catch (err) {
          this.emit('error', err);
        }
      }
    }
  }

  /** Publishes {topic, payload} items while connected (QoS 0, no retain). */
  publish(items) {
    if (!this.connected || !items) return;
    for (const { topic, payload } of items) this.client.publish(topic, payload, { qos: 0, retain: false });
  }

  /** Publishes changed lists a little later (after removals). */
  scheduleFlush() {
    if (this.flushTimer) return;
    this.flushTimer = setTimeout(() => {
      this.flushTimer = null;
      this.publish(this.provider.flushLists());
    }, FLUSH_MS);
  }

  /**
   * Subscribes a topic filter for a handler(topic, payload); resolves when
   * the broker granted it (or at once while offline: the subscription is
   * made with the next connect). Returns a function that unsubscribes.
   */
  subscribe(filter, handler) {
    let handlers = this.subscriptions.get(filter);
    const fresh = !handlers;
    if (fresh) {
      handlers = new Set();
      this.subscriptions.set(filter, handlers);
    }
    handlers.add(handler);
    const unsubscribe = () => {
      const hs = this.subscriptions.get(filter);
      if (!hs || !hs.delete(handler) || hs.size) return;
      this.subscriptions.delete(filter);
      if (!this.connected || this.provider.requestFilters().includes(filter)) return;
      // A new subscription of the filter waits until the broker confirmed
      // the unsubscription, so that it cannot remove the new one.
      const gone = new Promise((resolve) => this.client.unsubscribe(filter, () => resolve()));
      this.unsubscribing.set(filter, gone);
      gone.then(() => {
        if (this.unsubscribing.get(filter) === gone) this.unsubscribing.delete(filter);
      });
    };
    const ready = (fresh && this.connected ? (this.unsubscribing.get(filter) || Promise.resolve()) : null);
    const subscribed = !ready ? Promise.resolve() : ready.then(() => new Promise((resolve, reject) => {
      if (!this.connected || this.subscriptions.get(filter) !== handlers) {
        resolve();
        return;
      }
      this.client.subscribe(filter, { qos: 0 }, (err, granted) => {
        if (err) reject(err);
        else if (granted && granted.some((g) => g.qos === 128)) reject(new Error(`the broker refused the subscription ${filter}`));
        else resolve();
      });
    }));
    return { ready: subscribed, unsubscribe };
  }

  /** NORMAL_0/0 about the application, then a graceful disconnect (MG 6.3.4). */
  stop() {
    return new Promise((resolve) => {
      this.closing = true;
      clearInterval(this.timer);
      clearTimeout(this.flushTimer);
      const client = this.client;
      if (!client) {
        resolve();
        return;
      }
      // mqtt.js does not always call back end() while a connection is being
      // set up: force it after a while.
      const force = setTimeout(() => {
        client.end(true);
        resolve();
      }, STOP_MS);
      const done = () => {
        clearTimeout(force);
        resolve();
      };
      if (this.connected) {
        const close = this.provider.close();
        this.connected = false;
        client.publish(close.topic, close.payload, { qos: 0, retain: false }, () => client.end(false, {}, done));
      } else {
        client.end(true, {}, done);
      }
    });
  }
}

module.exports = { Session, HEARTBEAT_MS };
