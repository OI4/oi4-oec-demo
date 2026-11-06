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

## Where the demos come from

The demos are developed in the OEC source tree, in the folders `oec/demos`
and `oec/demos-smart-sensors`, together with the SDKs and the compliance
tools D7/D8 that build and check them. This repository holds an
**unmodified copy of these folders** as `demos/` and `demos-smart-sensors/`:
it is laid out like the OEC source tree, but contains only its demos. Paths
in the copied files that leave these folders (`../sdk/…`,
`../conformance/…`, `../spec/…`, `../dev/…`, `../ci/…`, the image build
context `oec/` with its `.dockerignore`; for the smart sensors the Micro SDK
component `../../../sdk/micro/port/esp-idf/oecm` and `../../dev`) refer to
the OEC source tree.

Changes are made in `oec/demos` and `oec/demos-smart-sensors` of the OEC
source tree and copied here; do not edit `demos/` or
`demos-smart-sensors/` in this repository.

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
