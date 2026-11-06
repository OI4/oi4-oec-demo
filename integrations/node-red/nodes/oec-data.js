'use strict';
// oec-data: a Data DataSet (MG 9.1.4) of the application or of an asset with
// its Metadata (MG 9.1.5). Input publishes values; with "writable" a
// `Set Data` of the message bus is accepted and sent to the output.

const { bind } = require('../lib/nodes');

function parseFields(text) {
  if (text == null || String(text).trim() === '') return null;
  const fields = JSON.parse(text);
  if (Array.isArray(fields) || (fields && typeof fields === 'object')) return fields;
  throw new Error('fields: a JSON array or object');
}

/** The values of an input payload: an object, else the first field. */
function valuesOf(payload, fields) {
  if (payload && typeof payload === 'object' && !Array.isArray(payload) && !Buffer.isBuffer(payload)) return payload;
  let first = 'Value';
  if (Array.isArray(fields) && fields.length) first = fields[0].Name ?? fields[0].name ?? first;
  else if (fields && typeof fields === 'object' && Object.keys(fields).length) first = Object.keys(fields)[0];
  return { [first]: payload };
}

function timeOf(value) {
  if (value == null || value === '') return undefined;
  const ms = typeof value === 'number' ? value : Date.parse(value);
  if (!Number.isFinite(ms)) throw new Error(`msg.timestamp ${JSON.stringify(value)} is no time`);
  return ms;
}

module.exports = function (RED) {
  function OecDataNode(config) {
    RED.nodes.createNode(this, config);
    const node = this;
    const b = bind(RED, node, config);
    if (!b) return;
    const tag = config.tag;
    let fields;
    try {
      fields = parseFields(config.fields);
      b.app.provider.defineData(b.il, tag, { fields, writable: !!config.writable, description: config.description });
    } catch (err) {
      node.error(`Data ${tag}: ${err.message}`);
      b.fail(err);
      return;
    }
    const offSet = b.app.onSet('data', b.il, tag, (e) => {
      b.status('set');
      node.send({ topic: tag, payload: e.values, oec: { reason: 'set', source: b.il, tag, messageId: e.messageId } });
    });
    node.on('input', (msg, send, done) => {
      try {
        b.app.apply((p) => p.publishData(b.il, tag, valuesOf(msg.payload, fields), timeOf(msg.timestamp)));
        b.status(null);
        done();
      } catch (err) {
        done(err);
      }
    });
    node.on('close', () => {
      offSet();
      b.app.provider.removeData(b.il, tag);
      b.app.flushLater();
    });
  }
  RED.nodes.registerType('oec-data', OecDataNode);
};

module.exports.valuesOf = valuesOf;
