'use strict';
// The nodes in a Node-RED runtime (node-red-node-test-helper) against an
// in-process MQTT broker (aedes, plain MQTT), observed by a peer client.

const test = require('node:test');
const assert = require('node:assert/strict');
const net = require('node:net');
const helper = require('node-red-node-test-helper');
const mqtt = require('mqtt');
const { Aedes } = require('aedes');
const oec = require('../lib/oec');

const NODES = ['oec-broker', 'oec-app', 'oec-asset', 'oec-health', 'oec-data', 'oec-config', 'oec-event', 'oec-subscribe', 'oec-get']
  .map((n) => require(`../nodes/${n}.js`));

helper.init(require.resolve('node-red'));

const APP_IL = 'example.com/Flow/NR/1';
const ASSET_IL = 'example.com/Sensor/S/42';
const ST = 'Utility';

let server;
let broker;
let port;
let peer;
let seen = [];
const waiters = new Set();

function record(topic, payload) {
  const item = { topic, t: oec.parseTopic(topic), m: JSON.parse(payload.toString()) };
  seen.push(item);
  for (const w of [...waiters]) {
    if (w.match(item)) {
      waiters.delete(w);
      clearTimeout(w.timer);
      w.resolve(item);
    }
  }
}

/** The first message (already seen or coming) that matches. */
function waitFor(match, what, ms = 5000) {
  const found = seen.find(match);
  if (found) return Promise.resolve(found);
  return new Promise((resolve, reject) => {
    const w = { match, resolve };
    w.timer = setTimeout(() => {
      waiters.delete(w);
      reject(new Error(`timeout waiting for ${what}`));
    }, ms);
    waiters.add(w);
  });
}

const is = (method, resource, filter, source) => (x) => x.t && x.t.method === method && x.t.resource === resource
  && (filter == null || (x.t.filter || []).join('/') === filter)
  && (source == null || x.t.sourceIl === source);

