'use strict';
// Helpers shared by the resource nodes: resolve the OEC application and the
// optional asset (Source) of a node and show the state of the connection.

const STATE = {
  connected: { fill: 'green', shape: 'dot', text: 'connected' },
  connecting: { fill: 'yellow', shape: 'ring', text: 'connecting' },
  disconnected: { fill: 'red', shape: 'ring', text: 'disconnected' },
  invalid: { fill: 'red', shape: 'dot', text: 'invalid application' },
};

/**
 * Binds a resource node to its oec-app and optional oec-asset: returns
 * {app, il, status(extra), fail(err)} or null (status shows why).
 */
function bind(RED, node, config) {
  const app = config.app ? RED.nodes.getNode(config.app) : null;
  if (!app) {
    node.status({ fill: 'red', shape: 'dot', text: 'no OEC application' });
    return null;
  }
  if (!app.provider) {
    node.status(STATE.invalid);
    return null;
  }
  let il = app.provider.il;
  let asset = null;
  if (config.asset) {
    asset = RED.nodes.getNode(config.asset);
    if (!asset) {
      node.status({ fill: 'red', shape: 'dot', text: 'asset not found' });
      return null;
    }
    try {
      il = app.attachAsset(asset, node.id);
    } catch (err) {
      node.status({ fill: 'red', shape: 'dot', text: 'invalid asset' });
      node.error(`asset ${asset.name || asset.id}: ${err.message}`);
      return null;
    }
  }
  let extra = '';
  let failed = '';
  const show = () => {
    if (failed) {
      node.status({ fill: 'red', shape: 'dot', text: failed });
      return;
    }
    const s = STATE[app.state] || STATE.connecting;
    node.status({ ...s, text: extra ? `${s.text} · ${extra}` : s.text });
  };
  app.on('oec-state', show);
  show();
  node.on('close', () => {
    app.removeListener('oec-state', show);
    if (asset) app.detachAsset(asset, node.id);
  });
  return {
    app,
    il,
    status(text) {
      extra = text == null ? '' : String(text).slice(0, 40);
      failed = '';
      show();
    },
    fail(err) {
      failed = String(err && err.message ? err.message : err).slice(0, 60);
      show();
    },
  };
}

/** The Identification Link of a node setting or message property. */
function ilOf(value) {
  if (value == null || value === '') return null;
  return String(value);
}

module.exports = { bind, ilOf, STATE };
