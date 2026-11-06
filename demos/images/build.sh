#!/bin/sh
# Opt-in build of the demo images (ADR-0028 §4, C-084) with docker buildx;
# not part of the gate (needs Docker and network: base and builder images,
# crates, Debian packages). Creates only its own objects:
#
#   oec/demos/images/build.sh [<output dir>]   build, check with D8, load
#   oec/demos/images/build.sh --remove         remove the loaded images
#
# For every demo (logger, weather, config) it builds the image for
# linux/amd64 and linux/arm64 as an OCI archive <output dir>/oec-<demo>.oci.tar,
# runs D8 `oec-infra image` on it (reports next to it), loads the image
# of the native platform as oi4/oec-<demo>:$VERSION into Docker (the
# classic image store of Docker cannot load a multi-platform image) and runs
# D8 `oec-infra run` on it (sandbox containers oec-d8-run-…, removed by D8).
# D8 judges at level 3, the Service Container level of all three demos
# (ADR-0028); a D8 verdict other than pass makes the script fail. The
# builds run in a buildx builder of their own (driver docker-container),
# which is removed at the end with its cache; the base and builder images
# are pulled into that builder only. The builder runs BuildKit pinned by tag
# and digest (not the shared tag buildx-stable-1, which a new pull would move
# away from the image other builders of the host use); a BuildKit image the
# script pulled is removed at the end as well. Nothing is pruned.
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
oec="$(cd "$here/../.." && pwd)"
version="${VERSION:-0.1.0}"
demos="logger weather config"
# BuildKit v0.34.1 (Apache-2.0), multi-arch index digest of 2026-11-01
buildkit="docker.io/moby/buildkit:v0.34.1@sha256:99368f3e46177c6405b9740e4461d91cee004128daeecca78d4ceac50ec1da5c"

if [ "${1:-}" = "--remove" ]; then
	for d in $demos; do
		docker image rm "oi4/oec-$d:$version" 2>/dev/null || true
	done
	exit 0
fi

out="${1:-$(mktemp -d)}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
case "$(uname -m)" in
x86_64 | amd64) native=amd64 ;;
arm64 | aarch64) native=arm64 ;;
*) echo "unsupported host $(uname -m)" >&2; exit 1 ;;
esac
epoch="$(git -C "$oec" log -1 --format=%ct)"
builder="oec-demos-$$"
tmp="$(mktemp -d)"
had_buildkit=0
docker image inspect "$buildkit" >/dev/null 2>&1 && had_buildkit=1
cleanup() {
	docker buildx rm "$builder" >/dev/null 2>&1 || true
	if [ "$had_buildkit" = 0 ]; then
		id="$(docker image inspect -f '{{.Id}}' "$buildkit" 2>/dev/null || true)"
		[ -z "$id" ] || docker image rm "$id" >/dev/null 2>&1 || true
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT

(cd "$oec/conformance" && GOTOOLCHAIN=auto go build -o "$tmp/oec-infra" ./cmd/oec-infra)
docker buildx create --name "$builder" --driver docker-container --driver-opt "image=$buildkit" >/dev/null
d8=0
for d in $demos; do
	echo "== oec-$d: linux/amd64 + linux/arm64"
	start=$(date +%s)
	docker buildx build --builder "$builder" --progress plain -f "$oec/demos/$d/Dockerfile" \
		--platform linux/amd64,linux/arm64 --build-arg VERSION="$version" \
		--build-arg SOURCE_DATE_EPOCH="$epoch" \
		-o "type=oci,dest=$out/oec-$d.oci.tar" "$oec" >"$out/oec-$d.build.log" 2>&1 ||
		{ tail -40 "$out/oec-$d.build.log" >&2; exit 1; }
	echo "built in $(($(date +%s) - start)) s: $(du -h "$out/oec-$d.oci.tar" | cut -f1) $out/oec-$d.oci.tar"
	rc=0
	"$tmp/oec-infra" image --level 3 --report "json=$out/oec-$d.d8-image.json" \
		"$out/oec-$d.oci.tar" >"$out/oec-$d.d8-image.txt" || rc=$?
	echo "D8 oec-infra image --level 3: exit $rc ($out/oec-$d.d8-image.txt)"
	[ "$rc" = 0 ] || d8=1
	old="$(docker image inspect -f '{{.Id}}' "oi4/oec-$d:$version" 2>/dev/null || true)"
	docker buildx build --builder "$builder" --progress quiet -f "$oec/demos/$d/Dockerfile" \
		--platform "linux/$native" --build-arg VERSION="$version" \
		--build-arg SOURCE_DATE_EPOCH="$epoch" \
		-t "oi4/oec-$d:$version" --load "$oec" >/dev/null
	new="$(docker image inspect -f '{{.Id}}' "oi4/oec-$d:$version")"
	if [ -n "$old" ] && [ "$old" != "$new" ]; then
		# the image of an earlier run lost its tag; without -f it stays if it has another
		docker image rm "$old" >/dev/null 2>&1 || true
	fi
	echo "loaded oi4/oec-$d:$version (linux/$native): $(docker image inspect -f '{{.Size}}' "oi4/oec-$d:$version") bytes"
	rc=0
	"$tmp/oec-infra" run --level 3 --report "json=$out/oec-$d.d8-run.json" \
		"oi4/oec-$d:$version" >"$out/oec-$d.d8-run.txt" || rc=$?
	echo "D8 oec-infra run --level 3: exit $rc ($out/oec-$d.d8-run.txt)"
	[ "$rc" = 0 ] || d8=1
done
[ "$d8" = 0 ] || {
	echo "D8 did not pass (see the reports in $out); the loaded images stay for a look ($0 --remove)" >&2
	exit 1
}
echo "images ok: $out (remove the loaded images with $0 --remove)"
