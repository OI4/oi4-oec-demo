# OEC Logger (demo D4)

A small OEC application in C on the C ABI of the SDK (`oec-ffi`, ADR-0021):
it subscribes to the `Event`, `Health` and optionally `Data` publications of
**all** participants of the bus, writes one record per accepted
DataSetMessage to standard output (and optionally a file) and keeps the last
records in memory for its Web UI (since C-083).

| | |
|---|---|
| Identity | `oi4.org` / `OEC Logger` / `OEC-LOGGER`, SerialNumber = hostname |
| ServiceType | `Utility` |
| Messaging level | **L2** (own `Config` with `Get`/`Set`; D7 `test --level 2` in the gate) |
| Storage | container storage (REQ-CNT-004) below `OEC_ROOT` (default `/`) |
| Web UI | `http://127.0.0.1:8781/`, read only, localhost only, **no authentication, no TLS** (A-305) |

## Build and run

Needs a C17 compiler, GNU make and `liboec_ffi.a`
(`cargo build --release -p oec-ffi` in `oec/sdk/rust`):

```sh
cd oec/demos/logger
make                 # target/oec-logger
make test            # C unit tests of the core (no SDK needed)
make SANITIZE=1 all test   # the same with AddressSanitizer + UBSan in target/asan/
OEC_ROOT=/path/to/container-storage target/oec-logger
```

`OEC_INCLUDE` (directory of `oec.h`) and `OEC_LIB` (`liboec_ffi.a`) override
the defaults. The container storage holds `etc/hostname`,
`etc/oi4/mqtt/broker.json`, `etc/oi4/certs/ca.pem` and the credentials in
`run/secrets/oi4/` (RIG 4.1); run the logger on a **copy** of a storage, it
writes its store (Config values, RevisionCounter) to `etc/oi4/app`.
SIGTERM or SIGINT end it with the Close message. Diagnostics of the logger
itself go to standard error, errors also as a Syslog `Event`.


### Image

