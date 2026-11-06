# OEC smart sensor demos

Real microcontroller boards as OEC smart devices, built with the
[OEC Micro SDK](../sdk/micro/README.md). They join the demo services of
[`../demos`](../demos/README.md) on the broker of the local development
environment [`../dev`](../dev/README.md): the OEC Registry lists them, the
config service changes their `Config`, the logger records them. Guide:
[Smart sensor on an ESP32](https://oi4.github.io/oi4-oec-documentation/docs/community/demos/smart-sensor)
in the Community section of the guideline site.

| Demo | Board, sensor | Platform | ServiceType | Messaging level | State |
|---|---|---|---|---|---|
| [`esp32-dht22/`](esp32-dht22/README.md) | ESP32 or ESP32-S3, DHT22 (AM2302) | ESP-IDF 6.1, C17 | `OTConnector` | L3 | tested on an ESP32-WROOM-32 against `oec/dev`: D7 L3 PASS (46 pass, 0 fail); `esp32s3` built only |

The demos are firmware: they are built and flashed by hand (opt-in, never
in the gate). The CI builds them with the ESP-IDF builds of the Micro SDK
(`oec/ci/micro-esp-idf.sh`, workflow `micro.yml`); a board is not needed
for that.
