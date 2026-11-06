#!/bin/sh
# Writes the settings of the smart sensor demo into the NVS partition
# oecprov of a board (README) – for the broker of the local development
# environment oec/dev (opt-in; needs the Python of ESP-IDF: . export.sh).
#
#   WIFI_SSID=… WIFI_PASS=… provision.sh -p <serial port> -u mqtts://<host>:8883 \
#       [-d <oec/dev folder>] [-l <level 1…3>]
#
# The broker CA and the credentials of the dev application oec-dev-app come
# from <oec/dev>/generated (default: ../../dev); the Wi-Fi from the
# environment. The CSV and the image live in a private temporary folder
# that is removed at the end. The board restarts after flashing.
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
dev="$here/../../dev"
port="" uri="" level=3
while getopts p:u:d:l: o; do
    case "$o" in
    p) port="$OPTARG" ;;
    u) uri="$OPTARG" ;;
    d) dev="$OPTARG" ;;
    l) level="$OPTARG" ;;
    *) exit 2 ;;
    esac
done
usage="usage: WIFI_SSID=… WIFI_PASS=… provision.sh -p <serial port> -u mqtts://<host>:8883 [-d <oec/dev>] [-l 1|2|3]"
[ -n "$port" ] && [ -n "$uri" ] || { echo "$usage" >&2; exit 2; }
[ -n "${WIFI_SSID:-}" ] || { echo "provision.sh: WIFI_SSID is not set" >&2; exit 2; }
case "$uri" in mqtts://*) ;; *) echo "provision.sh: the broker needs mqtts:// (TLS only)" >&2; exit 2 ;; esac
case "$level" in 1 | 2 | 3) ;; *) echo "provision.sh: level 1, 2 or 3" >&2; exit 2 ;; esac
ca="$dev/generated/broker/ca.pem"
cred_file="$dev/generated/roots/oec-dev-app/run/secrets/oi4/credentials"
[ -f "$ca" ] && [ -f "$cred_file" ] || { echo "provision.sh: no $dev/generated (cd oec/dev && go run ./pki)" >&2; exit 2; }
cred="$(base64 -d <"$cred_file")"

# A CSV field in double quotes, inner quotes doubled.
q() { printf '"%s"' "$(printf '%s' "$1" | sed 's/"/""/g')"; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
chmod 700 "$tmp"
{
    echo "key,type,encoding,value"
    echo "oec,namespace,,"
    echo "uri,data,string,$(q "$uri")"
    echo "ca,file,string,$(q "$ca")"
    echo "user,data,string,$(q "${cred%%:*}")"
    echo "password,data,string,$(q "${cred#*:}")"
    echo "level,data,u8,$level"
    echo "wifi_ssid,data,string,$(q "$WIFI_SSID")"
    echo "wifi_pass,data,string,$(q "${WIFI_PASS:-}")"
} >"$tmp/oecprov.csv"
python -m esp_idf_nvs_partition_gen generate "$tmp/oecprov.csv" "$tmp/oecprov.bin" 0x6000 >/dev/null
python -m esptool -p "$port" write-flash 0x10000 "$tmp/oecprov.bin"
echo "provision.sh: $uri, level $level and the Wi-Fi written; the board restarts"
