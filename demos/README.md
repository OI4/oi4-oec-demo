# OEC demo services (P6)

Three small services show the OEC SDKs at work (ADR-0028). Guide:
[Demo services](https://oi4.github.io/oi4-oec-documentation/docs/community/demos) in the Community section of the guideline
site.

| Demo | Language, SDK | ServiceType | Messaging level | State |
|---|---|---|---|---|
| [`logger/`](logger/README.md) – D4 | C17 on the C ABI (`oec-ffi`) | `Utility` | L2 | core since C-079; Web UI since C-083 (port 8781) |
| [`weather/`](weather/README.md) – D3 | Go on the Go SDK | `OTConnector` | L3 | core with the offline provider `simulation` and warnings since C-080; provider `met-no` (MET Norway, CC BY 4.0) and Health since C-081; Web UI since C-083 (port 8782) |
| [`config/`](config/README.md) – D10 | Rust on `oec` | `Utility` | L1 | core since C-082: discovery, read, writable `Config` (SF-107), `Set` with `Status` event, HTTP/JSON API; Web UI since C-083 (port 8783) |

## Web UIs

Each demo serves a small Web UI itself (C-083, ADR-0028 §3 and amendment
C-083): static HTML/CSS/JS embedded in the binary (no build step, no npm, no
CDN, nothing loaded from elsewhere), JSON under `/api/`, live updates by
Server-Sent Events (`GET /api/events`). Open them in a browser while the
demo runs:

| Demo | Address | What it shows |
|---|---|---|
| logger | `http://127.0.0.1:8781/` | the last records with filters, live; charts of numeric values; settings read only |
| weather | `http://127.0.0.1:8782/` | the stations with values, Health, warnings and a short SVG history; the MET Norway credit while `met-no` is active |
| config service | `http://127.0.0.1:8783/` | the applications and a form per `Config`; `Set Config` of writable ones with the `Status` result |

**No authentication and no TLS** – a demo, not a product (A-305). The
default address is loopback; `OEC_UI_LISTEN=host:port` changes it, empty
turns the UI off. All three servers apply the same rules: a `Host` that is
`localhost` or an IP address and no `Origin` of another site (else 403),
JSON only for `POST` (415), no `Transfer-Encoding` (501), head 8 KiB (431),
body 64 KiB (413), 5 s per request (408; the weather service on `net/http`
closes the connection), one request per connection, at
most 16 connections and 8 event streams (503), `Cache-Control: no-store`,
`X-Content-Type-Options: nosniff`, a `Content-Security-Policy` of
`default-src 'self'` and `Referrer-Policy: no-referrer`. In the images the
UIs listen on `0.0.0.0:<port>`; the compose profile `demos` of `oec/dev`
publishes them on `127.0.0.1` only.

## Images (C-084)

One `Dockerfile` per demo (`logger/`, `weather/`, `config/`; ADR-0028 §4 and
amendment C-084), build context `oec/` (allowlist `oec/.dockerignore`):

| | |
|---|---|
| Names | `oi4/oec-logger`, `oi4/oec-weather`, `oi4/oec-config`, tag `0.1.0`; local only (publication: P9) |
| Platforms | `linux/amd64` and `linux/arm64`, cross-built on the build platform (`docker buildx`) |
| Builders | `rust:1.89-trixie`, `golang:1.27.3-trixie`, pinned by tag and digest |
| Runtime | `gcr.io/distroless/cc-debian13:nonroot` (glibc 2.41, libgcc, no shell, no package manager), pinned by tag and digest |
| User | `65532:65532` (non-root); `ENTRYPOINT` is the binary `/usr/local/bin/oec-<demo>` |
| Volumes | `/etc/oi4/app`, `/opt/oi4/app` (owned by 65532); the platform mounts `/etc/oi4/mqtt`, `/etc/oi4/certs`, `/run/secrets/oi4` |
| Web UI | `OEC_UI_LISTEN=0.0.0.0:8781` / `8782` / `8783`, `EXPOSE` the port |
| Licenses | `/opt/oi4/licenses`: `sbom.spdx.json` (SPDX 2.3 JSON) and the text of every license and exception (RIG 4.1.4, SF-114) |
| Labels | OCI `title`, `description`, `version`, `licenses`, `vendor`, `source`, `base.name` |

No secrets, keys or certificates are in an image; the storages come from the
platform at run time.

```sh
oec/demos/images/build.sh /tmp/oec-images   # both platforms, D8 image + run, load the native one
oec/demos/images/build.sh --remove          # remove the loaded images
```

`build.sh` (opt-in, needs Docker and network) builds each image for both
platforms as an OCI archive `/tmp/oec-images/oec-<demo>.oci.tar`, checks
it with D8 `oec-infra image --level 3`, loads the image of the native
platform as `oi4/oec-<demo>:0.1.0` (the classic image store of Docker
cannot load a multi-platform image) and checks it with D8 `oec-infra run
--level 3` (reports `oec-<demo>.d8-*.{json,txt}` next to the archive). Level 3
is the Service Container level of all three demos (ADR-0028, A-377 of
C-085); a D8 verdict other than pass makes the script exit 1. A rebuild
removes the image it replaces (by ID). It uses a buildx builder of its own with BuildKit pinned by tag and digest and
removes both at the end; it never prunes. Run an image with a container
storage, e.g. `docker run --rm -v <root>/etc/oi4/mqtt:/etc/oi4/mqtt:ro -v
<root>/etc/oi4/certs:/etc/oi4/certs:ro -v <root>/run/secrets/oi4:/run/secrets/oi4:ro
--hostname <name> --name <name> -p 127.0.0.1:8781:8781 oi4/oec-logger:0.1.0`,
or all three with the broker through the compose profile `demos`
([`../dev/README.md`](../dev/README.md#compose-profile-demos-c-084)).

**SBOM and license texts** come from `internal/sbom` (`go run
./internal/sbom/cmd/oec-sbom`, standard library only), which the Dockerfiles
run in the build stage: the Rust crates of `cargo tree` (normal edges, no
build or proc-macro dependencies), the Go modules of `go version -m`, the
Debian packages of the base image (`/var/lib/dpkg/status.d` and the DEP-5
copyright files) mapped to SPDX by [`images/debian.json`](images/debian.json)
(D-1 of C-084), and the texts of [`images/licenses/`](images/licenses)
(SPDX license-list-data v3.29.0) or of the Debian copyright files for
`LicenseRef-*`. Every license is named unambiguously and every text is
present, or the tool fails. Go modules from outside this workspace need
`-golicenses <file>`, a reviewed map `module@version` → SPDX expression
(another version is refused): the image of the compliance tools uses
[`../conformance/images/go-licenses.json`](../conformance/images/go-licenses.json)
(C-107); the demos link no such module. For the deliverables without an
image (C-108, [`../ci/sbom.sh`](../ci/sbom.sh)) `-base`/`-debian` are left
out (only together), `-golist <file>` reads the Go modules from `go list
-deps` (no build; the format is in `GoList`), `-gostd <go version>` adds the
Go standard library of a binary and `-report <file>` writes the license
report `LICENSES.md` (the licenses with their number of components and text
file, then every component with its purl). Its tests run offline in the gate (fixtures and
golden SBOM in `internal/sbom/testdata/`, D8 `oec-infra image` on a
synthetic OCI layout).

## Layout

```text
oec/demos/
├── check.sh             gate step (called by oec/check.sh)
├── go.mod               one Go module: standard library and the Go SDK (replace ../sdk/go)
├── internal/demotest/   test harness: test broker, container storage, process, D7, Web UI checks
├── internal/cembed/     generator of the embedded UI files of the logger (C arrays)
├── internal/sbom/       SBOM (SPDX 2.3 JSON) and license texts of the images
├── images/              build.sh, Debian license map, SPDX license texts
├── images_test.go       Dockerfiles, .dockerignore and build.sh rules (offline)
├── reqs_test.go         every REQ id cited here exists and is active
├── config/              D10: own Cargo package (src/ in Rust, Cargo.lock), Go integration tests
├── logger/              D4: src/ and test/ in C, Makefile, Go integration tests
└── weather/             D3: package main in Go with unit and integration tests
```

## Tests

Everything runs offline, on loopback, without Docker (the image builds are
opt-in, see above):

```sh
oec/demos/check.sh
```

builds `liboec_ffi.a` (release) and the test broker `go-testbroker` of
`oec/sdk/rust`, puts `liboec_ffi.a` into `oec/sdk/go/lib/<GOOS>_<GOARCH>/`
for cgo (`go run ./cmd/oec-buildlib`, never committed), builds the demos (the
logger also with AddressSanitizer + UBSan, the weather service with `-race`),
runs the C unit tests, checks the config service with `cargo fmt`, clippy,
its unit tests and cargo-deny (`deny.toml` of the SDK) and builds it, then `go test -race ./...`: the integration tests of
every demo against the in-process TLS test broker and D7
(`oec-check test`, built from `oec/conformance` and run as a program) at the
demo's level, and the HTTP/SSE tests of the three Web UIs with the Go
client (no browser). The tests can also run alone; a missing helper skips
them:

```sh
cd oec/demos
OEC_LOGGER=$PWD/logger/target/oec-logger \
OEC_WEATHER=$PWD/weather/target/oec-weather \
OEC_CONFIG=$PWD/config/target/debug/oec-config \
OEC_TESTBROKER=<cargo target>/debug/examples/go-testbroker \
OEC_D7=<path of oec-check> go test ./...
```

`OEC_DEMOS_REQUIRE=1` turns a missing helper into a failure (set by
`check.sh`). `OEC_LOGGER_D7_REPORT=<file>`, `OEC_WEATHER_D7_REPORT=<file>` and
`OEC_CONFIG_D7_REPORT=<file>` keep the D7 report of the logger, of the
weather service and of the config service.

The weather tests of the provider `met-no` run against a fake server of
their own and never reach the network. A live test against api.met.no is
opt-in and never part of `check.sh`: set
`OEC_WEATHER_LIVE_CONTACT=<your e-mail address or URL>` (see
[`weather/README.md`](weather/README.md#tests)).
