FROM --platform=$BUILDPLATFORM golang:1.23.3-alpine AS builder

ARG TARGETARCH

WORKDIR /app
COPY . ./
RUN go mod download
RUN go mod verify
RUN GOOS=linux GOARCH=$TARGETARCH go build -a -o output/main ./main.go

FROM alpine:latest
WORKDIR /app

COPY --from=builder /app/output/main .

# Run
ENTRYPOINT ["/app/main"]
