# Smart sensor demo: ESP32 + DHT22

An ESP32 or ESP32-S3 with a DHT22 (AM2302) temperature and humidity sensor
as an OEC smart device, on the [OEC Micro SDK](../../sdk/micro/README.md)
(ESP-IDF port). It is the ESP-IDF example of the Micro SDK with a real
sensor instead of the simulated one and Wi-Fi only. Guide with the dev
environment, the registry, the config service, the logger and D7:
[Smart sensor on an ESP32](https://oi4.github.io/oi4-oec-documentation/docs/community/demos/smart-sensor).

| | |
|---|---|
| MAM | Model `OEC Micro ESP32 DHT22`, ProductCode `OECM-ESP32-DHT22`, SerialNumber = the base MAC (ESP32-S3: `OEC Micro ESP32-S3 DHT22`, `OECM-ESP32S3-DHT22`), DeviceClass `Sensor` |
| ServiceType | `OTConnector` (a directly connected smart device) |
| Messaging level | 3 (provisioning key `level`, default `CONFIG_DEMO_LEVEL`) |
| Data | `Climate`: Temperature (°C, −40 … 80), Humidity (%, 0 … 100), Label; read-only |
| Config | `Sensor`/`General`: Interval 2 … 3600 s (default 10), Label (32 characters); not stored across a restart |
| Health | `NORMAL_0`/100; `FAILURE_1`/0 after three failed rounds of readings in a row, back on the next good reading |

## Files

| File | Content |
|---|---|
| `main/main.c` | Wi-Fi, SNTP, provisioning from NVS, the device loop, the serial console (`stop`, `time`) – as in the example of the Micro SDK |
| `main/climate.c` | the DHT22: Data and Config tables, a reader task (critical section, core 1 if there is one), Health |
| `main/Kconfig.projbuild` | `DEMO_DHT22_GPIO` (default 23, ESP32-S3: 4), `DEMO_LEVEL`, `DEMO_CONSOLE` |
| `main/idf_component.yml` | the component `oecm` from `../../../sdk/micro/port/esp-idf/oecm` (the OEC source tree) |
| `sdkconfig.defaults`, `partitions.csv` | as in the example: 4 MB flash, the NVS partition `oecprov` at 0x10000 |
| `dependencies.lock` | the resolved components (`espressif/mqtt` 1.1.0) |
| `build.sh` | builds `esp32` and/or `esp32s3` into `target/<variant>/` |
| `provision.sh` | writes the settings for the broker of `oec/dev` into `oecprov` |

## Wiring

DHT22 VCC at 3.3 V, GND at GND, DATA at GPIO 23 (`idf.py menuconfig` →
*OEC smart sensor demo* to change it; the ESP32-S3 has no GPIO 23). A bare
sensor needs a pull-up of about 10 kΩ between DATA and VCC; most breakout
modules have one. The internal pull-up is on as well.

## Build and flash

Needs ESP-IDF 6.0 or later (`. $IDF_PATH/export.sh`) and the OEC source
tree around this folder; the first build fetches `espressif/mqtt` from the
ESP Component Registry.

```sh
./build.sh esp32                    # or esp32s3; both without argument
cd target/esp32
python -m esptool -p <serial port> write-flash @flash_args
```

## Provision

Nothing of a site is compiled into the firmware: the broker, its CA, the
credentials, the level and the Wi-Fi are read from the NVS partition
`oecprov` (keys: [Micro SDK README](../../sdk/micro/README.md#example-firmware)).
`provision.sh` writes them for the broker of the dev environment
(`oec/dev`, after `go run ./pki`): the CA and the credentials of the dev
application `oec-dev-app` come from `oec/dev/generated`, the Wi-Fi from the
environment. The CSV and the image stay in a private temporary folder that
is removed at the end.

```sh
WIFI_SSID=<SSID> WIFI_PASS=<password> ./provision.sh -p <serial port> -u mqtts://<ip>:8883
```

`<ip>` is an address of the broker that the board reaches and that its
certificate names (the guide shows how to publish the broker in the LAN).
The serial log (`python -m esp_idf_monitor -p <serial port>`) shows the
Wi-Fi address, the time sync, the readings and the Identification Link.
Before passing the board on, erase it: `python -m esptool -p <serial port> erase-flash`.

## Check

- OEC Registry `https://localhost:5798/`, config service
  `http://127.0.0.1:8783/`, logger `http://127.0.0.1:8781/` (compose
  profiles `demos` and `registry` of `oec/dev`).
- D7 against the board, from `oec/dev`:

  ```sh
  oec-check test --broker mqtts://localhost:8883 --ca generated/broker/ca.pem \
      --username oec-probe --password-file generated/probe/password \
      --app <Identification Link> --level 3
  ```

  With D7's proxy and restart hooks over the serial port also the
  connection and restart tests:
  [Run on real hardware](../../sdk/micro/README.md#run-on-real-hardware-opt-in).

The sensor code ran on an AZ-Delivery ESP32-WROOM-32 DevKit, DHT22 on
GPIO 23, ESP-IDF 6.1, against `oec/dev`: D7 L3 48 pass, 0 fail, 7 skip,
PASS (2026-11-28, as a local variant of the Micro SDK example). This folder
packages it as a demo of its own; its builds for `esp32` and `esp32s3` are
checked, a run of this firmware on the board is still open.
