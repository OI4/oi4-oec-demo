'use strict';
// Opt-in live conformance run (D7, oec-check test --level 2) of an embedded
// Node-RED runtime with the flow of test/d7/runtime.js against the test broker
// of the Rust SDK behind the oec-check proxy.
//
//   OEC_D7=<oec-check> [OEC_TESTBROKER=<go-testbroker>] npm test
//
// Skipped without OEC_D7 (or without the test broker); OEC_REQUIRE=1 fails
// instead. OEC_D7_REPORT=<file> keeps the JSON report, OEC_D7_RECORD=<file>
// records the bus traffic (JSONL).

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const net = require('node:net');
const readline = require('node:readline');
const { spawn, execFile } = require('node:child_process');
const oec = require('../lib/oec');

// The tests that pass for this flow (oec-check test --level 2 --peer --announce).
const PASS = [
  'T7-MON-01', 'T7-MON-02', 'T7-MON-03', 'T7-MON-04', 'T7-MON-05', 'T7-MON-06',
  'T7-MON-08', 'T7-MON-09', 'T7-MON-10', 'T7-MON-11', 'T7-MON-12', 'T7-MON-13',
  'T7-MON-14', 'T7-MON-15', 'T7-MON-16', 'T7-MON-18', 'T7-MON-19',
  'T7-CON-01', 'T7-CON-02', 'T7-CON-03', 'T7-CON-04',
  'T7-LIFE-01', 'T7-LIFE-02', 'T7-LIFE-03', 'T7-LIFE-04',
  'T7-ADR-03',
  'T7-GET-01', 'T7-GET-02', 'T7-GET-05', 'T7-GET-06', 'T7-GET-08', 'T7-GET-09',
  'T7-SET-01', 'T7-SET-02', 'T7-SET-05', 'T7-SET-06', 'T7-SET-07', 'T7-SET-08', 'T7-SET-11', 'T7-SET-12',
];

function tool(env, def) {
  const p = process.env[env] || def;
  if (p && fs.existsSync(p)) return p;
  return null;
}

/** The monorepo default of the test broker (absent in the oi4-oec-demo copy). */
const TESTBROKER = path.resolve(__dirname, '..', '..', '..', 'sdk', 'rust', 'target', 'debug', 'examples', 'go-testbroker');

function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer().listen(0, '127.0.0.1', () => {
      const { port } = s.address();
      s.close(() => resolve(port));
    }).on('error', reject);
  });
}

function write(file, content, mode) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, content, { mode });
}

function run(cmd, args) {
  return new Promise((resolve) => {
    execFile(cmd, args, { maxBuffer: 64 << 20 }, (err, stdout, stderr) => resolve({ err, out: stdout + stderr }));
  });
}

/** Starts the test broker; resolves {port, ca, stop}. */
function startBroker(exe, dir) {
  return new Promise((resolve, reject) => {
    const p = spawn(exe, [dir], { stdio: ['pipe', 'pipe', 'inherit'] });
    const rl = readline.createInterface({ input: p.stdout });
    let first = true;
    rl.on('line', (line) => {
      if (!first) return;
      first = false;
      try {
        const b = JSON.parse(line);
        resolve({
          ...b,
          stop: () => new Promise((r) => { p.once('exit', r); p.stdin.end(); }),
        });
      } catch (err) {
        reject(new Error(`test broker: ${err.message}: ${line}`));
      }
    });
    p.once('error', reject);
    p.once('exit', (code) => { if (first) reject(new Error(`test broker exited with ${code}`)); });
  });
}

const check = tool('OEC_D7');
const testbroker = tool('OEC_TESTBROKER', TESTBROKER);
const missing = !check ? 'OEC_D7 (oec-check) is not set' : !testbroker ? 'OEC_TESTBROKER (go-testbroker) is missing' : null;
if (missing && process.env.OEC_REQUIRE === '1') throw new Error(missing);

