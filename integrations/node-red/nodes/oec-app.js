'use strict';
// Config node oec-app: one OEC application (Messaging Level 2) on the
// message bus. It owns the protocol state (lib/provider.js) and the MQTT
// session (lib/session.js); the resource nodes use its API below.

const { Provider } = require('../lib/provider');
const { Session } = require('../lib/session');
const oec = require('../lib/oec');

const MAM_KEYS = [
  'manufacturer', 'manufacturerUri', 'model', 'productCode', 'serialNumber', 'hardwareRevision',
  'softwareRevision', 'deviceRevision', 'deviceManual', 'deviceClass', 'description', 'revisionCounter',
  'identificationLink',
];

function mamOf(config) {
  return Object.fromEntries(MAM_KEYS.map((k) => [k, config[k]]));
}

module.exports = function (RED) {
  const claimed = new Map();

  function OecAppNode(config) {
    RED.nodes.createNode(this, config);
    const node = this;
    node.name = config.name;
    node.state = 'connecting';
    node.setMaxListeners(0);
    node.assets = new Map();
    node.setHandlers = new Map();
    node.closed = false;
    node.provider = null;
    node.session = null;

    const brokerNode = RED.nodes.getNode(config.broker);
    try {
      if (!brokerNode) throw new Error('no OEC broker');
      const probe = new Provider({ serviceType: config.serviceType, mam: mamOf(config) });
      const b = brokerNode.describe(probe.il);
      // Several applications of one container share the container name: the
      // first one uses it as ClientId, the others add a suffix (MG 6.2.3).
      if (brokerNode.mode === 'container') {
        const key = `${brokerNode.id}|${b.options.clientId}`;
        const owner = claimed.get(key);
        if (owner && owner !== node.id) b.options.clientId = `${b.options.clientId}-${require('../lib/broker').clientIdOf(probe.il).slice(5, 13)}`;
        else claimed.set(key, node.id);
        node.on('close', () => {
          if (claimed.get(key) === node.id) claimed.delete(key);
        });
      }
      node.provider = new Provider({
        serviceType: config.serviceType, mam: mamOf(config), maxPacketSize: b.maxPacketSize, syslogLevel: config.syslogLevel || 'Notice',
      });
      const heartbeat = Math.min(Math.max(Number(config.heartbeat) || 30, 1), 60);
      node.session = new Session(node.provider, b.options, { heartbeatMs: heartbeat * 1000 });
      node.log(`${node.provider.serviceType}/${node.provider.il} → ${b.where} (${b.auth})`);
    } catch (err) {
      node.provider = null;
      node.error(`OEC application: ${err.message}`);
      node.state = 'invalid';
      return;
    }

    const session = node.session;
    session.on('state', (s) => node._state(s));
    session.on('error', (err) => {
      node.warn(`MQTT: ${err.message}`);
      if (!session.connected) node._state('disconnected');
    });
    session.on('set', (e) => {
      const handler = node.setHandlers.get(`${e.type}|${e.il}|${e.name}`);
      if (handler) handler(e);
    });
    session.start();

    node.on('close', (done) => {
      node.closed = true;
      session.stop().then(() => done(), () => done());
    });
  }

  OecAppNode.prototype._state = function (state) {
    if (this.state === state) return;
    this.state = state;
    this.emit('oec-state', state);
  };

  /** Publishes {topic, payload} items of the provider. */
  OecAppNode.prototype.publish = function (out) {
    if (!this.closed) this.session.publish(out);
  };

  /** Runs a change of the provider and publishes its output; throws OecError. */
  OecAppNode.prototype.apply = function (change) {
    const out = change(this.provider);
    this.publish(out);
    return out;
  };

  /** Publishes changed lists a little later (after a removal). */
  OecAppNode.prototype.flushLater = function () {
    if (!this.closed) this.session.scheduleFlush();
  };

  /** Registers an asset for a node; returns its Identification Link. */
  OecAppNode.prototype.attachAsset = function (assetNode, nodeId) {
    const { il, out } = this.provider.addAsset(assetNode.mam);
    let users = this.assets.get(il);
    if (!users) this.assets.set(il, (users = new Set()));
    users.add(nodeId);
    this.publish(out);
    return il;
  };

  OecAppNode.prototype.detachAsset = function (assetNode, nodeId) {
    for (const [il, users] of this.assets) {
      if (!users.delete(nodeId) || users.size) continue;
      this.assets.delete(il);
      this.provider.removeAsset(il);
      this.flushLater();
    }
  };

  /** handler({type, il, name, values, messageId}) for Set Data/Config. */
  OecAppNode.prototype.onSet = function (type, il, name, handler) {
    const key = `${type}|${il}|${name}`;
    this.setHandlers.set(key, handler);
    return () => {
      if (this.setHandlers.get(key) === handler) this.setHandlers.delete(key);
    };
  };

  /**
   * Subscribes a topic filter; `listed` adds it to the SubscriptionList.
   * Returns {ready, unsubscribe}.
   */
  OecAppNode.prototype.subscribe = function (filter, handler, { listed = false } = {}) {
    const sub = this.session.subscribe(filter, handler);
    if (listed) this.publish(this.provider.addConsumer(filter));
    let done = false;
    return {
      ready: sub.ready,
      unsubscribe: () => {
        if (done) return;
        done = true;
        sub.unsubscribe();
        if (listed) {
          this.provider.removeConsumer(filter);
          this.flushLater();
        }
      },
    };
  };

  RED.nodes.registerType('oec-app', OecAppNode);

  // The Identification Link of the settings in the editor (preview).
  RED.httpAdmin.post('/oec/identification-link', RED.auth.needsPermission('oec-app.read'), (req, res) => {
    try {
      const b = req.body || {};
      const il = b.identificationLink || oec.identificationLink(b.manufacturerUri || '', b.model || '', b.productCode || '', b.serialNumber || '');
      res.json({ il, appId: oec.encodeId(il) });
    } catch (err) {
      res.status(400).json({ error: err.message });
    }
  });
};

module.exports.mamOf = mamOf;
module.exports.MAM_KEYS = MAM_KEYS;