`logger/Dockerfile` builds `oi4/oec-logger:0.1.0` for `linux/amd64` and
`linux/arm64` (distroless, non-root UID 65532, SBOM and license texts in
`/opt/oi4/licenses`; see [`../README.md`](../README.md#images-c-084)):

```sh
oec/demos/images/build.sh /tmp/oec-images   # all three demos, D8 oec-infra image + run
docker run --rm --name oec-logger-<uuid> --hostname oec-logger-<uuid> \
  -v <root>/etc/oi4/mqtt:/etc/oi4/mqtt:ro -v <root>/etc/oi4/certs:/etc/oi4/certs:ro \
  -v <root>/run/secrets/oi4:/run/secrets/oi4:ro -p 127.0.0.1:8781:8781 oi4/oec-logger:0.1.0
```

The Web UI listens on `0.0.0.0:8781` in the container; publish it on
`127.0.0.1` only (A-305). With the broker of `oec/dev`: compose profile
`demos` ([`../../dev/README.md`](../../dev/README.md#compose-profile-demos-c-084)).
D8 `oec-infra image`/`run` judge the image at level 3, the Service
Container level of all three demos (A-377).

## Configuration

The `Config` resource of the logger (context `logger`, group `common`) is
read and changed over the bus with `Get`/`Set Config` (e.g. by the config
service D10 or `oec-probe`); values persist across restarts, `""` resets an
object to its default. A `Set` changes all values at once or nothing.

| Object | Type | Default | Meaning |
|---|---|---|---|
| `event` | Boolean | `true` | subscribe to the `Event` resource |
| `health` | Boolean | `true` | subscribe to the `Health` resource |
| `data` | Boolean | `false` | subscribe to the `Data` resource |
| `sources` | String (≤ 2048) | `""` | Identification Links (unencoded, separated by white space); a record is kept if its AppId or Source equals an entry or lies below it (prefix of whole levels, e.g. `vendor.com/Pump`); empty: all |
| `minEventLevel` | String | `Informational` | Events less severe than this Syslog level are dropped (`Emergency` … `Debug`) |
| `format` | String | `json` | `json` (one JSON object per line) or `text` |
| `file` | String (≤ 64) | `""` | file name in `/opt/oi4/app` the records are appended to as well (rotated to `<file>.1` at 10 MiB); empty: standard output only |
| `historySize` | Number 10 … 10000 | `1000` | records kept in memory for the Web UI |

The Event level is the Syslog severity: for `Syslog` events PRI % 8, for
`Status` events by the severity of `Number` (Good → Informational,
Uncertain → Warning, Bad → Error), otherwise Notice.

## Web UI

Open `http://127.0.0.1:8781/` in a browser while the logger runs. The page
shows the connection state, the settings now in force (read only), the last
records with filters (resource, Event level, text, number, pause) and small
SVG charts of the numeric values of `Data` and of the HealthScore; new
records arrive live by Server-Sent Events. The settings are changed only
through the OEC `Config` (e.g. with the config service D10); the UI has no
`POST`.

| Request | Answer |
|---|---|
| `GET /`, `/app.css`, `/app.js` | the page (embedded in the binary, no external resource) |
| `GET /api/status` | `{version, state, settings, records, lastSeq, series}` |
| `GET /api/records?limit&after&resource&level&q` | the last records (`limit` 1 … 10000, default 200), newest last; `after` = sequence number, `level` = Event level or more severe, `q` = part of the record |
| `GET /api/series` | the numeric series of the charts |
| `GET /api/events` | event stream: `status` first and when the settings or the state change, `record` for every new record, `reset` when records were missed; a comment `keep-alive` after 15 s without an event |

Environment: `OEC_UI_LISTEN` = `host:port` (default `127.0.0.1:8781`;
empty: no Web UI; the container of C-084 sets `0.0.0.0:8781`). An address
that cannot be bound stops the logger before it connects. The server is an
own minimal HTTP/1.1 server on POSIX sockets (`src/http.c`, no library, ADR-0028
amendment C-083): one request per connection, a thread per connection, at
most 16 connections and 8 event streams (503 beyond), head 8 KiB, body
64 KiB, 5 s per request. It refuses a `Host` that is not `localhost` or an
IP address and an `Origin` other than its own (403) – the same rules as the
other two demos.

## Records

JSON (one line each):

```json
{"time":"2026-11-01T11:50:34.689Z","resource":"Event","serviceType":"OTConnector","app":"vendor.com/Pump/P-3/0815","source":"vendor.com/Pump/P-3/0815","filter":"Syslog/Notice","level":"Notice","payload":{"Number":13,"Category":"CAT_SYSLOG_0","Details":{"MSG":"pump notice","HEADER":""}}}
```

`time` is the receive time (UTC), `app`, `source` and `filter` come from the
topic (Identification Links unencoded, `null` if absent), `level` only for
Events, `payload` is the DataSetMessage Payload. Text:

```text
2026-11-01T11:50:34.689Z Event Notice OTConnector vendor.com/Pump/P-3/0815 vendor.com/Pump/P-3/0815 Syslog/Notice {"Number":13,…}
```

The history keeps the last `historySize` records and, per resource,
application, source, filter and numeric field of `Data`/`Health`, the last
240 values (at most 256 series) for the charts of the Web UI.

## Tests

`oec/demos/check.sh` (a step of `oec/check.sh`) runs the C unit tests under
ASan/UBSan and the Go integration tests in this directory against the ASan
build on the in-process TLS test broker: defaults, records of other
applications, `Set Config` of every object, refused values, persistence
across a restart, the Web UI over HTTP and SSE (`webui_test.go`: files,
records and filters, live records, guards, limits, keep-alive, listen
addresses; `TestUIAssets` checks that `src/ui_assets.c` is current) and D7
`oec-check test --level 2` (46 pass, 9 skip). See [`../README.md`](../README.md).

After a change in `ui/`, regenerate the embedded files (from `oec/demos`):

```sh
go run ./internal/cembed/cmd/cembed -o logger/src/ui_assets.c logger/ui
```

## Code

| File | Content |
|---|---|
| `src/json.{h,c}` | small JSON reader (RFC 8259, UTF-8 checked, depth ≤ 64) and string writer – no C dependency |
| `src/core.{h,c}` | settings, Config definition, filter, Event level, records – free of `oec.h` |
| `src/history.{h,c}` | thread-safe ring buffer of records and numeric series |
| `src/http.{h,c}` | minimal HTTP/1.1 server on POSIX sockets (guards, limits, event streams) |
| `src/ui.{h,c}` | the routes of the Web UI and the event stream |
| `src/ui_assets.c` | the files of `ui/` as C arrays (generated by `internal/cembed`) |
| `ui/` | `index.html`, `app.css`, `app.js` of the Web UI |
| `src/main.c` | glue to the C ABI: app, Config, subscriptions, output, file, Web UI |
| `test/unit.c` | unit tests of the core |
