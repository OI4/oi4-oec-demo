package sbom

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// The npm packages of a bundled Web UI (amendment C-090 of ADR-0028): the
// non-dev packages of the lockfile, nested ones included, without the root
// package and the build tools; scoped names get %40 in the purl.
func TestNpm(t *testing.T) {
	f, err := os.Open("testdata/npm/package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cs, err := Npm(f)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cs {
		if c.Kind != KindNpm {
			t.Errorf("%s: kind %d", c.Name, c.Kind)
		}
		got = append(got, c.Name+" "+c.Version+" "+c.License+" "+c.Purl)
	}
	want := []string{
		"@scope/widget 1.2.3 MIT OR Apache-2.0 pkg:npm/%40scope/widget@1.2.3",
		"scheduler 0.27.0 MIT pkg:npm/scheduler@0.27.0",
		"react 19.3.1 MIT pkg:npm/react@19.3.1",
		"scheduler 0.28.0 MIT pkg:npm/scheduler@0.28.0",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
}

// The lockfile of the registry Web UI (oec/registry/ui, ADR-0029 §10) is
// the input of the registry image (oec/registry/Dockerfile): the three
// packages its bundle ships, all MIT.
func TestNpmRegistryUI(t *testing.T) {
	f, err := os.Open("../../../registry/ui/package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cs, err := Npm(f)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cs {
		got = append(got, c.Name+"@"+c.Version+" "+c.License)
	}
	if want := []string{"react@19.3.1 MIT", "react-dom@19.3.1 MIT", "scheduler@0.28.0 MIT"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// npm packages come after the Go modules and before the Rust standard
// library in the SBOM; the order of the other kinds stays.
func TestNpmOrder(t *testing.T) {
	f, err := os.Open("testdata/npm/package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cs, err := Npm(f)
	if err != nil {
		t.Fatal(err)
	}
	in := fixtureInput(t)
	in.Components = append(in.Components, cs...)
	var doc spdxDoc
	if err := json.Unmarshal(mustMake(t, in).SPDX, &doc); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range doc.Packages {
		names = append(names, p.Name)
	}
	at := func(name string) int {
		i := slices.Index(names, name)
		if i < 0 {
			t.Fatalf("%s not in %v", name, names)
		}
		return i
	}
	if !(at("github.com/OI4/oec/sdk/go") < at("@scope/widget") && at("scheduler") < at("rust-std")) {
		t.Errorf("order %v", names)
	}
}

// Lockfiles the tool refuses (fail closed).
func TestNpmRefused(t *testing.T) {
	for name, in := range map[string]string{
		"lockfile v1":    `{"lockfileVersion": 1, "dependencies": {"react": {"version": "19.3.1"}}}`,
		"no packages":    `{"lockfileVersion": 3}`,
		"link":           `{"lockfileVersion": 3, "packages": {"node_modules/x": {"version": "1.0.0", "license": "MIT", "link": true}}}`,
		"outside":        `{"lockfileVersion": 3, "packages": {"vendor/x": {"version": "1.0.0", "license": "MIT"}}}`,
		"no license":     `{"lockfileVersion": 3, "packages": {"node_modules/x": {"version": "1.0.0"}}}`,
		"legacy license": `{"lockfileVersion": 3, "packages": {"node_modules/x": {"version": "1.0.0", "license": {"type": "MIT"}}}}`,
		"no version":     `{"lockfileVersion": 3, "packages": {"node_modules/x": {"license": "MIT"}}}`,
		"only dev":       `{"lockfileVersion": 3, "packages": {"": {}, "node_modules/x": {"version": "1.0.0", "license": "MIT", "dev": true}}}`,
		"not json":       `lockfileVersion = 3`,
	} {
		if _, err := Npm(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
