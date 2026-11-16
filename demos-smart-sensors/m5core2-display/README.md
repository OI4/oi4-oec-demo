# Climate display demo: M5Stack Core2

An M5Stack Core2 as an OEC smart device that shows the climate sensors of
other OTConnector applications, e.g. the [DHT22 demo](../esp32-dht22/README.md),
on its display. It is built on the Arduino library of the
[OEC Micro SDK](../../sdk/micro/README.md) and M5Unified and uses the
consumer subscriptions of the Micro SDK
([Consumer subscriptions](../../sdk/micro/README.md#consumer-subscriptions-c-114)).
Guide with the dev environment:
[Smart sensor on an ESP32](https://oi4.github.io/oi4-oec-documentation/docs/community/demos/smart-sensor).

| | |
|---|---|
| MAM | Model `OEC Micro M5Stack Core2 Display`, ProductCode `OECM-CORE2-DISPLAY`, SerialNumber = the base MAC, DeviceClass `Display` |
| ServiceType | `OTConnector` (a directly connected smart device, MG 7.2) |
| Messaging level | 3 (provisioning key `level`: 2 or 3; the consumer subscriptions need Level 2) |
| Subscriptions | `Oi4v2/OTConnector/+/Pub/Data/+/Climate` and `Oi4v2/OTConnector/+/Pub/Health/+`, listed in the SubscriptionList (`NONE_0`, not writable) |
| Data | `View`: Sensors (number of sensors with Climate data), Shown (Identification Link of the shown sensor); read-only |
| Config | `Display`/`General`: Title (24 characters, default `OEC Climate`), StaleAfter 10 … 3600 s (default 120), Brightness 5 … 100 % (default 50), Cycle 0 … 3600 s (default 0: only by button, tap or swipe), History 5 … 720 min (default 30: the time shown by the graph); `Display`/`Scale`: ColdTemperature, HotTemperature (−40 … 80 °C, default 15/30); stored in NVS (namespace `demo.Display`), kept across a restart |
| Health | `NORMAL_0`/100 |

The display shows one sensor:

- the Label (or the SerialNumber; in a smaller font or shortened when it
  is long) and a chip with its Health in plain words (`NORMAL_0` →
  "Normal") and the HealthScore;
- a graph of the temperature over the last `History` minutes: the curve
  in the colour of the scale, the area below it fading into the card, the
  highest and the lowest value; the latest point flashes and sends out a
  fading ring when a new value arrives; the display keeps up to 128 points per
  sensor in RAM, so the graph starts empty after a restart;
- the temperature with a trend arrow (up/down: at least 0.3 °C within the
  last five minutes, grey: steady) and below it a colour scale as wide as
  the number, from `ColdTemperature` (blue) over green and yellow to
  `HotTemperature` (red), with a knob at the temperature – the same
  colours as the display and the RGB LED of the ESP32-S3-LCD-1.47 variant
  of the DHT22 demo; the lower of the two temperatures is the cold end,
  equal values span 1 °C;
- the Humidity and the dew point (Magnus formula);
- the SerialNumber, the age of the last `Climate` publication and, with
  more than one sensor, one dot per sensor.

A sensor without data for `StaleAfter` seconds is shown grey, its age red.
Button A, a tap or a swipe to the left shows the next sensor, a swipe to
the right the previous one; with `Cycle` above 0 the display also changes
to the next one after that many seconds (a thin line below the title shows
the time left). The display keeps up to six sensors (a sensor that only
sent Health makes room for one with data); its own publications are
ignored.

## Files

| File | Content |
|---|---|
| `m5core2-display.ino` | provisioning from NVS, Wi-Fi, SNTP, the display, the device loop, the serial console (`stop`) |
| `climate_view.c`, `climate_view.h` | the consumer side: subscriptions, `on_message` (`oecm_topic_app`, `oecm_topic_source`, `oecm_msg_number`, `oecm_msg_text`), Data and Config tables, the Config in NVS, the colour scale |
| `partitions.csv` | 16 MB flash: `nvs`, `otadata`, the NVS partition `oecprov` at 0x10000 (as the DHT22 demo), the application |
| `build.sh` | builds into `target/` with the pinned Arduino toolchain of `oec/ci/micro-arduino.sh` |

## Build and flash

Needs the OEC source tree around this folder and the pinned Arduino
toolchain (arduino-cli, Arduino-ESP32 3.3, M5Unified, M5GFX below
`~/.oec-arduino`; network, several GB, once):

```sh
../../ci/micro-arduino.sh 3.3
./build.sh
cd target
python -m esptool -p <serial port> write-flash @flash_args
```

`esptool` comes with ESP-IDF (`. $IDF_PATH/export.sh`) or `pip install esptool`.

## Provision

The same settings and the same script as the DHT22 demo (the CA and the
credentials of the dev application `oec-dev-app` from `oec/dev/generated`,
the Wi-Fi from the environment):

```sh
WIFI_SSID=<SSID> WIFI_PASS=<password> \
    ../esp32-dht22/provision.sh -p <serial port> -u mqtts://<ip>:8883
```

The display shows the progress (Wi-Fi, time) and then "waiting for climate
data" until the first `Climate` publication arrives; the serial log
(115200 Bd) shows the Identification Link. Before passing the board on,
erase it: `python -m esptool -p <serial port> erase-flash`.

## Check

As for the DHT22 demo: the OEC Registry lists the display, the config
service changes the Config (e.g. `Title`, `Brightness`, the scale), D7 runs against its
Identification Link (`oec-check test … --app <Identification Link> --level 3`).

Tested on an M5Stack Core2 (ESP32-D0WDQ6-V3) with an ESP32-S3 DHT22 demo
board, Arduino-ESP32 3.3.12, M5Unified 0.2.29, against `oec/dev`
(2026-11-28): the display shows the S3's climate, the View reports
`Sensors` 1; D7 L3 without the proxy: 38 pass, 0 fail, 17 skip
(the connection and restart tests need the proxy and the restart hooks).
With the extended Config (C-115, 2026-11-28): `Set` of the scale and the
brightness `Good` and in effect at once, the temperature and the marker in
the colours of the S3 display; D7 L3 without the proxy 38 pass, 0 fail,
17 skip.
With the graph (C-116, 2026-11-28): `Set` of `History` `Good` and in
effect at once; with a simulated second sensor (Climate from
`mosquitto_pub`) the page dots, button A, tap and swipe in both directions
change the sensor; D7 L3 without the proxy 38 pass, 0 fail, 17 skip.
