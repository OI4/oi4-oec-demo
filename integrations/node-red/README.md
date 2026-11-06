# Node-RED nodes for OI4 Open Edge Computing 2.0

Nodes for [Node-RED](https://nodered.org/) that turn a flow into an OEC 2.0
application on the message bus: a config node creates the application, and
one node per resource publishes values and answers `Get` requests for that
resource. Consumer nodes receive the publications of other applications and
send `Get` requests to them.

| | |
|---|---|
| Package | `@oi4/node-red-oec` |
| Node-RED | 3.1 or newer, Node.js 18 or newer |
| OEC role | one OEC application per `oec-app` config node, Messaging Level 2, any ServiceType |
| Connection | MQTT 3.1.1 over TLS ([mqtt.js](https://github.com/mqttjs/MQTT.js)), QoS 0, no retain |
| Conformance | D7 `oec-check test --level 2`: no failure (see [Tests](#tests)) |

## Install

The package is not published on npm yet. Install it from this folder into
the Node-RED user directory and restart Node-RED:

```sh
cd ~/.node-red
npm install /path/to/integrations/node-red
```

or build a tarball with `npm pack` in this folder and install the
`oi4-node-red-oec-<version>.tgz` in the same way (also with *Manage
palette → Install → upload*). The nodes appear in the palette category
**OEC**. *Import → Examples → @oi4/node-red-oec → oec-demo* imports
a demo flow.

## Nodes

Config nodes:

| Node | |
|---|---|
| `oec-broker` | The MQTT broker of the message bus: from the **container storage** of the OEC Runtime & Infrastructure Guideline (RIG 4.1) below a Root, or **set by hand** (host, port, TLS, credentials) |
| `oec-app` | An OEC application: ServiceType, Master Asset Model (MAM), Health heartbeat, Syslog levels. Several applications per flow and per broker are possible |
| `oec-asset` | An asset the application represents (its MAM). A resource node with an asset acts for the asset as Source instead of the application |

Resource nodes (one per resource and DataSet; each takes an application and
optionally an asset):

| Node | Resource | Input | Output |
|---|---|---|---|
| `oec-health` | Health (MG 9.1.2) | a Health state or `{Health, HealthScore}` | – |
| `oec-data` | Data and Metadata (MG 9.1.4/9.1.5) of one DataSet (Tag) | `{field: value}` or a single value | with *writable*: the values of a `Set Data` |
| `oec-config` | Config (MG 9.1.3) of one context | `{group: {name: value}}` | the values on deploy, `Set Config`, input and `Get` |
| `oec-event` | Event (MG 9.1.6): Syslog (facility, severity) or Generic (level, number) | the description (string) or the details (object) | – |

The application answers `Get` of every resource itself (MAM, Health,
Profile, Config, Data, Metadata, PublicationList, SubscriptionList) from the
last values of the nodes, with pagination; the nodes only provide the
values. `Set Data` (writable DataSets), `Set Config` and
`Set PublicationList` (Syslog levels) are accepted and answered with the
new values and the Status `Good`; everything else with `BadNotWritable` or
`BadNotFound`.

Consumer nodes:

| Node | |
|---|---|
| `oec-subscribe` | Receives `Pub` messages of other applications (by ServiceType, application, resource, Source and Filter, or by a topic filter). The subscription is listed in the SubscriptionList |
| `oec-get` | Sends a `Get` to an application on input and sends the answer (all pages); no answer within the timeout or a Status `Bad` is an error for a `catch` node |

The help of every node in the editor describes its settings and messages.

## Broker and container storage

In an OEC container (RIG 4.1) keep *container storage* with Root `/`: the
node reads `etc/oi4/mqtt/broker.json`, the CA (`etc/oi4/mqtt/broker_ca.pem`
or `etc/oi4/certs/ca.pem`) and either the client certificate
(`etc/oi4/certs/<container name>.pem` with
`run/secrets/oi4/private_key.pem`) or the credentials
(`run/secrets/oi4/credentials`). The container name (host name) is the MQTT
ClientId; further applications on the same broker node add a suffix.

Outside a container, point the Root at a copy of a sample storage of the
development environment `oec/dev` of the OEC source tree (broker on
`localhost:8883`):

```sh
cp -R oec/dev/generated/roots/oec-dev-app /tmp/node-red-root   # Root: /tmp/node-red-root
```

or choose *set by hand*: host, port, TLS (a `tls-config` node with the CA),
user name and password.

## Behaviour on the bus

- **Birth** on every connect: MAM, Health, Profile, PublicationList,
  SubscriptionList, then Config, Metadata and Data of the application and of
  every asset. **Last Will** `FAILURE_1`/0, created anew for every
  connection; `NORMAL_0`/0 before a graceful disconnect (redeploy, stop).
- **Health** of the application at least every *Heartbeat* seconds (1 … 60).
- A Data DataSet is listed in the PublicationList from its first value on;
  removed nodes are removed from the lists on the next deploy.
- **DataSetWriterIds**: MAM 10, Health 11, Profile 12, PublicationList 13,
  Status 14 … 16, SubscriptionList 17 (shared by all Sources); every other
  DataSet gets a stable id from 18 on (hash of resource, Source and Filter).
- **Config** values are kept in the node context: with a persistent
  context store (`contextStorage` in `settings.js`) they survive a restart.
  Sensitive values are masked in every publication.
- **Syslog levels** (REQ-TOP-024 … 027): the application publishes its
  Syslog events of the level selected in `oec-app` (default `Notice`) and
  the more severe ones; Debug is always off by default. The PublicationList
  lists every level with `Config` `MODE_1`, so a `Set PublicationList` of
  the bus switches a level on (`FILTER_4`) or off (`OFF_0`) until the next
  deploy. Syslog events of assets are always published.
- `Get` requests of `oec-get` use a temporary subscription that is not
  listed in the SubscriptionList (MG 9.1.9).

## Limitations

- Messaging Level 2: no ReferenceDesignation and no `NewDataSetWriterId`
  (D7 reports them as warnings of SHOULD requirements of Level 3).
- No `Del` of Data or Config; the SubscriptionList is not writable.
- `Get Metadata` of an unknown DataSet is not answered.
- Arrays and structures in Data fields are passed through unchecked.

## Tests

```sh
./check.sh                          # npm ci on first use, unit and node tests
OEC_D7=/path/to/oec-check ./check.sh   # + D7 live run
```

| File | |
|---|---|
| `test/oec.test.js`, `test/provider.test.js`, `test/broker.test.js` | protocol core, application and broker options (sans-IO) |
| `test/nodes.test.js` | the nodes and the example flow in Node-RED ([node-red-node-test-helper](https://github.com/node-red/node-red-node-test-helper)) against an in-process broker ([aedes](https://github.com/moscajs/aedes)) |
| `test/d7.test.js`, `test/d7/runtime.js` | opt-in: an embedded Node-RED runs a flow in container mode, `oec-check test --level 2 --peer --announce` checks it against the `go-testbroker` of the Rust SDK; every test in the pinned list must pass |

The D7 live run needs `oec-check` (`cd oec/conformance && go build ./cmd/oec-check`)
and `go-testbroker` (`cd oec/sdk/rust && cargo build --example go-testbroker`;
`OEC_TESTBROKER` if not in the monorepo). `OEC_D7_REPORT=<file>` keeps the
report, `OEC_D7_RECORD=<file>` records the traffic, `OEC_REQUIRE=1` fails
instead of skipping when a tool is missing. Result for this flow: 40 pass,
3 warn (ReferenceDesignation, NewDataSetWriterId), no fail.

Note: run long-lived Node-RED instances on macOS in a container (Linux) or
in the foreground: App Nap delays the timers of a background process, which
breaks the Health heartbeat.

## Development

The nodes are developed in the OEC source tree (`oec/integrations/node-red/`)
and copied unmodified into `OI4/oi4-oec-demo` (`integrations/node-red/`).
Change them in the OEC source tree.

| File | |
|---|---|
| `lib/oec.js` | OEC protocol core: topics, Oi4Identifier, NetworkMessages, DataSetClassIds, Config checks, pagination (sans-IO) |
| `lib/provider.js` | the OEC application with its assets: Birth, lists, answers to `Get`/`Set`/`Del` (sans-IO) |
| `lib/session.js` | MQTT session with mqtt.js: Will, Birth, heartbeat, requests, Close, reconnect |
| `lib/broker.js` | connection options from the container storage or set by hand |
| `lib/consumer.js`, `lib/nodes.js` | helpers of the consumer nodes and of the resource nodes |
| `nodes/*.js`, `nodes/*.html` | the Node-RED nodes and their editor and help |
| `examples/oec-demo.json` | the demo flow |

## License

MIT.
