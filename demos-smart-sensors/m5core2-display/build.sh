#!/usr/bin/env bash
# Builds the climate display demo for the M5Stack Core2 (opt-in, not in the
# gate) with the pinned Arduino toolchain of oec/ci/micro-arduino.sh and the
# Arduino library of the Micro SDK (sdk/micro/arduino/package.sh). Install
# the toolchain once (network, several GB below OECM_ARDUINO_HOME, default
# ~/.oec-arduino):
#
#   ../../ci/micro-arduino.sh 3.3
#   build.sh
#
# A warning in the sketch or in the library fails the build, as in the CI.
# Output: target/ (the bootloader, the partition table, the application and
# flash_args); flash with
#   cd target && python -m esptool -p <port> write-flash @flash_args
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
oec="$(cd "$here/../.." && pwd)"
if [ ! -x "$oec/sdk/micro/arduino/package.sh" ]; then
    echo "build.sh: the OEC Micro SDK is not at ../../sdk/micro (build in the OEC source tree)" >&2
    exit 2
fi
. "$oec/ci/versions.sh"
home="${OECM_ARDUINO_HOME:-$HOME/.oec-arduino}"
cli="$home/arduino-cli-$arduino_cli_version/arduino-cli"
a() { "$cli" --config-file "$home/arduino-cli.yaml" "$@"; }
if [ ! -x "$cli" ] || ! a core list 2>/dev/null | grep -q "^esp32:esp32 *$arduino_esp32_3_3 " ||
    ! a lib list 2>/dev/null | grep -q "^M5Unified *$m5unified_version "; then
    echo "build.sh: no Arduino toolchain in $home (run ../../ci/micro-arduino.sh 3.3)" >&2
    exit 2
fi
fqbn=esp32:esp32:m5stack_core2
out="$here/target"
rm -rf "$out"
mkdir -p "$out"
"$oec/sdk/micro/arduino/package.sh" "$out/lib" >/dev/null
log="$out/compile.log"
if ! a compile --fqbn "$fqbn" --warnings all --library "$out/lib/OECMicro" \
    --build-path "$out/build" "$here" >"$log" 2>&1; then
    cat "$log"
    exit 1
fi
own="$(grep 'warning:' "$log" | grep -E 'OECMicro/|/m5core2-display/' || true)"
if [ -n "$own" ]; then
    echo "$own"
    echo "build.sh: warnings in the sketch or the library" >&2
    exit 1
fi
grep -E '^(Sketch uses|Global variables)' "$log"
name=m5core2-display.ino
cp "$out/build/$name.bootloader.bin" "$out/build/$name.partitions.bin" "$out/build/$name.bin" "$out/"
cat >"$out/flash_args" <<ARGS
0x1000 $name.bootloader.bin
0x8000 $name.partitions.bin
0x20000 $name.bin
ARGS
echo "build.sh: $out (cd target && python -m esptool -p <port> write-flash @flash_args)"
