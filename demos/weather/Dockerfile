# syntax=docker/dockerfile:1
# Image of the weather service D3 (ADR-0028 §4, C-084): oi4/oec-weather.
# Build context: oec/ (see oec/.dockerignore), for linux/amd64 and
# linux/arm64 with docker buildx (oec/demos/images/build.sh):
#
#   docker buildx build -f demos/weather/Dockerfile --platform linux/arm64 \
#     -t oi4/oec-weather:0.1.0 --load oec
#
# The build stage runs on the build platform and cross-compiles (Debian
# cross toolchains, no emulation); the final stage has no RUN. The images
# are pinned by tag and digest (multi-platform index).
ARG RUST_IMAGE=docker.io/library/rust:1.89-trixie@sha256:57407b378b2b6e07b48a6135a20c87cc22ea6e249c0acf6cb1833ead3cf116e9
ARG GO_IMAGE=docker.io/library/golang:1.27.3-trixie@sha256:c65ecfe613b539ac74c62a4c3b2c27e8f8a6076384c8f27e8a3c0f3be17cc385
ARG BASE_IMAGE=gcr.io/distroless/cc-debian13:nonroot@sha256:08476c244888bfb425bae1ae4dd7c17f093e972aab8426883d97f3c6f280f7c1

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS go

# the runtime base of the target platform: its package database feeds the SBOM
FROM ${BASE_IMAGE} AS base

FROM --platform=$BUILDPLATFORM ${RUST_IMAGE} AS build
ARG TARGETARCH
ARG VERSION=0.1.0
ARG SOURCE_DATE_EPOCH
COPY --from=go /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local GOFLAGS=-buildvcs=false \
	CARGO_TARGET_DIR=/tmp/target \
	CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_LINKER=x86_64-linux-gnu-gcc \
	CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_LINKER=aarch64-linux-gnu-gcc \
	CC_x86_64_unknown_linux_gnu=x86_64-linux-gnu-gcc \
	CC_aarch64_unknown_linux_gnu=aarch64-linux-gnu-gcc
RUN case "$TARGETARCH" in amd64) cpu=x86_64 ;; arm64) cpu=aarch64 ;; *) echo "unsupported: $TARGETARCH" >&2; exit 1 ;; esac && \
	echo "$cpu" >/tmp/cpu && \
	apt-get update -qq && \
	apt-get install -y -qq --no-install-recommends "gcc-$(echo $cpu | tr _ -)-linux-gnu" "libc6-dev-$TARGETARCH-cross" >/dev/null && \
	rm -rf /var/lib/apt/lists/* && \
	rustup target add "$cpu-unknown-linux-gnu"
COPY --from=base /var/lib/dpkg/status.d /base/var/lib/dpkg/status.d
COPY --from=base /usr/share/doc /base/usr/share/doc
COPY --from=base /usr/lib/os-release /base/usr/lib/os-release
COPY schema /src/schema
COPY sdk/rust /src/sdk/rust
COPY sdk/go /src/sdk/go
COPY demos /src/demos
WORKDIR /src
RUN cpu="$(cat /tmp/cpu)" && t="$cpu-unknown-linux-gnu" && \
	(cd sdk/go && go run ./cmd/oec-buildlib -target "$t") && \
	cd demos && \
	CGO_ENABLED=1 GOOS=linux GOARCH="$TARGETARCH" CC="$cpu-linux-gnu-gcc" \
		go build -trimpath -ldflags='-s -w' -o /out/oec-weather ./weather && \
	mkdir -p /out/app && \
	go version -m /out/oec-weather >/tmp/go-version.txt && \
	cargo tree --locked --manifest-path ../sdk/rust/Cargo.toml -p oec-ffi --target "$t" \
		--edges normal,no-proc-macro --prefix none --format '{p}|{l}' >/tmp/cargo-tree.txt && \
	go run ./internal/sbom/cmd/oec-sbom -name oec-weather -version "$VERSION" \
		-cargo /tmp/cargo-tree.txt -go /tmp/go-version.txt -rust "$(rustc --version | cut -d' ' -f2)" \
		-base /base -debian images/debian.json -texts images/licenses -out /out/licenses

FROM ${BASE_IMAGE}
ARG VERSION=0.1.0
LABEL org.opencontainers.image.title="oec-weather" \
	org.opencontainers.image.description="OI4 OEC v2.0 demo D3: weather service (Go on the OEC Go SDK)" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.licenses="MIT" \
	org.opencontainers.image.vendor="OI4 Alliance" \
	org.opencontainers.image.source="https://github.com/OI4/oec" \
	org.opencontainers.image.base.name="gcr.io/distroless/cc-debian13:nonroot"
COPY --from=build /out/oec-weather /usr/local/bin/oec-weather
COPY --from=build /out/licenses/ /opt/oi4/licenses/
COPY --from=build --chown=65532:65532 /out/app/ /etc/oi4/app/
COPY --from=build --chown=65532:65532 /out/app/ /opt/oi4/app/
VOLUME ["/etc/oi4/app", "/opt/oi4/app"]
ENV OEC_UI_LISTEN=0.0.0.0:8782
EXPOSE 8782
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/oec-weather"]
