# Config service – D10 (Rust on `oec`)

A generic configuration tool for every OEC application with a `Config`
resource (ADR-0028 §7, A-304): it finds the applications on the bus, reads
their `Config`, shows which `Config` is writable and changes values with
`Set Config`, reporting the `Status` event of the target. Rust on the SDK
crate `oec` (`oec/sdk/rust`), ServiceType `Utility`, Messaging Level 1:
the service only sends requests and answers none.

Increment 4 (C-082) is the core with a small HTTP/JSON API, increment 5
(C-083) the Web UI on this API, increment 6 (C-084) the image and the
compose profile `demos` of `oec/dev` with an MQTT user `oec-config` of its
own: the broker ACL lets it publish `Set/Config` and no other request.

## Build and run

An own Cargo package outside the SDK workspace with its own `Cargo.lock`;
`oec` is a path dependency (`../../sdk/rust/oec`):

```sh
cd oec/demos/config
cargo build --offline --locked          # target/debug/oec-config
OEC_ROOT=<container storage root> target/debug/oec-config
```

The broker, the credentials and the identity come from the container
storage under `OEC_ROOT` (default `/`) through the SDK: `broker.json`,
`/etc/oi4/mqtt/…`, `/run/secrets/oi4` (REQ-CNT-003, GL-ADR-022). Run it on
a **copy** of an example storage, never on `oec/dev/generated/` itself.


### Image

