'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const oec = require('../lib/oec');
const { Provider, groupOf } = require('../lib/provider');

const MAM = { manufacturer: 'Example', manufacturerUri: 'example.com', model: 'Flow', productCode: 'NR', serialNumber: '1' };
const ASSET = { manufacturer: 'Example', manufacturerUri: 'example.com', model: 'Sensor', productCode: 'S', serialNumber: '42' };

function make(opts = {}) {
  let now = Date.parse('2026-11-28T12:00:00Z');
  const p = new Provider({ serviceType: 'Utility', mam: MAM, clock: () => (now += 1), ...opts });
  return p;
}

function decode(out) {
  return out.map(({ topic, payload }) => ({ topic, t: oec.parseTopic(topic), m: JSON.parse(payload) }));
}

function names(out) {
  return decode(out).map(({ t }) => [t.method, t.resource, ...(t.filter || [])].join(' '));
}

function request(p, method, resource, { source, filter, messages = [], replyTo = '' } = {}) {
  const topic = oec.topic(p.serviceType, p.il, method, resource, source, filter);
  const payload = oec.networkMessage('99-Utility/requester', 'Utility/requester', messages, replyTo);
  return p.handle(topic, payload);
}

test('Identification Link, AppId, DeviceClass of the application', () => {
  const p = make();
  assert.equal(p.il, 'example.com/Flow/NR/1');
  assert.equal(p.app.mam.DeviceClass, 'Oi4.Utility');
  assert.equal(p.app.mam.ProductInstanceUri, p.il);
  assert.throws(() => make({ serviceType: 'Nope' }), oec.OecError);
  assert.throws(() => new Provider({ serviceType: 'Utility', mam: { ...MAM, manufacturerUri: 'https://x' } }), /ManufacturerUri/);
});

test('connect sequence: Birth first, lists, then Config, Metadata, Data', () => {
  const p = make();
  p.defineConfig(p.il, 'Settings', { S: { Interval: { Type: 'Number', Value: '60' } } });
  p.defineData(p.il, 'Temp', { fields: [{ name: 'T', type: 'Double' }] });
  p.publishData(p.il, 'Temp', { T: 21.5 });
  const out = names(p.connected());
  assert.deepEqual(out, [
    'Pub MAM', 'Pub Health', 'Pub Profile', 'Pub PublicationList', 'Pub SubscriptionList',
    'Pub Config Settings', 'Pub Metadata Temp', 'Pub Data Temp',
  ]);
  const [mam, health] = decode(p.connected());
  assert.equal(mam.m.Messages[0].DataSetWriterId, oec.DSW.MAM);
  assert.equal(health.m.Messages[0].Payload.Health, 'NORMAL_0');
});

test('Will and close', () => {
  const p = make();
  const will = decode([p.will()])[0];
  assert.equal(will.t.resource, 'Health');
  assert.deepEqual(will.m.Messages[0].Payload, { Health: 'FAILURE_1', HealthScore: 0 });
  assert.notEqual(p.will().payload, p.will().payload, 'a new MessageId per will');
  p.connected();
  const close = decode([p.close()])[0];
  assert.deepEqual(close.m.Messages[0].Payload, { Health: 'NORMAL_0', HealthScore: 0 });
  assert.throws(() => p.setHealth(p.il, 'NORMAL_0', 0), oec.OecError);
});

test('PublicationList lists Data from its first value on', () => {
  const p = make();
  p.connected();
  p.defineData(p.il, 'Temp');
  let list = decode(request(p, 'Get', 'PublicationList').out)[0];
  assert.ok(!list.m.Messages.some((m) => m.Payload.Resource === 'Data'));
  const out = names(p.publishData(p.il, 'Temp', { T: 1 }));
  assert.deepEqual(out, ['Pub PublicationList', 'Pub Metadata Temp', 'Pub Data Temp']);
  list = decode(request(p, 'Get', 'PublicationList').out)[0];
  const groups = list.m.Messages.map((m) => m.WriterGroupName);
  assert.ok(groups.includes('Data/Temp'));
  assert.ok(groups.includes('Metadata/Temp'));
  assert.equal(list.m.ReplyTo, '99-Utility/requester');
});

test('Get Data / Metadata / Config', () => {
  const p = make();
  p.connected();
  p.defineData(p.il, 'Temp', { fields: { T: 'Double' } });
  assert.throws(() => p.publishData(p.il, 'Temp', 21), oec.OecError);
  p.publishData(p.il, 'Temp', { T: 21 });
  const data = decode(request(p, 'Get', 'Data', { source: p.il, filter: 'Temp' }).out);
  assert.equal(data.length, 1);
  assert.equal(data[0].t.method, 'Pub');
  assert.deepEqual(data[0].m.Messages[0].Payload, { T: 21 });
  const meta = decode(request(p, 'Get', 'Metadata', { source: p.il, filter: 'Temp' }).out);
  assert.equal(meta[0].m.MessageType, 'ua-metadata');
  assert.equal(meta[0].m.MetaData.Fields[0].Name, 'T');
  assert.equal(request(p, 'Get', 'Metadata', { source: p.il, filter: 'Nope' }).out.length, 0);
});

