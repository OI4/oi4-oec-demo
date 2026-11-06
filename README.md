# OI4 Open Edge Computing 2.0 – demo services

Three small services show the OEC 2.0 SDKs at work. Each one is an OEC
application on the message bus and serves a small Web UI of its own:

| Demo | Language, SDK | ServiceType | Messaging level | Web UI |
|---|---|---|---|---|
| [`demos/weather/`](demos/weather/README.md) | Go on the Go SDK | `OTConnector` | L3 | `http://127.0.0.1:8782/` |
| [`demos/logger/`](demos/logger/README.md) | C17 on the C ABI (`oec-ffi`) | `Utility` | L2 | `http://127.0.0.1:8781/` |
| [`demos/config/`](demos/config/README.md) | Rust on the crate `oec` | `Utility` | L1 | `http://127.0.0.1:8783/` |

Guide: [Demo services](https://oi4.github.io/oi4-oec-documentation/docs/community/demos)
in the Community section of the OEC guideline site. Details, images and
tests: [`demos/README.md`](demos/README.md).

## Smart sensor demos

Real microcontroller boards as OEC smart devices on the OEC Micro SDK. They
join the demo services on the broker of the local development environment:
the OEC Registry lists them, the config service changes their `Config`, the
logger records them.

| Demo | Board, sensor | Platform | ServiceType | Messaging level |
|---|---|---|---|---|
| [`demos-smart-sensors/esp32-dht22/`](demos-smart-sensors/esp32-dht22/README.md) | ESP32 or ESP32-S3, DHT22 (AM2302) | ESP-IDF 6.1, C17 | `OTConnector` | L3 |

Guide: [Smart sensor on an ESP32](https://oi4.github.io/oi4-oec-documentation/docs/community/demos/smart-sensor).
Overview: [`demos-smart-sensors/README.md`](demos-smart-sensors/README.md).

## Home Assistant integration (HACS)

[`custom_components/oi4_oec/`](custom_components/oi4_oec/) is a
[Home Assistant](https://www.home-assistant.io/) integration for OEC 2.0. It
joins the OEC message bus as an OEC application itself (`ITConnector`,
Messaging Level 2) and shows the applications and assets on the bus as Home
Assistant devices: their Health, whether an application is online, and the
values of their `Data` (e.g. the weather stations and the ESP32 smart
sensor).

Install it in Home Assistant 2026.11 or later with the
[Marketplace](https://www.home-assistant.io/integrations/marketplace):
*Settings → Marketplace → Add from link*, `https://github.com/OI4/oi4-oec-demo`,
type *Integration*; then install *OI4 Open Edge Computing* and add the
integration with the broker address, the credentials and the CA
certificate of the OEC platform. Home Assistant 2026.10 installs it with
[HACS](https://hacs.xyz/) as a custom repository. Both install the latest
GitHub release of this repository.

Documentation, entities, behaviour on the bus and tests:
[`integrations/home-assistant/README.md`](integrations/home-assistant/README.md).

## Node-RED nodes

[`integrations/node-red/`](integrations/node-red/) is the npm package
`@oi4/node-red-oec` for [Node-RED](https://nodered.org/): a flow becomes an
OEC 2.0 application (Messaging Level 2). Config nodes create the broker
connection (from the OEC container storage or set by hand), the
applications and the assets they represent; one node per resource
(`oec-health`, `oec-data`, `oec-config`, `oec-event`) publishes the values,
and the application answers `Get` requests from them. `oec-subscribe` and
`oec-get` receive the publications of other applications and send `Get`
requests.

Install it in the Node-RED user directory and restart Node-RED:

```sh
cd ~/.node-red
npm install /path/to/oi4-oec-demo/integrations/node-red
```

*Import → Examples → @oi4/node-red-oec* has a demo flow. Documentation,
nodes, behaviour on the bus and tests:
[`integrations/node-red/README.md`](integrations/node-red/README.md).

## Where the demos come from

The demos are developed in the OEC source tree, in the folders `oec/demos`,
`oec/demos-smart-sensors` and `oec/integrations`, together with the SDKs and the compliance
tools D7/D8 that build and check them. This repository holds an
**unmodified copy of these folders** as `demos/` and `demos-smart-sensors/`:
it is laid out like the OEC source tree, but contains only its demos. Paths
in the copied files that leave these folders (`../sdk/…`,
`../conformance/…`, `../spec/…`, `../dev/…`, `../ci/…`, the image build
context `oec/` with its `.dockerignore`; for the smart sensors the Micro SDK
component `../../../sdk/micro/port/esp-idf/oecm` and `../../dev`) refer to
the OEC source tree.

The Home Assistant integration comes from
`oec/integrations/home-assistant`: HACS needs `custom_components/oi4_oec/`
and `hacs.json` at the root of the repository, all other files of the
folder (README, tests, checks) are in `integrations/home-assistant/`. The
Node-RED nodes come unmodified from `oec/integrations/node-red` as
`integrations/node-red/`.

Changes are made in `oec/demos`, `oec/demos-smart-sensors` and
`oec/integrations` of the OEC source tree and copied here; do not edit
`demos/`, `demos-smart-sensors/`, `custom_components/`, `hacs.json` or
`integrations/` in this repository.

## Build and run

The demos need the OEC SDKs (Rust crate `oec`, C ABI `oec-ffi`, Go SDK) and,
for the tests, D7 and the test broker – all of them in the OEC source tree.
Build and test them there:

```sh
oec/demos/check.sh   # builds the SDK parts, the three demos and runs their tests with D7
```

The smart sensor firmware is built and flashed with ESP-IDF in the OEC
source tree, see [`demos-smart-sensors/esp32-dht22/README.md`](demos-smart-sensors/esp32-dht22/README.md).

How to start a single demo against a broker, the container images
(`oi4/oec-logger`, `oi4/oec-weather`, `oi4/oec-config`) and the compose
profile `demos` of the local development environment are described in
[`demos/README.md`](demos/README.md) and the README of each demo.

## Earlier versions

The OEC 1.x demo (TypeScript, namespace `Oi4`) is in the history of this
repository. OEC 2.0 (namespace `Oi4v2`) is not compatible with 1.x.

## License

MIT, see [`LICENSE`](LICENSE). The weather service shows data of MET Norway
(CC BY 4.0) while its provider `met-no` is active.
