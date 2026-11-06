#!/bin/sh
# Checks of the demo services of P6 (ADR-0028 §6), offline, no Docker:
#   logger (D4, C on oec-ffi, from C-079): liboec_ffi.a (release) and the
#   test broker go-testbroker of oec/sdk/rust; the logger with make (C17,
#   warnings are errors) normally and with AddressSanitizer + UBSan
#   (target/asan/); its C unit tests under the sanitizers; with GCC 14/15
#   (if installed) every source compiled once (make gcc-check, C-084);
#   weather (D3, Go on the Go SDK, from C-080): liboec_ffi.a for cgo in
#   oec/sdk/go/lib/<GOOS>_<GOARCH>/ (go run ./cmd/oec-buildlib, never
#   committed), the service built with -race (weather/target/);
#   config (D10, Rust on the SDK crate oec, from C-082; own Cargo package
#   with its own Cargo.lock, ADR-0028 §7): cargo fmt --check, clippy
#   (pedantic, warnings are errors), cargo test, cargo-deny with the
#   deny.toml of the SDK (licenses, advisories, bans, sources; ADR-0023),
#   the binary (config/target/debug/oec-config);
#   go (one module, standard library and the Go SDK of this workspace):
#   gofmt, go vet, go test -race – the cited REQ ids, the unit tests, the
#   integration tests of the logger (ASan build), of the weather service and
#   of the config service against the in-process TLS test broker and D7
#   `oec-check test` against them (--level 2 logger, --level 3 weather,
#   --level 1 config; oec-check of oec/conformance
#   built into a temporary directory and run, not imported: ADR-0006);
#   the SBOM generator of the images (internal/sbom, C-084) against its
#   reviewed fixtures and D8 `oec-infra image` on an OCI layout of its
#   output (oec-infra built like oec-check).
# Needs GNU make and cargo-deny. OEC_DEMOS_REQUIRE=1 (default here): a missing helper
# fails instead of skipping.
set -eu
root="$(cd "$(dirname "$0")" && pwd)"
rust="$root/../sdk/rust"
make="${MAKE:-make}"
case "$(uname -s)" in
Darwin) leaks=0 ;; # LeakSanitizer is not supported by Apple clang
*) leaks=1 ;;
esac
export ASAN_OPTIONS="${ASAN_OPTIONS:-detect_leaks=$leaks:halt_on_error=1}"
export UBSAN_OPTIONS="${UBSAN_OPTIONS:-print_stacktrace=1:halt_on_error=1}"

echo "demos: liboec_ffi.a, go-testbroker"
(cd "$rust" && cargo build --offline --locked --quiet --release -p oec-ffi &&
	cargo build --offline --locked --quiet -p oec-ffi --example go-testbroker)
target="$(cd "$rust" && cargo metadata --offline --locked --format-version 1 --no-deps | sed -n 's/.*"target_directory":"\([^"]*\)".*/\1/p')"

echo "demos/logger: make, make SANITIZE=1, unit tests (ASan/UBSan)"
cd "$root/logger"
"$make" -s OEC_LIB="$target/release/liboec_ffi.a"
"$make" -s OEC_LIB="$target/release/liboec_ffi.a" SANITIZE=1 all test
# GCC finds what the Linux image build (GCC 14) would refuse; optional
gcc="$(command -v gcc-15 || command -v gcc-14 || true)"
if [ -n "$gcc" ]; then
	echo "demos/logger: $(basename "$gcc") -O2 (warnings are errors)"
	"$make" -s gcc-check GCC="$gcc"
fi

echo "demos/weather: liboec_ffi.a for cgo, go build -race"
(cd "$root/../sdk/go" && go run ./cmd/oec-buildlib)
cd "$root"
go build -race -o "$root/weather/target/oec-weather" ./weather

echo "demos/config: cargo fmt, clippy, test, deny (deny.toml of the SDK), build"
cd "$root/config"
cargo fmt --check
cargo clippy --offline --locked --all-targets --quiet -- -D warnings
cargo test --offline --locked --quiet
command -v cargo-deny >/dev/null || { echo "cargo-deny missing: cargo install cargo-deny --locked" >&2; exit 1; }
cargo deny --log-level error --config "$rust/deny.toml" check || {
	echo "cargo deny: retrying with the cached advisory DB (no network?)" >&2
	cargo deny --log-level error --config "$rust/deny.toml" --frozen check
}
cargo build --offline --locked --quiet

echo "demos/go: gofmt, vet, oec-check, oec-infra, go test -race (logger D7 level 2, weather D7 level 3, config D7 level 1, SBOM D8 image)"
cd "$root"
unformatted="$(gofmt -l .)"
test -z "$unformatted" || { echo "gofmt needed: $unformatted" >&2; exit 1; }
go vet ./...
tmp="$(mktemp -d)"
(cd "$root/../conformance" && go build -o "$tmp/oec-check" ./cmd/oec-check &&
	go build -o "$tmp/oec-infra" ./cmd/oec-infra) || { rm -rf "$tmp"; exit 1; }
OEC_DEMOS_REQUIRE="${OEC_DEMOS_REQUIRE:-1}" OEC_TESTBROKER="$target/debug/examples/go-testbroker" \
	OEC_LOGGER="$root/logger/target/asan/oec-logger" OEC_WEATHER="$root/weather/target/oec-weather" \
	OEC_CONFIG="$root/config/target/debug/oec-config" OEC_D7="$tmp/oec-check" \
	OEC_D8="$tmp/oec-infra" go test -race -count=1 ./... || { rm -rf "$tmp"; exit 1; }
rm -rf "$tmp"
echo "demos: ok"
