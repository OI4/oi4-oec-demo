'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const broker = require('../lib/broker');

function storage(files) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'oecnr-'));
  for (const [f, content] of Object.entries(files)) {
    fs.mkdirSync(path.dirname(path.join(root, f)), { recursive: true });
    fs.writeFileSync(path.join(root, f), content);
  }
  return root;
}

const BROKER = JSON.stringify({ Address: 'broker.local', SecurePort: 8883, MaxPacketSize: 1048576 });

test('container storage with credentials (password with ":")', () => {
  const root = storage({
    'etc/hostname': 'nr-1\n',
    'etc/oi4/mqtt/broker.json': BROKER,
    'etc/oi4/certs/ca.pem': 'CA',
    'run/secrets/oi4/credentials': Buffer.from('user:pa:ss').toString('base64'),
  });
  const b = broker.fromContainer(root);
  assert.equal(b.host, 'broker.local');
  assert.equal(b.port, 8883);
  assert.equal(b.maxPacketSize, 1048576);
  assert.equal(b.clientId, 'nr-1');
  assert.equal(b.auth, 'credentials');
  assert.equal(b.username, 'user');
  assert.equal(b.password, 'pa:ss');
  const o = broker.connectOptions(b, 'x/y/z/w');
  assert.equal(o.protocol, 'mqtts');
  assert.equal(o.protocolVersion, 4);
  assert.equal(o.clean, true);
  assert.equal(o.minVersion, 'TLSv1.2');
  assert.equal(o.rejectUnauthorized, true);
  assert.equal(o.servername, 'broker.local');
  assert.equal(o.clientId, 'nr-1');
});

test('a client certificate wins over credentials; broker_ca.pem first', () => {
  const root = storage({
    'etc/hostname': 'nr-2',
    'etc/oi4/mqtt/broker.json': BROKER,
    'etc/oi4/mqtt/broker_ca.pem': 'CA1',
    'etc/oi4/certs/ca.pem': 'CA2',
    'etc/oi4/certs/nr-2.pem': 'CERT',
    'run/secrets/oi4/private_key.pem': 'KEY',
    'run/secrets/oi4/passphrase': 'pw\n',
    'run/secrets/oi4/credentials': Buffer.from('u:p').toString('base64'),
  });
  const b = broker.fromContainer(root);
  assert.equal(b.auth, 'client certificate');
  assert.equal(b.ca.toString(), 'CA1');
  assert.equal(b.cert.toString(), 'CERT');
  assert.equal(b.passphrase, 'pw');
  assert.equal(b.username, undefined);
});

test('container storage errors', () => {
  assert.throws(() => broker.fromContainer(storage({})), /broker.json/);
  assert.throws(() => broker.fromContainer(storage({ 'etc/oi4/mqtt/broker.json': '{"Address":"x","SecurePort":0,"MaxPacketSize":1}' })), /SecurePort/);
  assert.throws(() => broker.fromContainer(storage({ 'etc/oi4/mqtt/broker.json': BROKER })), /CA/);
  assert.throws(() => broker.fromContainer(storage({ 'etc/oi4/mqtt/broker.json': BROKER, 'etc/oi4/certs/ca.pem': 'CA' })), /authentication/);
  assert.throws(() => broker.fromContainer(storage({
    'etc/oi4/mqtt/broker.json': BROKER, 'etc/oi4/certs/ca.pem': 'CA', 'run/secrets/oi4/credentials': Buffer.from('nouser').toString('base64'),
  })), /username/);
});

test('manual options: IP address without SNI, plain MQTT, derived ClientId', () => {
  const o = broker.connectOptions({ host: '127.0.0.1', port: 1, username: 'u', password: 'p' }, 'a/b/c/d');
  assert.equal(o.servername, undefined);
  assert.match(o.clientId, /^oecnr[0-9a-f]{16}$/);
  assert.equal(o.clientId, broker.clientIdOf('a/b/c/d'));
  const plain = broker.connectOptions({ host: 'h', tls: false }, 'a/b/c/d');
  assert.equal(plain.protocol, 'mqtt');
  assert.equal(plain.port, 1883);
  assert.equal(plain.minVersion, undefined);
});
