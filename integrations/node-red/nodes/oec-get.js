'use strict';
// oec-get: a Get request (MG 8.3) to an OEC application; the answer – all
// pages of a paginated answer – is sent as one message. The subscriptions
// for the answer only exist for the time of the request and are not listed
// in the SubscriptionList (SF-109).

const { bind } = require('../lib/nodes');
const consumer = require('../lib/consumer');
const oec = require('../lib/oec');

function pick(o, key, fallback) {
  const v = o && o[key];
  return v == null || v === '' ? fallback : v;
}

/** The request of the node settings and the overrides of msg.oec. */
function requestOf(config, msg) {
  const o = msg.oec && typeof msg.oec === 'object' ? msg.oec : {};
  const nonEmpty = (v) => (v == null || v === '' ? null : String(v));
  return {
    serviceType: pick(o, 'serviceType', config.serviceType),
    appIl: pick(o, 'appIl', config.appIl),
    resource: pick(o, 'resource', config.resource),
    sourceIl: nonEmpty(pick(o, 'sourceIl', config.sourceIl)),
    filter: nonEmpty(pick(o, 'filter', config.filter)),
    perPage: Number(pick(o, 'perPage', config.perPage || 0)),
    page: Number(pick(o, 'page', config.page || 0)),
    timeout: Number(pick(o, 'timeout', config.timeout || 5)),
  };
}

module.exports = function (RED) {
  function OecGetNode(config) {
    RED.nodes.createNode(this, config);
    const node = this;
    const b = bind(RED, node, config);
    if (!b) return;
    const pending = new Set();

    node.on('input', (msg, send, done) => {
      let req;
      let request;
      try {
        req = requestOf(config, msg);
        if (!Number.isInteger(req.perPage) || req.perPage < 0) throw new Error('perPage: an integer ≥ 0');
        if (!Number.isInteger(req.page) || req.page < 0) throw new Error('page: an integer ≥ 0');
        if (!(req.timeout > 0)) throw new Error('timeout: seconds > 0');
        if (b.app.state !== 'connected') throw new Error('the OEC application is not connected');
        request = b.app.provider.getRequest(req);
      } catch (err) {
        done(err);
        return;
      }
      const filters = [...new Set([
        `${oec.topic(req.serviceType, req.appIl, 'Pub', req.resource)}/#`,
        `${oec.topic(req.serviceType, req.appIl, 'Pub', 'Event')}/+/Status/#`,
      ])];
      const messages = [];
      let first = null;
      let pagination = null;
      const subs = [];
      const job = {};
      const finish = (err, quiet = false) => {
        if (job.over) return;
        job.over = true;
        pending.delete(job);
        clearTimeout(job.timer);
        for (const s of subs) s.unsubscribe();
        if (quiet) {
          done();
          return;
        }
        if (err) {
          b.status('failed');
          done(err);
          return;
        }
        b.status(`${messages.length} received`);
        msg.payload = messages;
        msg.oec = { ...consumer.describe(first.topic, first.message), request: req, requestId: request.messageId };
        if (pagination) msg.oec.pagination = pagination;
        send(msg);
        done();
      };
      job.cancel = () => finish(null, true);
      const handler = (topic, payload) => {
        const message = consumer.json(payload);
        if (!message || message.ReplyTo !== request.messageId) return;
        const t = oec.parseTopic(topic);
        if (t && t.resource === 'Event' && t.filter && t.filter[0] === 'Status' && req.resource !== 'Event') {
          const dsm = (message.Messages || [])[0];
          if (t.filter[1] === 'Bad') {
            const p = (dsm && dsm.Payload) || {};
            const symbol = (p.Details && p.Details.SymbolicId) || 'Bad';
            const e = new Error(`${symbol}: ${p.Description || 'the request failed'}`);
            e.status = symbol;
            finish(e);
          }
          return;
        }
        if (!first) first = { topic, message };
        if (message.MessageType === 'ua-metadata') {
          messages.push(message);
          finish();
          return;
        }
        let more = false;
        for (const dsm of message.Messages || []) {
          if (consumer.isPagination(dsm)) {
            pagination = dsm.Payload;
            more = !!(dsm.Payload && dsm.Payload.HasNext) && req.perPage === 0;
          } else {
            messages.push(dsm);
          }
        }
        if (!more) finish();
      };
      pending.add(job);
      for (const f of filters) subs.push(b.app.subscribe(f, handler));
      job.timer = setTimeout(() => finish(new Error(`no answer within ${req.timeout} s`)), req.timeout * 1000);
      b.status('requesting');
      Promise.all(subs.map((s) => s.ready)).then(
        () => {
          if (!job.over) b.app.publish([request]);
        },
        (err) => finish(err),
      );
    });

    node.on('close', () => {
      for (const job of [...pending]) job.cancel();
    });
  }
  RED.nodes.registerType('oec-get', OecGetNode);
};

module.exports.requestOf = requestOf;
