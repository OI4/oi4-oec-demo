'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const oec = require('../lib/oec');

test('dnp-encoding and Identification Link (MG 4.1, A3)', () => {
  assert.equal(oec.dnpEncode('Node-RED_1.0~x'), 'Node-RED_1.0~x');
  assert.equal(oec.dnpEncode('a b/c'), 'a,20b,2Fc');
  assert.equal(oec.dnpEncode('ä'), ',C3,A4');
  assert.equal(oec.identificationLink('example.com', 'My Model', 'P/1', 'S 1'), 'example.com/My,20Model/P,2F1/S,201');
});

test('AppId levels are base64url without padding (MG 7.4)', () => {
  const il = 'example.com/Model/PC/SN';
  const id = oec.encodeId(il);
  assert.doesNotMatch(id, /[=+/]/);
  assert.equal(oec.decodeId(id), il);
  assert.throws(() => oec.decodeId('a'), oec.OecError);
  assert.throws(() => oec.decodeId('YQ=='), oec.OecError);
});

test('topics and filter levels', () => {
  const t = oec.topic('Utility', 'a.com/M/P/S', 'Pub', 'Event', 'a.com/M/P/S', ['Generic', 'a+b#c/d%']);
  assert.equal(t.split('/').length, 8);
  assert.match(t, /Generic\/a%2Bb%23c%2Fd%25$/);
  const p = oec.parseTopic(t);
  assert.equal(p.serviceType, 'Utility');
  assert.equal(p.appIl, 'a.com/M/P/S');
  assert.equal(p.resource, 'Event');
  assert.deepEqual(p.filter, ['Generic', 'a+b#c/d%']);
  assert.equal(oec.parseTopic('foo/bar'), null);
  assert.equal(oec.parseTopic('Oi4v2/Nope/x/Pub/MAM'), null);
});

test('topic filter matching', () => {
  assert.ok(oec.topicMatches('a/+/c', 'a/b/c'));
  assert.ok(oec.topicMatches('a/#', 'a'));
  assert.ok(oec.topicMatches('a/#', 'a/b/c'));
  assert.ok(!oec.topicMatches('a/+', 'a/b/c'));
  assert.ok(!oec.topicMatches('a/b/c', 'a/b'));
});

test('MessageIds increase strictly', () => {
  let now = 1000;
  const ids = new oec.MessageIds('Utility/x', () => now);
  const a = ids.next();
  const b = ids.next();
  assert.equal(a, '1000-Utility/x');
  assert.equal(b, '1001-Utility/x');
  now = 5000;
  assert.equal(ids.next(), '5000-Utility/x');
});

test('fields: both key styles, derived fields and values', () => {
  const a = oec.normalizeFields([{ Name: 'T', Type: 'Double', Unit: 'CEL' }]);
  const b = oec.normalizeFields([{ name: 'T', type: 'Double', unit: 'CEL' }]);
  assert.deepEqual(a, b);
  assert.deepEqual(oec.normalizeFields({ On: 'Boolean' }), [{ name: 'On', type: 'Boolean' }]);
  assert.throws(() => oec.normalizeFields([{ name: '1x' }]), oec.OecError);
  assert.throws(() => oec.normalizeFields([{ name: 'x', type: 'Nope' }]), oec.OecError);
  assert.deepEqual(oec.deriveFields({ a: 1, b: true, c: 's' }).map((f) => f.type), ['Double', 'Boolean', 'String']);
  assert.throws(() => oec.fieldValue({ name: 'x', type: 'Boolean' }, 1), oec.OecError);
});

test('DataSetClassId and version depend on the shape only', () => {
  const f = oec.normalizeFields([{ name: 'T', type: 'Double' }]);
  const id = oec.dataClassId('Temp', f);
  assert.match(id, /^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.equal(oec.dataClassId('Temp', f), id);
  assert.notEqual(oec.dataClassId('Temp', oec.normalizeFields([{ name: 'T', type: 'Int32' }])), id);
  assert.ok(Number.isInteger(oec.dataVersion(id)));
});

test('Config definition, validation and masking', () => {
  const c = oec.normalizeConfig({
    Settings: {
      Interval: { Value: '60', Type: 'Number', Validation: { Min: 1, Max: 3600 } },
      Token: { Value: 'secret', Type: 'String', Sensitive: true },
    },
  });
  assert.equal(c.Settings.Name.Text, 'Settings');
  assert.equal(oec.checkConfigValue(c.Settings.Interval, '7200') !== null, true);
  assert.equal(oec.checkConfigValue(c.Settings.Interval, '30'), null);
  assert.equal(oec.maskedConfig(c).Settings.Token.Value, oec.SENSITIVE_MASK);
  assert.equal(c.Settings.Token.Value, 'secret');
  assert.throws(() => oec.normalizeConfig({ S: { X: { Type: 'Number', Value: 'abc' } } }), oec.OecError);
  assert.throws(() => oec.normalizeConfig({ S: { X: { Type: 'Number', Validation: { Pattern: '(' } } } }), oec.OecError);
});
