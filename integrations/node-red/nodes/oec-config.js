'use strict';
// oec-config: a Config DataSet (MG 9.1.3) of the application or of an
// asset. The values are kept in the node context; a `Set Config` of the
// message bus, an input with values and the start send the typed values to
// the output (msg.oec.reason "set", "input", "init" or "get").

const { bind } = require('../lib/nodes');

module.exports = function (RED) {
  function OecConfigNode(config) {
    RED.nodes.createNode(this, config);
    const node = this;
    const b = bind(RED, node, config);
    if (!b) return;
    const name = config.contextName;
    const ctx = node.context();
    let definition;
    try {
      definition = JSON.parse(config.definition || '{}');
      if (config.description) definition.Context = { ...(definition.Context || {}), Description: config.description };
      b.app.apply((p) => p.defineConfig(b.il, name, definition, ctx.get('values') || null));
    } catch (err) {
      node.error(`Config ${name}: ${err.message}`);
      b.fail(err);
      return;
    }
    const emit = (reason, messageId) => {
      const msg = { topic: name, payload: b.app.provider.configTypedValues(b.il, name), oec: { reason, source: b.il, context: name } };
      if (messageId) msg.oec.messageId = messageId;
      node.send(msg);
    };
    const store = () => ctx.set('values', b.app.provider.configValues(b.il, name));
    const offSet = b.app.onSet('config', b.il, name, (e) => {
      store();
      b.status('set');
      emit('set', e.messageId);
    });
    const started = () => emit('init');
    RED.events.once('flows:started', started);
    node.on('input', (msg, send, done) => {
      try {
        if (msg.payload && typeof msg.payload === 'object' && Object.keys(msg.payload).length) {
          b.app.apply((p) => p.setConfigValues(b.il, name, msg.payload));
          store();
          b.status(null);
          emit('input');
        } else {
          emit('get');
        }
        done();
      } catch (err) {
        done(err);
      }
    });
    node.on('close', () => {
      RED.events.removeListener('flows:started', started);
      offSet();
      b.app.provider.removeConfig(b.il, name);
      b.app.flushLater();
    });
  }
  RED.nodes.registerType('oec-config', OecConfigNode);
};
