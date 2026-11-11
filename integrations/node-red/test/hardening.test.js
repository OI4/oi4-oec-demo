'use strict';
// C-113 red/blue team: names from a bus payload or a flow never resolve
// inherited properties (RD-3), and the answer to a `Set Config` does not
// depend on a Sensitive DefaultValue (RS-1 analog).
const test = require('node:test');
const assert = require('node:assert/strict');
const oec = require('../lib/oec');
const { Provider } = require('../lib/provider');

const MAM = { manufacturer: 'Example', manufacturerUri: 'example.com', model: 'Flow', productCode: 'NR', serialNumber: '1' };

function make() {
  let now = Date.parse('2026-11-28T12:00:00Z');
  return new Provider({ serviceType: 'Utility', mam: MAM, clock: () => (now += 1) });
}

function request(p, method, resource, { source, filter, messages = [] } = {}) {
  const topic = oec.topic(p.serviceType, p.il, method, resource, source, filter);
  return p.handle(topic, oec.networkMessage('99-Utility/requester', 'Utility/requester', messages, ''));
}

/** The StatusCode name of the status Event of an answer. */
function statusOf(result) {
  const event = result.out.map(({ payload }) => JSON.parse(payload)).at(-1);
  return event.Messages[0].Payload.Details.SymbolicId;
}

/** Runs `fn`, then asserts and repairs that it did not pollute Object.prototype. */
function unpolluted(fn) {
  try {
    fn();
  } finally {
    const polluted = Object.getOwnPropertyNames(Object.prototype).filter((k) => ['Value', 'Type', 'x'].includes(k));
    for (const k of polluted) delete Object.prototype[k];
    for (const k of ['Value', 'x']) delete Object[k];
    assert.deepEqual(polluted, [], 'Object.prototype polluted');
    assert.equal(({}).Value, undefined);
  }
}

function withConfig() {
  const p = make();
  p.connected();
  p.defineConfig(p.il, 'cfg', { S: { Interval: { Type: 'Number', Value: '60' } } });
  return p;
}

const SET_PAYLOADS = [
  ['group constructor, object prototype', { constructor: { prototype: { Value: 'pwned' } } }],
  ['group __proto__', JSON.parse('{"__proto__":{"hasOwnProperty":{"Value":"pwned"}}}')],
  ['object __proto__', JSON.parse('{"S":{"__proto__":{"Value":"pwned"}}}')],
  ['object constructor', { S: { constructor: { Value: 'pwned' } } }],
  ['group hasOwnProperty', { hasOwnProperty: { prototype: { Value: 'pwned' } } }],
  ['group toString', { toString: { prototype: { Value: 'pwned' } } }],
  ['object toString', { S: { toString: { Value: 'pwned' } } }],
];

for (const [what, payload] of SET_PAYLOADS) {
  test(`RD-3: Set Config with ${what} → BadNotFound, no pollution`, () => {
    unpolluted(() => {
      const p = withConfig();
      const direct = p._setConfig(p.app, 'cfg', { MessageId: 'x', Messages: [{ DataSetWriterId: 100, Payload: payload }] });
      assert.equal(direct.status, 'BadNotFound');
      const bus = request(p, 'Set', 'Config', { source: p.il, filter: 'cfg', messages: [{ DataSetWriterId: 100, Payload: payload }] });
      assert.equal(statusOf(bus), 'BadNotFound');
      assert.deepEqual(p.configValues(p.il, 'cfg'), { S: { Interval: '60' } });
    });
  });
}

test('RD-3: setConfigValues of a flow with inherited names throws, no pollution', () => {
  unpolluted(() => {
    const p = withConfig();
    for (const values of [
      { constructor: { prototype: { Value: 'pwned' } } },
      { constructor: { x: 'pwned' } },
      JSON.parse('{"S":{"__proto__":"pwned"}}'),
      JSON.parse('{"__proto__":{"Value":"pwned"}}'),
      { S: { constructor: 'pwned' } },
    ]) {
      assert.throws(() => p.setConfigValues(p.il, 'cfg', values), oec.OecError);
    }
    assert.deepEqual(p.configValues(p.il, 'cfg'), { S: { Interval: '60' } });
  });
});

