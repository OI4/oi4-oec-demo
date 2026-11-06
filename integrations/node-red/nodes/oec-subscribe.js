'use strict';
// oec-subscribe: receives the publications of other OEC applications (or
// of its own) and sends one message per DataSetMessage. The subscription
// is listed in the SubscriptionList of the application (MG 9.1.9).

const { bind } = require('../lib/nodes');
const consumer = require('../lib/consumer');

module.exports = function (RED) {
  function OecSubscribeNode(config) {
    RED.nodes.createNode(this, config);
    const node = this;
    const b = bind(RED, node, config);
    if (!b) return;
    const filter = config.mode === 'topic'
      ? String(config.topic || '').trim()
      : consumer.topicFilter({
        serviceType: config.serviceType, appIl: config.appIl, resource: config.resource,
        sourceIl: config.sourceIl, filter: config.filter,
      });
    if (!filter) {
      node.error('no topic filter');
      b.fail('no topic filter');
      return;
    }
    const own = b.app.provider.publisherId;
    let count = 0;
    const handler = (topic, payload) => {
      const message = consumer.json(payload);
      if (!message) {
        node.warn(`${topic}: payload is no JSON object`);
        return;
      }
      if (config.ignoreOwn && message.PublisherId === own) return;
      const meta = consumer.describe(topic, message);
      count += 1;
      b.status(`${count} received`);
      if (config.output === 'message' || !Array.isArray(message.Messages)) {
        node.send({ topic, payload: message, oec: meta });
        return;
      }
      for (const dsm of message.Messages) {
        if (consumer.isPagination(dsm)) continue;
        node.send({ topic, payload: dsm.Payload, dataSetMessage: dsm, oec: { ...meta, dataSetWriterId: dsm.DataSetWriterId, writerGroupName: dsm.WriterGroupName, source: dsm.DataSetWriterName } });
      }
    };
    let sub;
    try {
      sub = b.app.subscribe(filter, handler, { listed: true });
    } catch (err) {
      node.error(err.message);
      b.fail(err);
      return;
    }
    sub.ready.catch((err) => {
      node.error(err.message);
      b.fail(err);
    });
    node.on('close', () => sub.unsubscribe());
  }
  RED.nodes.registerType('oec-subscribe', OecSubscribeNode);
};
