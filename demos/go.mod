module github.com/OI4/oec/demos

go 1.26.0

toolchain go1.27.3

require github.com/OI4/oec/sdk/go v0.0.0

// The Go SDK of this workspace (ADR-0027, ADR-0028); cgo links
// sdk/go/lib/<GOOS>_<GOARCH>/liboec_ffi.a (go run ./cmd/oec-buildlib).
replace github.com/OI4/oec/sdk/go => ../sdk/go
