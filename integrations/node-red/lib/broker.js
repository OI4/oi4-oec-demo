'use strict';
// MQTT connection options of an OEC application: either from the container
// storage (RIG 4.1.3, REQ-CNT-003 … 009: broker.json, CA, credentials or
// client certificate) or set by hand.

const fs = require('node:fs');
const os = require('node:os');
const net = require('node:net');
const path = require('node:path');
const crypto = require('node:crypto');

const DEFAULT_MAX_PACKET_SIZE = 262144;

class BrokerError extends Error {}

function read(file) {
  try {
    return fs.readFileSync(file);
  } catch (e) {
    if (e.code === 'ENOENT') return null;
    throw e;
  }
}

function isFile(file) {
  try {
    return fs.statSync(file).isFile();
  } catch {
    return false;
  }
}

/** The container name: hostname of the container storage, else of the system. */
function hostname(root) {
  for (const f of ['proc/sys/kernel/hostname', 'etc/hostname']) {
    const raw = read(path.join(root, f));
    const name = raw && raw.toString('utf8').trim();
    if (name) return name;
  }
  return os.hostname();
}

/**
 * The broker of the container storage below `root` ("/" in a container):
 * {host, port, ca, maxPacketSize, clientId, auth, username?, password?,
 * cert?, key?, passphrase?}. Throws BrokerError.
 */
function fromContainer(root = '/') {
  const name = hostname(root);
  const brokerFile = path.join(root, 'etc/oi4/mqtt/broker.json');
  const raw = read(brokerFile);
  if (!raw) throw new BrokerError(`${brokerFile} not found (REQ-CNT-004)`);
  let b;
  try {
    b = JSON.parse(raw.toString('utf8'));
  } catch (e) {
    throw new BrokerError(`${brokerFile}: ${e.message}`);
  }
  const port = b && b.SecurePort;
  if (!b || typeof b.Address !== 'string' || !b.Address || !Number.isInteger(port) || port < 1 || port > 65535
    || !Number.isInteger(b.MaxPacketSize) || b.MaxPacketSize < 1) {
    throw new BrokerError(`${brokerFile}: Address, SecurePort 1 … 65535 and MaxPacketSize are required (REQ-CNT-003)`);
  }
  let ca = null;
  for (const f of ['etc/oi4/mqtt/broker_ca.pem', 'etc/oi4/certs/ca.pem']) {
    ca = read(path.join(root, f));
    if (ca) break;
  }
  if (!ca) throw new BrokerError('no CA certificate: etc/oi4/mqtt/broker_ca.pem or etc/oi4/certs/ca.pem (REQ-CNT-004, REQ-CNT-005)');
  const out = { host: b.Address, port, ca, maxPacketSize: b.MaxPacketSize, clientId: name };
  const secrets = path.join(root, 'run/secrets/oi4');
  const keyFile = path.join(secrets, 'private_key.pem');
  const certFile = path.join(root, 'etc/oi4/certs', `${name}.pem`);
  if (isFile(keyFile) && isFile(certFile)) {
    // A client certificate wins over credentials (REQ-MQTT-010, SF-117).
    out.auth = 'client certificate';
    out.cert = fs.readFileSync(certFile);
    out.key = fs.readFileSync(keyFile);
    const passphrase = read(path.join(secrets, 'passphrase'));
    if (passphrase) out.passphrase = passphrase.toString('utf8').replace(/\r?\n$/, '');
    return out;
  }
  const credentials = read(path.join(secrets, 'credentials'));
  if (!credentials) throw new BrokerError('no means of authentication: run/secrets/oi4/credentials or a client certificate (REQ-MQTT-009, REQ-CNT-008)');
  const text = credentials.toString('utf8').trim();
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(text)) throw new BrokerError('credentials are not base64 (REQ-CNT-009)');
  const decoded = Buffer.from(text, 'base64').toString('utf8');
  const i = decoded.indexOf(':');
  if (i <= 0) throw new BrokerError('credentials: expected base64(<username>:<password>) (REQ-CNT-009)');
  out.auth = 'credentials';
  out.username = decoded.slice(0, i);
  out.password = decoded.slice(i + 1);
  return out;
}

/** A stable ClientId of an application without container (MG 6.2.3). */
function clientIdOf(il) {
  return `oecnr${crypto.createHash('sha256').update(il).digest('hex').slice(0, 16)}`;
}

/**
 * Options for mqtt.connect() (MQTT 3.1.1, clean session, keepalive 60 s,
 * TLS 1.2 or higher with verification, REQ-MQTT-005/008) from a broker
 * description of fromContainer() or of the settings of the broker node.
 */
function connectOptions(b, il) {
  const tls = b.tls !== false;
  const o = {
    protocol: tls ? 'mqtts' : 'mqtt',
    host: b.host,
    port: b.port || (tls ? 8883 : 1883),
    protocolVersion: 4,
    clean: true,
    keepalive: 60,
    reconnectPeriod: 5000,
    connectTimeout: 30000,
    clientId: b.clientId || clientIdOf(il),
    resubscribe: false,
  };
  if (b.username) o.username = b.username;
  if (b.password != null && b.username) o.password = b.password;
  if (tls) {
    o.minVersion = 'TLSv1.2';
    o.rejectUnauthorized = b.rejectUnauthorized !== false;
    if (b.ca) o.ca = b.ca;
    if (b.cert) o.cert = b.cert;
    if (b.key) o.key = b.key;
    if (b.passphrase) o.passphrase = b.passphrase;
    if (!net.isIP(b.host)) o.servername = b.host;
  }
  return o;
}

module.exports = { BrokerError, DEFAULT_MAX_PACKET_SIZE, fromContainer, hostname, clientIdOf, connectOptions };
