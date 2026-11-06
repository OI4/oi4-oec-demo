package sbom

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// golistFormat is the go list template of oec/ci/sbom.sh (C-108).
const golistFormat = `{{with .Module}}{{.Path}}|{{.Version}}|{{with .Replace}}{{.Path}}|{{.Version}}{{end}}{{end}}`

// TestGoList: the Go modules of a binary or library from go list -deps
// (C-108, deliverables without an image): one line per package, the
// standard library packages empty, the main module without version; a
// module of this workspace (local replacement) gets the local license, a
// third-party module the license of the reviewed map, fail closed as GoKnown.
func TestGoList(t *testing.T) {
	if !strings.Contains(read(t, "../../../ci/sbom.sh"), golistFormat) {
		t.Error("oec/ci/sbom.sh does not use the go list format of GoList")
	}
	known, err := ParseGoLicenses([]byte(`{"modules": {
		"golang.org/x/net@v0.62.0": "BSD-3-Clause",
		"github.com/c/d@v1.1.0": "Apache-2.0"}}`))
	if err != nil {
		t.Fatal(err)
	}
	in := "\n\ngithub.com/OI4/oec/conformance||\n" +
		"github.com/OI4/oec/spec|v0.0.0-00010101000000-000000000000|../spec|\n" +
		"golang.org/x/net|v0.62.0|\n" +
		"golang.org/x/net|v0.62.0|\n" +
		"github.com/a/b|v1.0.0|github.com/c/d|v1.1.0\n" +
		"github.com/OI4/oec/conformance||\n"
	cs, err := GoList(strings.NewReader(in), "MIT", known)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cs {
		if c.Kind != KindGo {
			t.Errorf("%s: kind %d", c.Name, c.Kind)
		}
		got = append(got, c.Name+"@"+c.Version+" "+c.License+" "+c.Purl)
	}
	want := []string{
		"github.com/OI4/oec/spec@v0.0.0-00010101000000-000000000000 MIT pkg:golang/github.com/OI4/oec/spec@v0.0.0-00010101000000-000000000000",
		"golang.org/x/net@v0.62.0 BSD-3-Clause pkg:golang/golang.org/x/net@v0.62.0",
		"github.com/c/d@v1.1.0 Apache-2.0 pkg:golang/github.com/c/d@v1.1.0",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
	// the main module alone: a library or binary without dependencies
	if cs, err := GoList(strings.NewReader("github.com/OI4/oec/sdk/go||\n\n"), "MIT", nil); err != nil || len(cs) != 0 {
		t.Errorf("main module only: %v, %v", cs, err)
	}
	for name, in := range map[string]string{
		"foreign module":     "golang.org/x/net|v0.61.0|\n",
		"foreign repl.":      "github.com/a/b|v1.0.0|github.com/c/e|v1.1.0\n",
		"no separator":       "golang.org/x/net v0.62.0\n",
		"empty":              "",
		"two main modules":   "a||\nb||\n",
		"no main module":     "golang.org/x/net|v0.62.0|\n",
		"replacement no ver": "github.com/a/b|v1.0.0|github.com/c/d|\n",
	} {
		if _, err := GoList(strings.NewReader(in), "MIT", known); err == nil {
			t.Errorf("go list %s: accepted", name)
		}
	}
}

// TestGoStd: the Go standard library linked into a Go binary, from the
// version of the toolchain (go env GOVERSION).
func TestGoStd(t *testing.T) {
	for _, v := range []string{"go1.27.3", "1.27.3"} {
		c, err := GoStd(v)
		if err != nil {
			t.Fatal(err)
		}
		if c.Kind != KindGo || c.Name != "stdlib" || c.Version != "go1.27.3" || c.License != "BSD-3-Clause" || c.Purl != "pkg:golang/stdlib@1.27.3" {
			t.Errorf("%s: %+v", v, c)
		}
	}
	for _, v := range []string{"", "devel", "go1.27.3 X:foo", "go"} {
		if _, err := GoStd(v); err == nil {
			t.Errorf("GoStd(%q): accepted", v)
		}
	}
}

// TestReport: the license report next to the SBOM (C-108): every license
// identifier with the number of components that name it and its text
// file, then every component with kind, name, version, license and purl,
// in the order of the SBOM. Deterministic (no time in it).
func TestReport(t *testing.T) {
	texts := fstest.MapFS{"MIT.txt": {Data: []byte("MIT text")}, "Apache-2.0.txt": {Data: []byte("Apache text")}}
	in := Input{App: App("oec-ffi", "0.1.0", "MIT"), Texts: texts, Created: time.Unix(0, 0),
		Components: []Component{
			{Kind: KindCargo, Name: "b", Version: "1.0.0", License: "MIT OR Apache-2.0", Purl: "pkg:cargo/b@1.0.0"},
			{Kind: KindCargo, Name: "a", Version: "2.0.0", License: "Apache-2.0", Purl: "pkg:cargo/a@2.0.0"},
			RustStd("1.89.0"),
		}}
	res := mustMake(t, in)
	want := `# License report: oec-ffi 0.1.0

The SBOM ` + "`sbom.spdx.json`" + ` (SPDX 2.3) of oec-ffi 0.1.0 lists 4 components, the application included; the text of every license is next to it as ` + "`<id>.txt`" + `. Made by oec-sbom (ADR-0028 §4, amendment C-108).

## Licenses

| License | Components | Text |
|---|---:|---|
| Apache-2.0 | 3 | ` + "`Apache-2.0.txt`" + ` |
| MIT | 3 | ` + "`MIT.txt`" + ` |

## Components

| Kind | Name | Version | License | purl |
|---|---|---|---|---|
| application | oec-ffi | 0.1.0 | MIT | ` + "`pkg:generic/oi4/oec-ffi@0.1.0`" + ` |
| cargo | a | 2.0.0 | Apache-2.0 | ` + "`pkg:cargo/a@2.0.0`" + ` |
| cargo | b | 1.0.0 | MIT OR Apache-2.0 | ` + "`pkg:cargo/b@1.0.0`" + ` |
| rust | rust-std | 1.89.0 | MIT OR Apache-2.0 | ` + "`pkg:generic/rust-lang/rust-std@1.89.0`" + ` |
`
	if string(res.Report) != want {
		t.Errorf("report:\n%s\nwant:\n%s", res.Report, want)
	}
	again := mustMake(t, in)
	if string(again.Report) != string(res.Report) {
		t.Error("report not deterministic")
	}
	// the image fixture: every kind has a name, every text is listed
	full := string(mustMake(t, fixtureInput(t)).Report)
	for _, s := range []string{"| debian |", "| go |", "| cargo |", "| rust |", "`LicenseRef-"} {
		if !strings.Contains(full, s) {
			t.Errorf("fixture report lacks %q", s)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