test('D7 level 2: embedded Node-RED flow', { skip: missing || false, timeout: 15 * 60 * 1000 }, async (t) => {
  const work = fs.mkdtempSync(path.join(os.tmpdir(), 'oecnr-d7-'));
  // node:test runs after hooks in registration order: one hook, in reverse.
  const cleanup = [() => fs.rmSync(work, { recursive: true, force: true })];
  t.after(async () => {
    for (const f of cleanup.reverse()) await f();
  });
  const broker = await startBroker(testbroker, path.join(work, 'broker'));
  cleanup.push(() => broker.stop());

  const proxyCA = path.join(work, 'proxy-ca.pem');
  const proxyKey = path.join(work, 'proxy-ca.key');
  const { privateKey } = crypto.generateKeyPairSync('ec', { namedCurve: 'P-256' });
  write(proxyKey, privateKey.export({ type: 'pkcs8', format: 'pem' }));
  const ca = await run('openssl', ['req', '-x509', '-key', proxyKey, '-out', proxyCA, '-days', '1',
    '-subj', '/CN=OEC Node-RED test proxy CA',
    '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign']);
  assert.ifError(ca.err);
  const proxyPort = await freePort();

  const root = path.join(work, 'storage');
  write(path.join(root, 'etc/hostname'), 'nodered-d7\n');
  write(path.join(root, 'etc/oi4/mqtt/broker.json'), JSON.stringify({ Address: 'localhost', SecurePort: proxyPort, MaxPacketSize: 262144 }));
  write(path.join(root, 'etc/oi4/certs/ca.pem'), fs.readFileSync(proxyCA));
  write(path.join(root, 'run/secrets/oi4/credentials'), 'dXNlcjpwYTpzcw==\n'); // base64("user:pa:ss")
  const password = path.join(work, 'password');
  write(password, 'pa:ss\n');

  const pid = path.join(work, 'nodered.pid');
  const log = path.join(work, 'nodered.log');
  const start = path.join(work, 'start.sh');
  const stop = path.join(work, 'stop.sh');
  const q = JSON.stringify;
  write(start, `#!/bin/sh\nOEC_ROOT=${q(root)} ${q(process.execPath)} ${q(path.join(__dirname, 'd7', 'runtime.js'))} ${q(path.join(work, 'userdir'))} >> ${q(log)} 2>&1 &\necho $! > ${q(pid)}\n`, 0o755);
  write(stop, `#!/bin/sh\nP=$(cat ${q(pid)})\nkill -TERM "$P"\nwhile kill -0 "$P" 2>/dev/null; do sleep 0.1; done\n`, 0o755);
  const started = await run(start, []);
  assert.ifError(started.err);
  cleanup.push(async () => {
    try {
      const p = Number(fs.readFileSync(pid, 'utf8').trim());
      process.kill(p, 'SIGTERM');
      for (let end = Date.now() + 20000; Date.now() < end;) {
        process.kill(p, 0);
        await new Promise((r) => setTimeout(r, 100));
      }
      process.kill(p, 'SIGKILL');
    } catch { /* gone */ }
  });

  const il = oec.identificationLink('oi4.org', 'Node-RED OEC', 'NR', 'D7-1');
  const report = path.join(work, 'd7.json');
  const began = Date.now();
  const res = await run(check, ['test',
    '--broker', `mqtts://localhost:${broker.port}`, '--ca', broker.ca,
    '--username', 'user', '--password-file', password,
    '--app', `Utility/${il}`, '--level', '2', '--timeout', '3s',
    '--proxy-listen', `127.0.0.1:${proxyPort}`, '--proxy-ca', proxyCA, '--proxy-ca-key', proxyKey,
    '--stop-cmd', stop, '--start-cmd', start, '--restart-wait', '15s', '--peer', '--announce',
    '--format', 'text', '--report', `json=${report}`,
    ...(process.env.OEC_D7_RECORD ? ['--record', process.env.OEC_D7_RECORD] : [])]);
  t.diagnostic(`oec-check test --level 2: ${res.err ? res.err.message : 'ok'} after ${Math.round((Date.now() - began) / 1000)} s`);
  const showLog = () => {
    const s = fs.existsSync(log) ? fs.readFileSync(log, 'utf8') : '';
    return `${res.out}\n--- nodered.log (last 64 KiB) ---\n${s.slice(-65536)}`;
  };
  assert.ok(fs.existsSync(report), `no report: ${res.err}\n${showLog()}`);
  const raw = fs.readFileSync(report, 'utf8');
  if (process.env.OEC_D7_REPORT) fs.writeFileSync(process.env.OEC_D7_REPORT, raw);
  const r = JSON.parse(raw);
  t.diagnostic(`summary ${JSON.stringify(r.summary)}, exit ${r.exit}`);
  const outcome = Object.fromEntries(r.tests.map((x) => [x.id, x.outcome]));
  const failed = r.tests.filter((x) => x.outcome === 'fail').map((x) => x.id);
  const passed = r.tests.filter((x) => x.outcome === 'pass').map((x) => x.id);
  const missingPass = PASS.filter((id) => outcome[id] !== 'pass');
  const unpinned = passed.filter((id) => !PASS.includes(id));
  assert.ok(!failed.length && !missingPass.length && !unpinned.length && r.exit === 0,
    `failed ${failed}; pinned but not passed ${missingPass.map((id) => `${id}=${outcome[id]}`)}; passed but not pinned ${unpinned}; exit ${r.exit}\n${showLog()}`);
});