test('Set Config: publication, then Status Good; invalid value → Bad', () => {
  const p = make();
  p.connected();
  p.defineConfig(p.il, 'Settings', { S: { Interval: { Type: 'Number', Value: '60', Validation: { Min: 1 } } } });
  const current = decode(request(p, 'Get', 'Config', { source: p.il, filter: 'Settings' }).out)[0].m.Messages[0];
  const set = (value) => {
    const payload = structuredClone(current.Payload);
    payload.S.Interval.Value = value;
    return request(p, 'Set', 'Config', { source: p.il, filter: 'Settings', messages: [{ ...current, Payload: payload }] });
  };
  const ok = set('30');
  assert.deepEqual(names(ok.out), ['Pub Config Settings', 'Pub Event Status Good']);
  assert.deepEqual(ok.events.map((e) => [e.type, e.name, e.values.S.Interval]), [['config', 'Settings', 30]]);
  assert.equal(p.configValues(p.il, 'Settings').S.Interval, '30');
  const bad = set('0');
  assert.deepEqual(names(bad.out), ['Pub Event Status Bad']);
  assert.equal(p.configValues(p.il, 'Settings').S.Interval, '30');
});

test('Set Data only for writable Data', () => {
  const p = make();
  p.connected();
  p.defineData(p.il, 'Ro', { fields: { V: 'Double' } });
  p.defineData(p.il, 'Rw', { fields: { V: 'Double' }, writable: true });
  p.publishData(p.il, 'Ro', { V: 1 });
  p.publishData(p.il, 'Rw', { V: 1 });
  const dsm = (tag) => decode(request(p, 'Get', 'Data', { source: p.il, filter: tag }).out)[0].m.Messages[0];
  const set = (tag, v) => request(p, 'Set', 'Data', { source: p.il, filter: tag, messages: [{ ...dsm(tag), Payload: { V: v } }] });
  const rw = set('Rw', 5);
  assert.deepEqual(names(rw.out), ['Pub Data Rw', 'Pub Event Status Good']);
  assert.deepEqual(rw.events[0].values, { V: 5 });
  const ro = set('Ro', 5);
  assert.deepEqual(names(ro.out), ['Pub Event Status Bad']);
  assert.equal(ro.events.length, 0);
});

test('assets: Birth on add, own PublicationList, unknown Source → BadNotFound', () => {
  const p = make();
  p.connected();
  const { il, out } = p.addAsset(ASSET);
  assert.equal(il, 'example.com/Sensor/S/42');
  assert.deepEqual(names(out).slice(0, 3), ['Pub MAM', 'Pub Health', 'Pub Profile']);
  const mam = decode(request(p, 'Get', 'MAM').out);
  assert.deepEqual(mam[0].m.Messages.map((m) => m.DataSetWriterName), [p.il, il], 'Get MAM without Source answers for every Source');
  const health = decode(p.setHealth(il, 'MAINTENANCE_REQUIRED_4', 70));
  assert.equal(health[0].t.sourceIl, il);
  const unknown = request(p, 'Set', 'Config', { source: 'example.com/X/Y/Z', filter: 'S', messages: [] });
  const status = decode(unknown.out)[0];
  assert.equal(status.t.sourceIl, p.il);
  assert.equal(status.m.Messages[0].Payload.Details.SymbolicId, 'BadNotFound');
  p.removeAsset(il);
  const lists = names(p.flushLists());
  assert.ok(lists.includes('Pub PublicationList'));
});

test('events: Syslog levels of the application always listed, others on first use', () => {
  const p = make();
  p.connected();
  const out = names(p.publishEvent(p.il, { category: 'Syslog', number: 11, details: { MSG: 'boom' } }));
  assert.deepEqual(out, ['Pub Event Syslog Error']);
  const g = decode(p.publishEvent(p.il, { category: 'Generic', level: 'Door', number: 3, description: 'open', details: { a: 1 } }));
  assert.deepEqual(g.map(({ t }) => t.resource), ['PublicationList', 'Event']);
  assert.equal(g.at(-1).m.Messages[0].Payload.Category, 'CAT_GENERIC_99');
  assert.throws(() => p.publishEvent(p.il, { category: 'Syslog', number: 192 }), oec.OecError);
  assert.throws(() => p.publishEvent(p.il, { category: 'Generic', level: 'a/b' }), oec.OecError);
});