`config/Dockerfile` builds `oi4/oec-config:0.1.0` for `linux/amd64` and
`linux/arm64` (distroless, non-root UID 65532, SBOM and license texts in
`/opt/oi4/licenses`; see [`../README.md`](../README.md#images-c-084)):

```sh
oec/demos/images/build.sh /tmp/oec-images   # all three demos, D8 oec-infra image + run
docker run --rm --name oec-config-<uuid> --hostname oec-config-<uuid> \
  -v <root>/etc/oi4/mqtt:/etc/oi4/mqtt:ro -v <root>/etc/oi4/certs:/etc/oi4/certs:ro \
  -v <root>/run/secrets/oi4:/run/secrets/oi4:ro -p 127.0.0.1:8783:8783 oi4/oec-config:0.1.0
```

The Web UI listens on `0.0.0.0:8783` in the container; publish it on
`127.0.0.1` only (A-305). With the broker of `oec/dev`: compose profile
`demos` ([`../../dev/README.md`](../../dev/README.md#compose-profile-demos-c-084)).
D8 `oec-infra image`/`run` judge the image at level 3, the Service
Container level of all three demos (A-377).

## Settings

Not on the bus, so no OEC `Config` of its own (A-354). An optional JSON file
`<OEC_ROOT>/etc/oi4/app/config-service.json` (or the path in
`OEC_CONFIG_FILE`, which must then exist):

```json
{
  "listen": "127.0.0.1:8783",
  "requestTimeoutSeconds": 5,
  "apps": ["Utility/oi4.org/OEC,20Logger/OEC-LOGGER/logger-1"],
  "setHistory": 50
}
```

| Key | Meaning | Default |
|---|---|---|
| `listen` | address of the Web UI and its API; `""` turns both off | `127.0.0.1:8783` |
| `requestTimeoutSeconds` | timeout of every request, 1 … 60 | 5 |
| `apps` | applications asked with `Get MAM` at every connect, as `<ServiceType>/<AppId>` | none |
| `setHistory` | results of `Set` kept for `/api/sets`, 1 … 1000 | 50 |

Unknown keys and invalid values stop the service with a message (exit ≠ 0),
also an address that cannot be bound (before the service connects).
Environment: `OEC_UI_LISTEN` overrides `listen` (empty: off; the container
of C-084 sets `0.0.0.0:8783`), `OEC_CONFIG_LOG` = `error`, `warn`,
`info` (default) or `debug` for the log on stderr, `OEC_UI_KEEPALIVE_MS`
(1 … 15000, for tests) the time between the keep-alive comments of the
event streams.

## Discovery

- every Birth message `Pub/MAM` of an application (REQ-LIFE-003,
  REQ-LIFE-013) and, at every connect, `Get MAM` to the applications of
  `apps` (A-355); after a reconnect of the service, every online
  application is read again;
- for a new or again online application: `Get Profile`, then `Get Config`
  and `Get SubscriptionList` if the Profile lists them;
- a `Config` is **writable** if the SubscriptionList lists its `Set` topic,
  also by a wildcard (SF-107, REQ-RES-SUBLIST-016);
- `Pub/Config` of any application updates the cached `Config`; a Close
  message or a Will marks the application offline (REQ-LIFE-011/012);
- the service never lists itself;
- **limits** (C-113 RC-1): every client of the bus can announce
  applications, so the service keeps at most 1000 applications, 256 KiB
  per application (MAM, Profile, `Config`s, SubscriptionList; estimated
  memory, a payload above it is not even parsed) and 64 MiB in total. An
  offline application makes room for a new one (the one offline longest
  first); without one, a new application is not listed and
  `POST /api/refresh` for it answers 503. What does not fit is not kept
  (a discovery notes it in `errors`); a warning in the log tells how many
  updates were not kept, at most once a minute.

## Web UI

Open `http://127.0.0.1:8783/` in a browser while the service runs
(A-304):

- **Applications:** the discovered applications with their state, Health
  and number of `Config`s, live; *Discover again* reads every online one
  again, *Read again* the chosen one.
- **Configs:** for the chosen application its identity (MAM), Profile and
  one form per `Config`, built from the definition in the `Config` itself:
  a select for `Validation.Values` and for Booleans, a number field with
  `Min`/`Max` for Numbers, `Length` as the maximum length, `Unit`,
  `Pattern`, `DefaultValue`, `Mandatory` and the description as hints. Only
  a **writable** `Config` (SF-107) can be edited; the others are shown read
  only.
- **Sensitive objects** stay masked: the field is a password field with the
  mask `********` as placeholder; left empty, the mask is sent back and the
  application keeps its value (GL-ADR-021); a new value is sent but never
  shown.
- **Send Set Config** sends the changed values and, for every changed
  group, the `Mandatory` objects with the value the form shows (C-113
  RD-2; the service adds a sensitive one as the mask) and shows the result of the `Status` event, e.g.
  `Good (0x00000000)` or `BadOutOfRange (0x803C0000)`, under the form. The
  form shows the new values once the application publishes its `Config`;
  while the user edits, a change from the bus does not overwrite the form
  but shows a notice.
- **Set results:** every `Set` of the service, newest first, live.

The UI checks no value itself: the application decides (A-356), the UI
shows its answer.

## API

JSON over HTTP/1.1, one request per connection (`Connection: close`); the
Web UI uses the same API (A-357, changed in C-083: `GET /` is the page and
`GET /api/events` is new):

| Request | Answer |
|---|---|
| `GET /`, `/app.css`, `/app.js` | the Web UI (embedded with `include_str!`, no external resource) |
| `GET /api/apps` | `{"apps":[{serviceType, app, state, health, healthScore, mam, profile, subscriptionList, configs:[{source, context, writable, dataSetWriterId, config}], errors, discoveredMs}]}` |
| `POST /api/refresh` | body empty or `{}`: discovery of every online application; `{"serviceType","app"}`: of one (unknown: `Get MAM` first, 404 without answer); answers like `/api/apps` |
| `POST /api/set` | body `{"serviceType","app","source","context","values":{"<group>":{"<object>":"<value>"}}}`; answers the recorded result |
| `GET /api/sets` | `{"sets":[…]}`, the newest first: `{id, timeMs, serviceType, app, source, context, values, status:{code, symbol, good}, error}` |
| `GET /api/events` | event stream (SSE): `{"type":"apps", "apps":[…], "connected":…}` (as `/api/apps`) first and on every change, `{"type":"set", "set":{…}}` (as an entry of `/api/sets`) for every `Set` result; a comment `keep-alive` after 15 s without an event |

A query (`?…`) is refused with 400: no route takes parameters.

`POST /api/set` checks only the structure (A-356): 400 for an unknown
group or object, a value that is not a string (REQ-RES-CONFIG-007) or no
value; 404 for an unknown application or `Config`; 409 if the application
is offline or the `Config` is not writable; 503 while not connected. The
target decides on the values (REQ-RES-CONFIG-023): its `Status` event comes
back as `status` with HTTP 200, also for a Bad status (e.g. `BadOutOfRange`,
`good: false`); no Status event within the timeout is 504 with `error`.
Every `Mandatory` object not in `values` is sent with its cached value
(REQ-RES-CONFIG-011) – a value any client of the bus can have published
in a `Config`, which is why the Web UI sends the ones it shows. A
`Sensitive` value is published as the mask `********` and goes back
unchanged as the mask, which keeps the secret (REQ-RES-CONFIG-025,
GL-ADR-021); a new secret is sent as given and is never shown – the
history shows the mask. An object once seen as `Sensitive` stays
`Sensitive` and masked as long as the service lists its application, also
if a later `Config` says otherwise (a warning in the log, C-113 RD-2): any
client of the bus can publish a `Config` under any AppId. After a Good
status the service reads the `Config` again.

The API is for loopback and the Web UI of the service (A-357): no
authentication, no TLS (a demo, A-305); it refuses a `Host` that is not
`localhost` or an IP address and an `Origin` other than its own (403), a
POST without `application/json` (415), `Transfer-Encoding` (501), bodies
above 64 KiB (413), heads above 8 KiB (431), slow requests (408), more
than 16 connections and more than 8 event streams (503). Every answer
carries `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`,
`Content-Security-Policy: default-src 'self'; frame-ancestors 'none'` and
`Referrer-Policy: no-referrer`.

## Tests

```sh
cargo test --offline --locked           # unit tests
```

The Go integration tests in `config_test.go` (package of the Go module
`oec/demos`) start the test broker, the logger (D4), the weather service
(D3) and the config service on loopback: discovery, read, `Set` of the
logger's `minEventLevel` (Good, arrives at the logger), a rejected `Set` of
the weather service (`met-no` with `pollInterval` 599 → `BadOutOfRange`,
A-348), the error answers, the request guards, invalid settings, the Web UI
over HTTP and SSE (`webui_test.go`: files, the events `apps` and `set` of a
Good and a refused `Set`, guards, limits, keep-alive, listen addresses), and
D7 `oec-check test --level 1` with a pinned list of passes. `OEC_CONFIG` is the
path of the binary (default `target/debug/oec-config`),
`OEC_CONFIG_D7_REPORT=<file>` keeps the D7 report. `oec/demos/check.sh` runs
fmt, clippy (pedantic, `-D warnings`), the unit tests, cargo-deny with the
`deny.toml` of the SDK and the integration tests.