test('RD-3: the names constructor and prototype are plain Config names', () => {
  unpolluted(() => {
    const p = make();
    p.connected();
    p.defineConfig(p.il, 'cfg', {
      constructor: { prototype: { Value: 'a' }, Value: { Value: 'b', Mandatory: true } },
    }, {});
    assert.deepEqual(p.configValues(p.il, 'cfg'), { constructor: { prototype: 'a', Value: 'b' } });
    assert.deepEqual(p.configTypedValues(p.il, 'cfg'), { constructor: { prototype: 'a', Value: 'b' } });
    p.setConfigValues(p.il, 'cfg', { constructor: { prototype: 'c' } });
    const set = (payload) => request(p, 'Set', 'Config', { source: p.il, filter: 'cfg', messages: [{ DataSetWriterId: 100, Payload: payload }] });
    assert.equal(statusOf(set({ constructor: { prototype: { Value: 'd' } } })), 'BadInvalidArgument', 'Mandatory Value missing');
    assert.equal(statusOf(set({ constructor: { Value: { Value: 'e' } } })), 'Good');
    assert.deepEqual(p.configValues(p.il, 'cfg'), { constructor: { prototype: 'c', Value: 'e' } });
    assert.equal(typeof Object.prototype, 'object');
  });
});

test('RD-3: __proto__ is no Config name and no field name', () => {
  assert.throws(() => oec.normalizeConfig(JSON.parse('{"__proto__":{"X":{"Value":"1"}}}')), oec.OecError);
  assert.throws(() => oec.normalizeConfig(JSON.parse('{"S":{"__proto__":{"Value":"1"}}}')), oec.OecError);
  assert.throws(() => oec.normalizeFields([{ name: '__proto__', type: 'Double' }]), oec.OecError);
  assert.throws(() => oec.deriveFields(JSON.parse('{"__proto__":1}')), oec.OecError);
  assert.throws(() => oec.normalizeFields([{ name: 'V', type: 'constructor' }]), oec.OecError);
  assert.throws(() => oec.normalizeFields([{ name: 'V', type: 'toString' }]), oec.OecError);
});

test('RD-3: Set Data and publishData ignore inherited field values', () => {
  unpolluted(() => {
    const p = make();
    p.connected();
    p.defineData(p.il, 'D', { fields: { constructor: 'String', V: 'Double' }, writable: true });
    assert.throws(() => p.publishData(p.il, 'D', { V: 1 }), /no value for field constructor/);
    p.publishData(p.il, 'D', { constructor: 'a', V: 1 });
    const set = (payload) => request(p, 'Set', 'Data', { source: p.il, filter: 'D', messages: [{ DataSetWriterId: 100, Payload: payload }] });
    assert.equal(statusOf(set({ V: 2 })), 'BadInvalidArgument');
    assert.equal(statusOf(set({ constructor: 'b', V: 2 })), 'Good');
  });
});

test('RS-1 analog: the status of a Set Config does not reveal a Sensitive DefaultValue', () => {
  const p = make();
  p.connected();
  p.defineConfig(p.il, 'cfg', { S: { Token: { Type: 'String', Value: 'v', DefaultValue: 'secret-default', Sensitive: true } } });
  const current = JSON.parse(request(p, 'Get', 'Config', { source: p.il, filter: 'cfg' }).out[0].payload).Messages[0];
  assert.equal(current.Payload.S.Token.DefaultValue, oec.SENSITIVE_MASK);
  const set = (defaultValue) => {
    const payload = structuredClone(current.Payload);
    if (defaultValue === undefined) delete payload.S.Token.DefaultValue;
    else payload.S.Token.DefaultValue = defaultValue;
    return statusOf(request(p, 'Set', 'Config', { source: p.il, filter: 'cfg', messages: [{ ...current, Payload: payload }] }));
  };
  assert.equal(set(undefined), 'Good');
  assert.equal(set(oec.SENSITIVE_MASK), 'Good');
  assert.equal(set('secret-default'), 'BadNotWritable', 'the right guess');
  assert.equal(set('wrong-guess'), 'BadNotWritable');
  assert.equal(set(''), 'BadNotWritable');
  assert.equal(oec.parseConfigSet(p.app.configs.get('cfg').config, { S: { Token: { Value: 'w', DefaultValue: 'secret-default' } } }).status, 'BadNotWritable');
});

test('RS-1 analog: an empty Sensitive DefaultValue is compared as published', () => {
  const c = oec.normalizeConfig({ S: { Token: { Value: 'v', DefaultValue: '', Sensitive: true } } });
  assert.equal(oec.maskedConfig(c).S.Token.DefaultValue, '');
  assert.deepEqual(oec.parseConfigSet(c, { S: { Token: { Value: 'w', DefaultValue: '' } } }), { changes: [['S', 'Token', 'w']] });
  assert.deepEqual(oec.parseConfigSet(c, { S: { Token: { Value: 'w', DefaultValue: oec.SENSITIVE_MASK } } }), { changes: [['S', 'Token', 'w']] });
  assert.equal(oec.parseConfigSet(c, { S: { Token: { Value: 'w', DefaultValue: 'x' } } }).status, 'BadNotWritable');
});
