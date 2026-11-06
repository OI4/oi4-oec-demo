FROM --platform=$BUILDPLATFORM golang:1.24.2-alpine AS builder

ARG TARGETARCH

WORKDIR /app
COPY . ./
RUN go mod download
RUN go mod verify
RUN GOOS=linux GOARCH=$TARGETARCH go build -a -o output/main ./main.go

FROM alpine:latest
WORKDIR /app

COPY --from=builder /app/output/main .
RUN mkdir -p /etc/oi4/app
RUN mkdir -p /opt/oi4/app

# Run
ENTRYPOINT ["/app/main"]
