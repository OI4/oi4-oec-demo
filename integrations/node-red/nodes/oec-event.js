'use strict';
// oec-event: Events (MG 9.1.6) of the application or of an asset – Syslog
// (RFC 5424 Facility and Severity) or Generic (own level and number).

const { bind } = require('../lib/nodes');
const oec = require('../lib/oec');

function severityOf(value) {
  if (value == null || value === '') return null;
  const n = Number(value);
  if (Number.isInteger(n) && n >= 0 && n <= 7) return n;
  const i = oec.SYSLOG_LEVELS.findIndex((l) => l.toLowerCase() === String(value).toLowerCase());
  if (i < 0) throw new Error(`severity ${JSON.stringify(value)}: 0 … 7 or ${oec.SYSLOG_LEVELS.join(', ')}`);
  return i;
}

/** The event of an input message and the node settings. */
function eventOf(config, msg) {
  if ((config.category || 'Syslog') === 'Syslog') {
    const facility = Number(msg.facility ?? config.facility ?? 1);
    if (!Number.isInteger(facility) || facility < 0 || facility > 23) throw new Error('facility: 0 … 23');
    const severity = severityOf(msg.severity) ?? severityOf(config.severity) ?? 5;
    const p = msg.payload;
    const details = p && typeof p === 'object' ? { MSG: p.MSG, HEADER: p.HEADER } : { MSG: p == null ? undefined : String(p) };
    return { category: 'Syslog', number: facility * 8 + severity, details };
  }
  const p = msg.payload;
  return {
    category: 'Generic',
    level: msg.level ?? config.level,
    number: msg.number ?? (config.number === '' || config.number == null ? 0 : Number(config.number)),
    description: msg.description ?? (typeof p === 'string' ? p : config.description),
    details: p && typeof p === 'object' ? p : undefined,
  };
}

module.exports = function (RED) {
  function OecEventNode(config) {
    RED.nodes.createNode(this, config);
    const node = this;
    const b = bind(RED, node, config);
    if (!b) return;
    node.on('input', (msg, send, done) => {
      try {
        const ev = eventOf(config, msg);
        b.app.apply((p) => p.publishEvent(b.il, ev));
        if (ev.category === 'Syslog') {
          const level = oec.SYSLOG_LEVELS[ev.number % 8];
          const off = b.il === b.app.provider.il && !b.app.provider.syslogEnabled(level);
          b.status(off ? `${level} (off)` : level);
        } else {
          b.status(ev.level);
        }
        done();
      } catch (err) {
        done(err);
      }
    });
  }
  RED.nodes.registerType('oec-event', OecEventNode);
};

module.exports.eventOf = eventOf;
