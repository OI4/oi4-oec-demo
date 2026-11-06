'use strict';
// Helpers of the consumer nodes oec-subscribe and oec-get: topic filters of
// other OEC applications and the messages received from them.

const oec = require('./oec');

/** The filter levels of a Filter setting ("Syslog/Error" → 2 levels). */
function filterLevels(filter) {
  if (filter == null || filter === '') return [];
  return String(filter).split('/').map((l) => (l === '+' || l === '#' ? l : oec.encodeLevel(l)));
}

/**
 * The topic filter of {serviceType, appIl, method, resource, sourceIl,
 * filter}; empty values become wildcards.
 */
function topicFilter({ serviceType, appIl, method = 'Pub', resource, sourceIl, filter }) {
  const levels = [oec.TOPIC_ROOT, serviceType || '+', appIl ? oec.encodeId(appIl) : '+', method];
  if (!resource) return [...levels, '#'].join('/');
  levels.push(resource);
  if (!sourceIl) {
    if (filter) return [...levels, '+', ...filterLevels(filter)].join('/');
    return [...levels, '#'].join('/');
  }
  levels.push(oec.encodeId(sourceIl));
  levels.push(...(filter ? filterLevels(filter) : ['#']));
  return levels.join('/');
}

/** Parses a received payload: the JSON object or null. */
function json(payload) {
  try {
    const v = JSON.parse(Buffer.isBuffer(payload) ? payload.toString('utf8') : String(payload));
    return v && typeof v === 'object' && !Array.isArray(v) ? v : null;
  } catch {
    return null;
  }
}

/** Whether a DataSetMessage is the Pagination of a response (MG 8.3.5). */
function isPagination(m) {
  return m && m.DataSetWriterId === oec.DSW_PAGINATION && m.WriterGroupName === 'Pagination';
}

/** The `msg.oec` description of a received topic and NetworkMessage. */
function describe(topicName, message) {
  const t = oec.parseTopic(topicName);
  const out = { topic: topicName };
  if (t) {
    Object.assign(out, {
      serviceType: t.serviceType, appIl: t.appIl, method: t.method, resource: t.resource,
      sourceIl: t.sourceIl, filter: t.filter ? t.filter.join('/') : null,
    });
  }
  if (message) {
    Object.assign(out, {
      messageId: message.MessageId, messageType: message.MessageType, publisherId: message.PublisherId,
      replyTo: message.ReplyTo || '', dataSetClassId: message.DataSetClassId,
    });
  }
  return out;
}

module.exports = { filterLevels, topicFilter, json, isPagination, describe };
