#!/usr/bin/env bash
# Builds the smart sensor demo (opt-in, not in the gate): needs ESP-IDF
# >= 6.0 (IDF_PATH, export.sh) and the OEC source tree around this folder
# (the component oecm of sdk/micro); fetches esp-mqtt from the ESP Component
# Registry on the first build (dependencies.lock).
#
#   build.sh [esp32|esp32s3 ...]   (default: both)
#
# Output: target/<variant>/ (oec_smart_sensor_dht22.bin, flash_args,
# sdkconfig); flash with
#   cd target/<variant> && python -m esptool -p <port> write-flash @flash_args
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
if [ -z "${IDF_PATH:-}" ] || ! command -v idf.py >/dev/null; then
    echo "build.sh: ESP-IDF not set up (. \$IDF_PATH/export.sh)" >&2
    exit 2
fi
if [ ! -d "$here/../../sdk/micro/port/esp-idf/oecm" ]; then
    echo "build.sh: the OEC Micro SDK is not at ../../sdk/micro (build in the OEC source tree)" >&2
    exit 2
fi
variants=("$@")
[ ${#variants[@]} -gt 0 ] || variants=(esp32 esp32s3)
for v in "${variants[@]}"; do
    case "$v" in
    esp32 | esp32s3) ;;
    *)
        echo "build.sh: unknown variant $v" >&2
        exit 2
        ;;
    esac
    b="$here/target/$v"
    mkdir -p "$b"
    echo "== $v → $b"
    idf.py -C "$here" -B "$b" -D SDKCONFIG="$b/sdkconfig" -D SDKCONFIG_DEFAULTS=sdkconfig.defaults \
        -D IDF_TARGET="$v" build
    idf.py -C "$here" -B "$b" -D SDKCONFIG="$b/sdkconfig" size
done
