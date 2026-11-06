'use strict';
// An embedded Node-RED runtime with an OEC flow for the D7 live run
// (test/d7.test.js): the package is linked into the user directory, the
// broker comes from the container storage OEC_ROOT. SIGTERM stops the
// runtime gracefully (NORMAL_0/0, then disconnect).
//
//   OEC_ROOT=<storage> node test/d7/runtime.js <userDir>

const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');

const pkg = path.resolve(__dirname, '..', '..');
const userDir = path.resolve(process.argv[2] || fs.mkdtempSync('/tmp/oecnr-'));
const root = process.env.OEC_ROOT;
if (!root) {
  console.error('OEC_ROOT is not set');
  process.exit(2);
}

const MAM = { manufacturer: 'OI4 Alliance', manufacturerUri: 'oi4.org', productCode: 'NR' };

/**
 * The D7 flow: an application with Data, Config and Syslog, and an asset.
 * The first values come right after the start: a DataSet exists (and is
 * listed in the PublicationList) from its first value on.
 */
function flow() {
  return [
    { id: 'tab', type: 'tab', label: 'OEC D7' },
    { id: 'broker', type: 'oec-broker', mode: 'container', root },
    {
      id: 'app', type: 'oec-app', broker: 'broker', serviceType: 'Utility', heartbeat: 30,
      ...MAM, model: 'Node-RED OEC', serialNumber: 'D7-1', softwareRevision: require(path.join(pkg, 'package.json')).version,
    },
    { id: 'asset', type: 'oec-asset', ...MAM, model: 'Simulated sensor', serialNumber: 'D7-S1', deviceClass: 'Sensor' },
    { id: 'go', z: 'tab', type: 'inject', once: true, onceDelay: 0.1, repeat: '5', payload: '', payloadType: 'date', wires: [['temp', 'level']] },
    { id: 'temp', z: 'tab', type: 'function', func: 'return { payload: { Temperature: 20 + Math.round(Math.random() * 50) / 10 } };', outputs: 1, wires: [['data']] },
    { id: 'level', z: 'tab', type: 'function', func: 'return { payload: Math.round(Math.random() * 100) };', outputs: 1, wires: [['adata']] },
    {
      id: 'data', z: 'tab', type: 'oec-data', app: 'app', tag: 'Climate', writable: false,
      fields: JSON.stringify([{ Name: 'Temperature', Type: 'Double', Unit: 'CEL' }]), wires: [[]],
    },
    {
      id: 'setpoint', z: 'tab', type: 'oec-data', app: 'app', tag: 'Setpoint', writable: true,
      fields: JSON.stringify([{ Name: 'Value', Type: 'Double', Unit: 'CEL' }]), wires: [[]],
    },
    { id: 'go2', z: 'tab', type: 'inject', once: true, onceDelay: 0.1, payload: '21', payloadType: 'num', wires: [['setpoint']] },
    {
      id: 'config', z: 'tab', type: 'oec-config', app: 'app', contextName: 'Settings',
      definition: JSON.stringify({
        Publication: {
          Interval: { Type: 'Number', Value: '5', Unit: 'SEC', Description: 'Publication interval', Validation: { Min: 1, Max: 3600 } },
          Enabled: { Type: 'Boolean', Value: 'true' },
        },
        Access: { Token: { Type: 'String', Value: 'secret', Sensitive: true } },
      }),
      wires: [['log']],
    },
    { id: 'log', z: 'tab', type: 'function', func: 'return { payload: "Settings " + msg.oec.reason };', outputs: 1, wires: [['event']] },
    { id: 'event', z: 'tab', type: 'oec-event', app: 'app', category: 'Syslog', facility: 1, severity: '5' },
    { id: 'adata', z: 'tab', type: 'oec-data', app: 'app', asset: 'asset', tag: 'Level', fields: '', wires: [[]] },
    { id: 'ahealth', z: 'tab', type: 'oec-health', app: 'app', asset: 'asset', initial: 'NORMAL_0', score: 100 },
  ];
}

fs.mkdirSync(path.join(userDir, 'node_modules', '@oi4'), { recursive: true });
const link = path.join(userDir, 'node_modules', '@oi4', 'node-red-oec');
if (!fs.existsSync(link)) fs.symlinkSync(pkg, link, 'dir');
fs.writeFileSync(path.join(userDir, 'flows.json'), JSON.stringify(flow(), null, 2));

const RED = require(require.resolve('node-red', { paths: [pkg] }));
const server = http.createServer();
RED.init(server, {
  userDir,
  flowFile: 'flows.json',
  httpAdminRoot: false,
  httpNodeRoot: false,
  disableEditor: true,
  credentialSecret: false,
  externalModules: { autoInstall: false, palette: { allowInstall: false } },
  logging: { console: { level: process.env.OEC_NR_LOG || 'info' } },
});

let stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  try {
    await RED.stop();
  } finally {
    process.exit(0);
  }
}
process.on('SIGTERM', stop);
process.on('SIGINT', stop);

RED.start().catch((err) => {
  console.error(err);
  process.exit(1);
});