test('Syslog levels: default threshold, switched by Set PublicationList (REQ-TOP-024/027)', () => {
  assert.throws(() => make({ syslogLevel: 'Debug' }), /Debug/);
  const p = make();
  p.connected();
  const entries = decode(request(p, 'Get', 'PublicationList', { source: p.il, filter: ['Event'] }).out)[0].m.Messages
    .filter((m) => m.Payload.Filter.startsWith('Syslog/'));
  assert.deepEqual(entries.map((m) => [m.Payload.Filter, m.Payload.Mode, m.Payload.Config]), oec.SYSLOG_LEVELS.map((l, i) => [
    `Syslog/${l}`, i <= 5 ? 'FILTER_4' : 'OFF_0', 'MODE_1']));
  assert.equal(new Set(entries.map((m) => m.Payload.DataSetWriterId)).size, 8);
  const info = { category: 'Syslog', number: 14 };
  assert.deepEqual(p.publishEvent(p.il, info), []);
  const on = entries.find((m) => m.Payload.Filter === 'Syslog/Informational');
  const set = request(p, 'Set', 'PublicationList', {
    source: p.il, filter: ['Event', 'Syslog', 'Informational'],
    messages: [{ ...on, Payload: { ...on.Payload, Mode: 'FILTER_4' } }],
  });
  const r = decode(set.out);
  assert.deepEqual(names(set.out), ['Pub PublicationList Event Syslog Informational', 'Pub PublicationList', 'Pub Event Status Good']);
  assert.equal(r[0].m.ReplyTo, '99-Utility/requester');
  assert.equal(r[0].m.Messages[0].Payload.Mode, 'FILTER_4');
  assert.deepEqual(names(p.publishEvent(p.il, info)), ['Pub Event Syslog Informational']);
  const bad = request(p, 'Set', 'PublicationList', {
    source: p.il, filter: ['Event', 'Syslog', 'Informational'],
    messages: [{ ...on, Payload: { ...on.Payload, Mode: 'ON_REQUEST_1' } }],
  });
  assert.equal(decode(bad.out)[0].m.Messages[0].Payload.Details.SymbolicId, 'BadNotWritable');
  const mam = decode(request(p, 'Get', 'PublicationList', { source: p.il, filter: ['MAM'] }).out)[0].m.Messages[0];
  const fixed = request(p, 'Set', 'PublicationList', {
    source: p.il, filter: ['MAM'], messages: [{ ...mam, Payload: { ...mam.Payload, Mode: 'OFF_0' } }],
  });
  assert.equal(decode(fixed.out)[0].m.Messages[0].Payload.Details.SymbolicId, 'BadNotWritable');
});

test('SubscriptionList: consumers and Set topics; not writable', () => {
  const p = make();
  p.connected();
  p.defineConfig(p.il, 'Settings', { S: { X: { Value: 'a' } } });
  const filter = 'Oi4v2/+/+/Pub/Data/#';
  assert.deepEqual(names(p.addConsumer(filter)), ['Pub SubscriptionList']);
  const list = decode(request(p, 'Get', 'SubscriptionList').out)[0].m.Messages;
  assert.deepEqual(list.map((m) => m.Payload.TopicPath), [filter, oec.topic('Utility', p.il, 'Set', 'Config', p.il, 'Settings')]);
  assert.equal(groupOf(filter), 'Data');
  const del = request(p, 'Del', 'SubscriptionList', {
    source: p.il, filter: 'Data', messages: [{ DataSetWriterId: oec.DSW.SubscriptionList, Payload: { TopicPath: filter } }],
  });
  assert.equal(decode(del.out)[0].m.Messages[0].Payload.Details.SymbolicId, 'BadNotWritable');
  p.removeConsumer(filter);
  assert.deepEqual(names(p.flushLists()), ['Pub SubscriptionList']);
  assert.deepEqual(p.flushLists(), []);
});

test('Get request of a consumer', () => {
  const p = make();
  const r = p.getRequest({ serviceType: 'Registry', appIl: 'a.com/R/R/1', resource: 'Data', sourceIl: 'a.com/S/S/1', filter: 'T', perPage: 2, page: 1 });
  const t = oec.parseTopic(r.topic);
  assert.deepEqual([t.method, t.resource, t.sourceIl, t.filter[0]], ['Get', 'Data', 'a.com/S/S/1', 'T']);
  const m = JSON.parse(r.payload);
  assert.equal(m.MessageId, r.messageId);
  assert.deepEqual(m.Messages[0].Payload, { PerPage: 2, Page: 1 });
  assert.throws(() => p.getRequest({ serviceType: 'Registry', appIl: 'a.com/R/R/1', resource: 'Metadata' }), oec.OecError);
  assert.equal(JSON.parse(p.getRequest({ serviceType: 'Registry', appIl: 'a', resource: 'Metadata', sourceIl: 'b', filter: 'T' }).payload).MessageType, 'ua-metadata');
});

test('pagination of a large answer', () => {
  const p = make({ maxPacketSize: 4000 });
  p.connected();
  for (let i = 0; i < 30; i++) p.addAsset({ ...ASSET, serialNumber: String(i) });
  const pages = decode(request(p, 'Get', 'MAM').out);
  assert.ok(pages.length > 1);
  const first = pages[0].m.MessageId;
  for (const [i, page] of pages.entries()) {
    assert.ok(Buffer.byteLength(JSON.stringify(page.m)) + page.topic.length + 4 <= 4000);
    const pg = page.m.Messages.at(-1);
    assert.equal(pg.DataSetWriterId, oec.DSW_PAGINATION);
    assert.equal(pg.Payload.PaginationId, first);
    assert.equal(pg.Payload.HasNext, i < pages.length - 1);
  }
  const total = pages.reduce((n, pg) => n + pg.m.Messages.length - 1, 0);
  assert.equal(total, 31);
});