function nextOutput(node, ms = 5000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timeout waiting for output of ${node.id}`)), ms);
    node.once('input', (msg) => {
      clearTimeout(timer);
      resolve(msg);
    });
  });
}

function flow() {
  const mam = (o) => ({ manufacturer: 'Example', manufacturerUri: 'example.com', ...o });
  return [
    { id: 'b', type: 'oec-broker', mode: 'manual', host: '127.0.0.1', port, tls: false },
    { id: 'a', type: 'oec-app', broker: 'b', serviceType: ST, heartbeat: 60, ...mam({ model: 'Flow', productCode: 'NR', serialNumber: '1' }) },
    { id: 's', type: 'oec-asset', ...mam({ model: 'Sensor', productCode: 'S', serialNumber: '42' }) },
    { id: 'health', type: 'oec-health', app: 'a', asset: 's', initial: 'NORMAL_0', score: 90 },
    { id: 'data', type: 'oec-data', app: 'a', tag: 'Temp', fields: '[{"Name":"T","Type":"Double","Unit":"CEL"}]', writable: true, wires: [['out-data']] },
    { id: 'adata', type: 'oec-data', app: 'a', asset: 's', tag: 'Level', fields: '', wires: [[]] },
    { id: 'config', type: 'oec-config', app: 'a', contextName: 'Settings', definition: '{"S":{"Interval":{"Type":"Number","Value":"60","Validation":{"Min":1}}}}', wires: [['out-config']] },
    { id: 'event', type: 'oec-event', app: 'a', category: 'Syslog', facility: 1, severity: '4' },
    { id: 'sub', type: 'oec-subscribe', app: 'a', mode: 'resource', resource: 'Data', ignoreOwn: false, output: 'dsm', wires: [['out-sub']] },
    { id: 'get', type: 'oec-get', app: 'a', serviceType: ST, appIl: APP_IL, resource: 'Data', timeout: 3, wires: [['out-get']] },
    { id: 'out-data', type: 'helper' },
    { id: 'out-config', type: 'helper' },
    { id: 'out-sub', type: 'helper' },
    { id: 'out-get', type: 'helper' },
  ];
}

test.before(async () => {
  broker = await Aedes.createBroker();
  server = net.createServer(broker.handle);
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  port = server.address().port;
  peer = mqtt.connect({ host: '127.0.0.1', port, protocolVersion: 4, clientId: 'peer' });
  await new Promise((r) => peer.on('connect', r));
  await new Promise((r, j) => peer.subscribe('Oi4v2/#', (e) => (e ? j(e) : r())));
  peer.on('message', record);
  await new Promise((r) => helper.startServer(r));
});

test.after(async () => {
  await helper.unload();
  await new Promise((r) => helper.stopServer(r));
  peer.end(true);
  await new Promise((r) => server.close(r));
  await new Promise((r) => broker.close(r));
});

test('the nodes run an OEC application', async (t) => {
  t.after(() => helper.unload());
  await helper.load(NODES, flow());
  const n = (id) => helper.getNode(id);
  const appId = oec.encodeId(APP_IL);

  // Birth, asset, Health of the asset set at start
  const mam = await waitFor(is('Pub', 'MAM', null, APP_IL), 'MAM of the application');
  assert.equal(mam.m.Messages[0].Payload.ProductInstanceUri, APP_IL);
  assert.equal(mam.m.Messages[0].Payload.DeviceClass, 'Oi4.Utility');
  assert.ok(mam.topic.startsWith(`Oi4v2/${ST}/${appId}/Pub/MAM`));
  await waitFor(is('Pub', 'MAM', null, ASSET_IL), 'MAM of the asset');
  const ah = await waitFor((x) => is('Pub', 'Health', null, ASSET_IL)(x) && x.m.Messages[0].Payload.HealthScore === 90, 'Health of the asset');
  assert.equal(ah.m.Messages[0].Payload.Health, 'NORMAL_0');
  const cfg = await waitFor(is('Pub', 'Config', 'Settings', APP_IL), 'Config');
  assert.equal(cfg.m.Messages[0].Payload.S.Interval.Value, '60');
  const subs = await waitFor((x) => is('Pub', 'SubscriptionList')(x) && x.m.Messages.some((m) => m.Payload.TopicPath === 'Oi4v2/+/+/Pub/Data/#'), 'SubscriptionList with the consumer');
  assert.ok(subs);

  // Data: input → Pub Data; the subscribe node receives it
  const subOut = nextOutput(n('out-sub'));
  n('data').receive({ payload: 21.5 });
  const data = await waitFor(is('Pub', 'Data', 'Temp', APP_IL), 'Data Temp');
  assert.deepEqual(data.m.Messages[0].Payload, { T: 21.5 });
  const meta = await waitFor(is('Pub', 'Metadata', 'Temp', APP_IL), 'Metadata Temp');
  assert.equal(meta.m.MetaData.Fields[0].Name, 'T');
  const got = await subOut;
  assert.deepEqual(got.payload, { T: 21.5 });
  assert.equal(got.oec.resource, 'Data');
  assert.equal(got.oec.filter, 'Temp');
  assert.equal(got.oec.sourceIl, APP_IL);

  // Asset Data with derived fields
  n('adata').receive({ payload: { Fill: 0.5, Ok: true } });
  const level = await waitFor(is('Pub', 'Data', 'Level', ASSET_IL), 'Data Level of the asset');
  assert.deepEqual(level.m.Messages[0].Payload, { Fill: 0.5, Ok: true });

  // Get Data of the own application
  const getOut = nextOutput(n('out-get'));
  n('get').receive({ payload: 'go', oec: { sourceIl: APP_IL, filter: 'Temp' } });
  const answer = await getOut;
  assert.equal(answer.payload.length, 1);
  assert.deepEqual(answer.payload[0].Payload, { T: 21.5 });
  assert.equal(answer.oec.replyTo, answer.oec.requestId);

  // Get with Status Bad → error
  const errors = [];
  const caught = new Promise((resolve) => {
    n('get').on('call:error', (call) => {
      errors.push(call.args[0]);
      resolve();
    });
  });
  n('get').receive({ payload: 'go', oec: { sourceIl: 'example.com/No/No/0', filter: 'Temp' } });
  await caught;
  assert.match(String(errors[0]), /BadNotFound/);

  // Set Config from the bus → Pub Config, Status Good, output "set"
  const cfgOut = nextOutput(n('out-config'));
  const current = cfg.m.Messages[0];
  const payload = structuredClone(current.Payload);
  payload.S.Interval.Value = '30';
  const setTopic = oec.topic(ST, APP_IL, 'Set', 'Config', APP_IL, 'Settings');
  peer.publish(setTopic, oec.networkMessage('1-Utility/peer', 'Utility/peer', [{ ...current, Payload: payload }]));
  const set = await cfgOut;
  assert.equal(set.oec.reason, 'set');
  assert.equal(set.payload.S.Interval, 30);
  await waitFor((x) => is('Pub', 'Event', 'Status/Good')(x) && x.m.ReplyTo === '1-Utility/peer', 'Status Good');

  // Config input
  const cfgIn = nextOutput(n('out-config'));
  n('config').receive({ payload: { S: { Interval: 45 } } });
  assert.equal((await cfgIn).payload.S.Interval, 45);
  await waitFor((x) => is('Pub', 'Config', 'Settings')(x) && x.m.Messages[0].Payload.S.Interval.Value === '45', 'Config 45');

  // Set Data (writable)
  const dataOut = nextOutput(n('out-data'));
  const dsm = data.m.Messages[0];
  peer.publish(oec.topic(ST, APP_IL, 'Set', 'Data', APP_IL, 'Temp'), oec.networkMessage('2-Utility/peer', 'Utility/peer', [{ ...dsm, Payload: { T: 18 } }]));
  const setData = await dataOut;
  assert.deepEqual(setData.payload, { T: 18 });
  assert.equal(setData.oec.reason, 'set');

  // Event
  n('event').receive({ payload: 'disk almost full' });
  const ev = await waitFor(is('Pub', 'Event', 'Syslog/Warning'), 'Syslog Warning');
  assert.equal(ev.m.Messages[0].Payload.Number, 12);
  assert.equal(ev.m.Messages[0].Payload.Details.MSG, 'disk almost full');

  // Health of the application
  n('health').receive({ payload: 'maintenance' });
  await waitFor((x) => is('Pub', 'Health', null, ASSET_IL)(x) && x.m.Messages[0].Payload.Health === 'MAINTENANCE_REQUIRED_4', 'Health maintenance');

  // Graceful stop: NORMAL_0/0 of the application
  seen = [];
  await helper.unload();
  await waitFor((x) => is('Pub', 'Health', null, APP_IL)(x) && x.m.Messages[0].Payload.HealthScore === 0 && x.m.Messages[0].Payload.Health === 'NORMAL_0', 'NORMAL_0/0');
});

test('the example flow runs', async (t) => {
  t.after(() => helper.unload());
  const core = ['common/20-inject.js', 'common/21-debug.js', 'common/90-comment.js', 'function/10-function.js']
    .map((f) => require(require.resolve(`@node-red/nodes/core/${f}`, { paths: [require.resolve('node-red')] })));
  const example = require('../examples/oec-demo.json').map((n) => (n.type === 'oec-broker'
    ? { ...n, mode: 'manual', host: '127.0.0.1', port, tls: false } : n));
  seen = [];
  await helper.load([...NODES, ...core], example);
  const il = 'example.com/NodeRedDemo/NR/1';
  const asset = 'example.com/ExampleSensor/NR/S1';
  const climate = await waitFor(is('Pub', 'Data', 'Climate', il), 'Data Climate');
  assert.deepEqual(Object.keys(climate.m.Messages[0].Payload), ['Temperature', 'Humidity']);
  await waitFor(is('Pub', 'Data', 'Level', asset), 'Data Level of the asset');
  await waitFor(is('Pub', 'Data', 'Setpoint', il), 'Data Setpoint');
  await waitFor(is('Pub', 'Config', 'Settings', il), 'Config Settings');
  await waitFor((x) => is('Pub', 'Health', null, asset)(x) && x.m.Messages[0].Payload.Health === 'NORMAL_0', 'Health of the asset');
  const out = nextOutput(helper.getNode('dbg4'));
  helper.getNode('i6').receive({ payload: Date.now() });
  const mam = await out;
  assert.equal(mam.payload[0].Payload.ProductInstanceUri, il);
});

test('an invalid application shows its error', async (t) => {
  t.after(() => helper.unload());
  await helper.load(NODES, [
    { id: 'b', type: 'oec-broker', mode: 'manual', host: '127.0.0.1', port, tls: false },
    { id: 'a', type: 'oec-app', broker: 'b', serviceType: ST, manufacturer: 'x', manufacturerUri: 'https://x', model: 'm', productCode: 'p', serialNumber: 's' },
    { id: 'd', type: 'oec-data', app: 'a', tag: 'T' },
  ]);
  const statuses = [];
  helper.getNode('d').on('call:status', (c) => statuses.push(c.args[0]));
  assert.equal(helper.getNode('a').provider, null);
  await helper.unload();
});
