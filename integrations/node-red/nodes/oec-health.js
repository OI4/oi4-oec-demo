'use strict';
// oec-health: the Health of the application or of an asset (MG 9.1.2).

const { bind } = require('../lib/nodes');
const oec = require('../lib/oec');

const SCORE = { NORMAL_0: 100, FAILURE_1: 0, CHECK_FUNCTION_2: 50, OFF_SPEC_3: 50, MAINTENANCE_REQUIRED_4: 75 };

/** {state, score} of an input payload: a state name or {Health, HealthScore}. */
function healthOf(payload, msg = {}) {
  let state;
  let score;
  if (payload && typeof payload === 'object') {
    state = payload.Health ?? payload.health ?? payload.state;
    score = payload.HealthScore ?? payload.healthScore ?? payload.score;
  } else {
    state = payload;
    score = msg.healthScore;
  }
  if (typeof state === 'string' && !oec.HEALTH_STATES.includes(state)) {
    const up = state.toUpperCase().replace(/[\s-]/g, '_');
    state = oec.HEALTH_STATES.find((s) => s.startsWith(`${up}_`)) || state;
  }
  if (score == null || score === '') score = SCORE[state] ?? 100;
  return { state, score: Number(score) };
}

module.exports = function (RED) {
  function OecHealthNode(config) {
    RED.nodes.createNode(this, config);
    const node = this;
    const b = bind(RED, node, config);
    if (!b) return;
    const apply = (h) => {
      b.app.apply((p) => p.setHealth(b.il, h.state, h.score));
      b.status(`${h.state} ${h.score}`);
    };
    if (config.initial) {
      try {
        apply({ state: config.initial, score: Number(config.score ?? SCORE[config.initial] ?? 100) });
      } catch (err) {
        node.error(err.message);
        b.fail(err);
      }
    }
    node.on('input', (msg, send, done) => {
      try {
        apply(healthOf(msg.payload, msg));
        done();
      } catch (err) {
        done(err);
      }
    });
  }
  RED.nodes.registerType('oec-health', OecHealthNode);
};

module.exports.healthOf = healthOf;
