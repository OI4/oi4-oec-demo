# OEC Weather (demo D3)

A small OEC application in Go on the Go SDK (ADR-0027): an `OTConnector`
that represents weather stations as assets of its own. Each station publishes
its weather as the `Data` resource `Oi4Pv`, has a `Config` with its location,
and raises the Generic events `Frost` and `Storm`. A provider supplies the
weather: `simulation` is offline and deterministic, `met-no` reads the
Locationforecast of MET Norway (ADR-0010, since C-081). A small Web UI shows
the stations (since C-083).

Weather data of the provider `met-no`: **Based on data from MET Norway**
(Locationforecast 2.0, [api.met.no](https://api.met.no/)), licensed under
[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/) (and NLOD 2.0).
Changed: the forecast of the hour nearest to the poll time, mapped to
`Oi4Pv` and rounded. MET Norway does not endorse this service.

| | |
|---|---|
| Identity | `oi4.org` / `OEC Weather` / `OEC-WEATHER`, SerialNumber = hostname (container name, REQ-CNT-021) |
| Stations | `oi4.org` / `OEC Weather Station` / `OEC-WEATHER-STATION`, SerialNumber `<hostname>-station-<n>`, n = 1 … 5 |
| ServiceType | `OTConnector` |
| Messaging level | **L3** (`Get`/`Set` of every resource of the application and of each station; D7 `test --level 3` in the gate) |
| Storage | container storage (REQ-CNT-004) below `OEC_ROOT` (default `/`) |
| Web UI | `http://127.0.0.1:8782/`, read only, localhost only, **no authentication, no TLS** (A-305) |

## Build and run

The Go SDK links `liboec_ffi.a` with cgo. Put it in place once with
`go run ./cmd/oec-buildlib` in `oec/sdk/go`; it is never committed. Then:

```sh
cd oec/demos
go build -o weather/target/oec-weather ./weather
OEC_ROOT=/path/to/container-storage weather/target/oec-weather
```

The container storage holds `etc/hostname`, `etc/oi4/mqtt/broker.json`,
`etc/oi4/certs/ca.pem` and the credentials in `run/secrets/oi4/` (RIG 4.1).
Run the service on a **copy** of a storage, because it writes its store
(Config values, RevisionCounter) to `etc/oi4/app`. SIGTERM or SIGINT end it
with the Close message.

Standard output gets one line per change and per poll round, which the tests
read:

```text
config provider=simulation stations=3 pollInterval=1m0s
station 1 Frankfurt am Main at 50.1109, 8.6821, 112 m added
running 0.1.0
event station 3 Frost begins
station 2 Oslo failed: met-no: 500 Internal Server Error
health OFF_SPEC_3 66
poll 3 stations
station 2 Oslo recovered
health NORMAL_0 100
poll 3 stations
```


### Image

`weather/Dockerfile` builds `oi4/oec-weather:0.1.0` for `linux/amd64` and
`linux/arm64` (distroless, non-root UID 65532, SBOM and license texts in
`/opt/oi4/licenses`; see [`../README.md`](../README.md#images-c-084)):

```sh
oec/demos/images/build.sh /tmp/oec-images   # all three demos, D8 oec-infra image + run
docker run --rm --name oec-weather-<uuid> --hostname oec-weather-<uuid> \
  -v <root>/etc/oi4/mqtt:/etc/oi4/mqtt:ro -v <root>/etc/oi4/certs:/etc/oi4/certs:ro \
  -v <root>/run/secrets/oi4:/run/secrets/oi4:ro -p 127.0.0.1:8782:8782 oi4/oec-weather:0.1.0
```

The Web UI listens on `0.0.0.0:8782` in the container; publish it on
`127.0.0.1` only (A-305). With the broker of `oec/dev`: compose profile
`demos` ([`../../dev/README.md`](../../dev/README.md#compose-profile-demos-c-084)).
D8 `oec-infra image`/`run` judge the image at level 3, the Service
Container level of all three demos (A-377).

## Web UI

Open `http://127.0.0.1:8782/` in a browser while the service runs. The page
shows the connection state, the Health of the service, the provider and the
settings (read only), and per station its location, Health, the current
values (temperature, humidity, pressure, wind), the Frost/Storm warnings, an
error of the provider, and an SVG line of the last 60 temperature and wind
values. While `met-no` is the provider, the page shows the MET Norway credit
("Based on data from MET Norway", CC BY 4.0) as a banner and in the footer
(A-351). The settings are changed only through the OEC `Config` (e.g. with
the config service D10); the UI has no `POST`.

| Request | Answer |
|---|---|
| `GET /`, `/app.css`, `/app.js` | the page (embedded with `embed`, no external resource) |
| `GET /api/state` | `{state, version, provider, pollIntervalSeconds, frostThreshold, stormThreshold, health, healthScore, attribution, stations:[{index, name, latitude, longitude, altitude, serialNumber, health, healthScore, failed, error, frost, storm, observation, history}], updated}`; `attribution` is `null` unless `met-no` is active |
| `GET /api/events` | event stream: `{"type":"state", …}` (as `/api/state`) first, after every poll or change and when the connection state changes; a comment `keep-alive` after 15 s without an event |

Environment: `OEC_UI_LISTEN` = `host:port` (default `127.0.0.1:8782`;
empty: no Web UI; the container of C-084 sets `0.0.0.0:8782`). An address
that cannot be bound stops the service before it connects. The server is
`net/http` with the same rules as the other two demos: one request per
connection, at most 16 connections and 8 event streams (503 beyond), head
8 KiB, body 64 KiB, 5 s per request (then `net/http` closes the connection
without an answer), a `Host` that is `localhost` or an IP
address and no `Origin` of another site (else 403).

## Configuration

Both `Config` resources (group `common`) are read and changed over the bus
with `Get`/`Set Config`, e.g. by the config service D10 or `oec-probe`.
Values persist across restarts, and `""` resets an object to its default. A
`Set` changes all values at once or nothing: an invalid value is refused
with Event `Status` Bad. An accepted `Set` takes effect in an immediate poll
round, also a change of the provider.

Application (context `weather`):

| Object | Type | Default | Meaning |
|---|---|---|---|
| `provider` | String, one of `simulation`, `met-no` | `simulation` | the weather data provider |
| `userAgentContact` | String (≤ 256, printable ASCII without spaces) | `""` | e-mail address or URL of the operator in the User-Agent; required by `met-no` |
| `pollInterval` | Number 1 … 86400, whole | `60` s | time between two observations of all stations; at least 600 s with `met-no` |
| `stations` | Number 1 … 5, whole | `3` | number of stations; new ones appear, removed ones leave the `PublicationList` |
| `frostThreshold` | Number −50 … 50 | `0` °C | a temperature at or below it raises `Frost` |
| `stormThreshold` | Number 0 … 100 | `20.8` m/s | a wind speed at or above it (Beaufort 9) raises `Storm` |
| `simulationSeed` | Number 0 … 999999999, whole | `1` | seed of the simulated weather |

Station n (context `station`):

| Object | Type | Default (n = 1 … 5) | Meaning |
|---|---|---|---|
| `name` | String (≤ 64) | Frankfurt am Main, Oslo, Zugspitze, Hamburg, Munich | name of the location |
| `latitude` | Number −90 … 90, ≤ 4 decimals | 50.1109, 59.9139, 47.4211, 53.5511, 48.1374 | WGS 84, north positive (°) |
| `longitude` | Number −180 … 180, ≤ 4 decimals | 8.6821, 10.7522, 10.9853, 9.9937, 11.5755 | WGS 84, east positive (°) |
| `altitude` | Number −500 … 9000, whole | 112, 23, 2962, 6, 519 | height above sea level (m) |

A station that is removed and added again keeps its stored `Config`.

Rules across objects (A-348): a `Set` with `provider` `met-no` is refused
with `BadInvalidArgument` without a `userAgentContact` and with
`BadOutOfRange` with a `pollInterval` below 600 s. The service never clamps
a value, so the `Config` shows what it does. A stored `Config` that breaks
these rules (e.g. written by an older version) starts with `simulation` and
a note on standard error.

## Data and events

`Data` `Oi4Pv` of each station. Its `Metadata` comes from the field
definitions below. It is published self-initiated on a change of at least
the Precision, and consumers may change Mode and Interval with
`Set PublicationList`.

| Field | Quantity | Unit | Range | Precision |
|---|---|---|---|---|
| `Pv` | air temperature | °C | −90 … 60 | 0.1 |
| `Sv1` | relative humidity | % | 0 … 100 | 1 |
| `Sv2` | air pressure at sea level | hPa | 870 … 1085 | 0.1 |
| `Sv3` | wind speed | m/s | 0 … 75 | 0.1 |
| `Sv4` | wind direction (from, clockwise from north) | ° | 0 … 360 | 1 |

Warnings are Generic events of the station (`Pub/Event/<station>/Generic/Frost`
or `…/Storm`). `Number` 1 means the warning begins and 0 means it ends.
`Details` carry the value and the threshold. A warning is evaluated only
while connected, so a change during a connection loss is reported after the
reconnect.

## Health

The service publishes the Health of the application and of each station
(A-349). The order of priority of MG 7.5 (SF-096) applies to each asset of
its own.

| Situation | Station | Application |
|---|---|---|
| every station has data | `NORMAL_0` / 100 | `NORMAL_0` / 100 |
| k of n stations without data | `FAILURE_1` / 0 for each of them | `OFF_SPEC_3` / 100·(n−k)/n (rounded down) |

A station without data (the provider failed, timed out, sent a broken
document, or was throttled) gets no `Data` update. The service never
invents values, and its last values stay readable with `Get` while its
Health marks them invalid. Each change is a `Syslog` event of the
application: `Warning` when a station fails and `Notice` when it recovers.
The application never reports `FAILURE_1` about itself, because it stays
available (REQ-LIFE-012/017).

## MET Norway (`met-no`)

The provider follows the [terms of service](https://api.met.no/doc/TermsOfService)
of api.met.no (ADR-0010, A-347):

- **User-Agent** `oec-weather/<version> <userAgentContact>`. Without a
  contact the service sends no request.
- **Caching**: no request before `Expires`. After that it sends a
  conditional request with `If-Modified-Since` (the `Last-Modified` of the
  cached document), and a `304` renews `Expires`. A response without
  `Expires` counts as expired. At most 16 documents are cached.
- **Coordinates** with at most 4 decimals and the altitude in whole metres
  (`lat`, `lon`, `altitude` of the compact product).
- **No bursts**: at least 1 s between two requests, so a poll round spreads
  the stations. The minimum poll interval is 600 s.
- **429**: no request for `Retry-After`, but at least 10 min. **403**: the
  error names the User-Agent. **203** (deprecated product): one `Syslog`
  Warning.
- Timeout of 30 s per request and at most 4 MiB per response. The units of
  the document are checked, and every value must lie in the Range of its
  field.

Mapping to `Oi4Pv` (A-343): `air_temperature` → `Pv`, `relative_humidity`
→ `Sv1`, `air_pressure_at_sea_level` → `Sv2`, `wind_speed` → `Sv3`,
`wind_from_direction` → `Sv4`, from the instant values of the forecast
entry nearest to the poll time (at most 1 h away).

`OEC_WEATHER_METNO_URL` replaces the endpoint for tests (a fake server). It
is deliberately no part of the `Config` (A-350).

## Simulation

The provider `simulation` needs no network. Its weather is a pure function of
the seed, the coordinates, the altitude and the time:

- a climate by latitude (warmer at the equator);
- a seasonal cycle that is reversed in the southern hemisphere;
- a diurnal cycle with its maximum at 15:00 local solar time;
- the standard lapse rate of 6.5 K/km;
- smooth value noise per quantity on a slow scale (one to one and a half
  days) and a fast scale (one to three hours).

The same inputs always give the same values. The station name has no
influence.

## Tests

`oec/demos/check.sh` (a step of `oec/check.sh`) builds the service with
`-race` and runs the tests in this directory:

- unit tests for the simulation (determinism, ranges, climate, smoothness),
  the settings and the Config definitions;
- unit tests for `met-no` against an `httptest` server: User-Agent, query,
  mapping, caching with `304`, missing `Expires`, 4xx/5xx, 403, 429 with
  `Retry-After`, 203, timeout, broken JSON, wrong units, out-of-range values,
  spacing, cache bound, body limit;
- integration tests against the in-process TLS test broker:
  - defaults;
  - adding and removing stations;
  - the poll interval;
  - `Set Config` of a station, and refused values;
  - Frost/Storm events;
  - persistence across a restart;
  - `met-no` against a fake server: refused `Set`s, the switch at runtime,
    User-Agent, spacing and query, a failing station and its recovery,
    every station failing, and the switch back;
- the Web UI over HTTP and SSE (`webui_test.go`: files, state and events,
  guards, limits, keep-alive, listen addresses; the MET Norway credit in
  `TestMetNoProvider` appears with `met-no` and disappears after the switch
  back);
- D7 `oec-check test --level 3`: 49 pass, 6 skip.

The live test against api.met.no is opt-in and never part of the gate:

```sh
cd oec/demos
OEC_WEATHER_LIVE_CONTACT=<your e-mail address or URL> go test -run TestMetNoLive -v ./weather
```

See [`../README.md`](../README.md).

## Code

| File | Content |
|---|---|
| `provider.go` | `Location`, `Observation`, the `Provider` interface, the quantities of `Oi4Pv` |
| `simulation.go` | the offline provider `simulation` |
| `metno.go` | the provider `met-no` (MET Norway Locationforecast 2.0) |
| `settings.go` | the Config definitions, their parsing and checks, the identity helpers |
| `service.go` | the stations and the poll goroutine (applies accepted Configs, polls, publishes, sets the Health, raises warnings) |
| `ui.go`, `ui/` | the Web UI: server, guards, snapshot, event stream; `index.html`, `app.css`, `app.js` |
| `main.go` | the app: identity, start, Web UI, signals |
