package sbom

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// The fixtures of testdata (a cargo tree, a go version -m output and a
// small Debian file tree) give the reviewed SBOM testdata/expected/ (R-1
// of C-084): sbom.spdx.json and texts.txt, the ids of the license texts
// written next to it; OEC_SBOM_UPDATE=1 rewrites both.
func TestGolden(t *testing.T) {
	res := mustMake(t, fixtureInput(t))
	var ids []string
	for id := range res.Texts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	texts := []byte(strings.Join(ids, "\n") + "\n")
	dir := filepath.Join("testdata", "expected")
	if os.Getenv("OEC_SBOM_UPDATE") == "1" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, SPDXFile), res.SPDX, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "texts.txt"), texts, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, got := range map[string][]byte{SPDXFile: res.SPDX, "texts.txt": texts} {
		want, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from %s (OEC_SBOM_UPDATE=1 rewrites it):\n%s", name, dir, got)
		}
	}
	// the texts are the files of images/licenses or the embedded ones
	for id, text := range res.Texts {
		if strings.HasPrefix(id, "LicenseRef-") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(licenseDir, id+".txt"))
		if err != nil || !bytes.Equal(b, text) {
			t.Errorf("%s: not the text of images/licenses: %v", id, err)
		}
	}
}

// What D8 (T8-IMG-03) and SF-114 ask of the SBOM: document fields, every
// package with name, version, a stated license and a purl, a text for
// every license and exception id (embedded for LicenseRef-).
func TestRules(t *testing.T) {
	res := mustMake(t, fixtureInput(t))
	var doc spdxDoc
	if err := json.Unmarshal(res.SPDX, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SPDXVersion != "SPDX-2.3" || doc.DataLicense != "CC0-1.0" || doc.SPDXID != "SPDXRef-DOCUMENT" ||
		doc.Name != "oec-demo-0.1.0" || !strings.HasPrefix(doc.DocumentNamespace, nsBase+"oec-demo-0.1.0-") ||
		doc.CreationInfo.Created != "2026-11-01T12:00:00Z" || len(doc.CreationInfo.Creators) == 0 {
		t.Errorf("document fields: %+v", doc)
	}
	embedded := map[string]bool{}
	for _, e := range doc.Extracted {
		embedded[e.LicenseID] = strings.TrimSpace(e.ExtractedText) != ""
	}
	ids := map[string]bool{}
	for _, p := range doc.Packages {
		if p.Name == "" || p.VersionInfo == "" || p.LicenseConcluded != p.LicenseDeclared ||
			len(p.ExternalRefs) != 1 || p.ExternalRefs[0].Category != "PACKAGE-MANAGER" || !strings.HasPrefix(p.ExternalRefs[0].Locator, "pkg:") {
			t.Errorf("package %+v", p)
		}
		if ids[p.SPDXID] {
			t.Errorf("SPDXID %s twice", p.SPDXID)
		}
		ids[p.SPDXID] = true
		lids, err := IDs(p.LicenseConcluded)
		if err != nil {
			t.Error(err)
		}
		for _, id := range lids {
			if len(res.Texts[id]) == 0 {
				t.Errorf("%s: no text for %s", p.Name, id)
			}
			if strings.HasPrefix(id, "LicenseRef-") && !embedded[id] {
				t.Errorf("%s: %s not in hasExtractedLicensingInfos", p.Name, id)
			}
		}
	}
	if len(doc.Packages) != 13 {
		t.Errorf("%d packages, expected 13", len(doc.Packages))
	}
	if doc.Relationships[0].Type != "DESCRIBES" || len(doc.Relationships) != len(doc.Packages) {
		t.Errorf("relationships %+v", doc.Relationships)
	}
	// the same input gives the same document (reproducible)
	if again := mustMake(t, fixtureInput(t)); !bytes.Equal(again.SPDX, res.SPDX) {
		t.Error("not deterministic")
	}
}

func TestDebianFixture(t *testing.T) {
	cs, refs, err := Debian(os.DirFS("testdata/base"), debianMap(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"libc6":     "LGPL-2.1-or-later AND (BSD-2-Clause OR GPL-2.0-only) AND LicenseRef-libc6-public-domain AND LicenseRef-libc6-Univ-Coimbra",
		"libgcc-s1": "GPL-3.0-or-later WITH GCC-exception-3.1",
		"tzdata":    "LicenseRef-tzdata-public-domain",
	}
	purls := map[string]string{
		"libc6":  "pkg:deb/debian/libc6@2.41-12%2Bdeb13u4?arch=arm64&distro=debian-13",
		"tzdata": "pkg:deb/debian/tzdata@1%3A2026f-0%2Bdeb13u1?arch=all&distro=debian-13",
	}
	if len(cs) != len(want) {
		t.Fatalf("%d packages: %+v", len(cs), cs)
	}
	for _, c := range cs {
		if c.License != want[c.Name] {
			t.Errorf("%s: license %q, expected %q", c.Name, c.License, want[c.Name])
		}
		if p, ok := purls[c.Name]; ok && c.Purl != p {
			t.Errorf("%s: purl %q, expected %q", c.Name, c.Purl, p)
		}
	}
	if got := refs["LicenseRef-libc6-public-domain"]; got != "This file is in the public domain.\n\nWritten by a hacker, placed in the public domain.\n" {
		t.Errorf("public-domain of two Files paragraphs: %q", got)
	}
	if got := refs["LicenseRef-libc6-Univ-Coimbra"]; !strings.Contains(got, "University of Coimbra.\n\nTHIS SOFTWARE") {
		t.Errorf("Univ-Coimbra: %q", got)
	}
	if len(refs) != 3 {
		t.Errorf("LicenseRefs %v", refs)
	}
}

// The mapping images/debian.json only names SPDX ids that have a text
// in images/licenses (and so does every package expression).
func TestDebianMapTexts(t *testing.T) {
	m := debianMap(t)
	var exprs []string
	for _, e := range m.SPDX {
		exprs = append(exprs, e)
	}
	for _, e := range m.Packages {
		exprs = append(exprs, e)
	}
	for _, e := range exprs {
		ids, err := IDs(e)
		if err != nil {
			t.Error(err)
		}
		for _, id := range ids {
			if _, err := os.Stat(filepath.Join(licenseDir, id+".txt")); err != nil {
				t.Errorf("%s: %v", e, err)
			}
		}
	}
}

func TestCargo(t *testing.T) {
	f, err := os.Open("testdata/cargo-tree.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cs, err := Cargo(f)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cs {
		got = append(got, c.Name+" "+c.Version+" "+c.License+" "+c.Purl)
	}
	want := []string{
		"oec-ffi 0.1.0 MIT pkg:generic/oi4/oec-ffi@0.1.0",
		"oec 0.1.0 MIT pkg:generic/oi4/oec@0.1.0",
		"aws-lc-rs 1.18.2 ISC AND (Apache-2.0 OR ISC) pkg:cargo/aws-lc-rs@1.18.2",
		"memchr 2.8.3 Unlicense OR MIT pkg:cargo/memchr@2.8.3",
		"serde 1.0.229 MIT OR Apache-2.0 pkg:cargo/serde@1.0.229",
		"subtle 2.6.1 BSD-3-Clause pkg:cargo/subtle@2.6.1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
}

func TestGo(t *testing.T) {
	f, err := os.Open("testdata/go-version.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cs, err := Go(f, "MIT")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Purl != "pkg:golang/stdlib@1.27.3" || cs[0].Version != "go1.27.3" || cs[0].License != "BSD-3-Clause" ||
		cs[1].Name != "github.com/OI4/oec/sdk/go" || cs[1].License != "MIT" || cs[1].Purl != "pkg:golang/github.com/OI4/oec/sdk/go@v0.0.0" {
		t.Errorf("%+v", cs)
	}
}

// TestGoKnown: a module that is not of this workspace gets the license of
// the reviewed map (C-107, the tool image of oec/conformance), keyed by
// module path and version: another version is unknown again.
func TestGoKnown(t *testing.T) {
	known, err := ParseGoLicenses([]byte(`{"comment": "x", "modules": {
		"golang.org/x/net@v0.61.0": "BSD-3-Clause",
		"go.yaml.in/yaml/v3@v3.0.5": "MIT AND Apache-2.0",
		"github.com/c/d@v1.1.0": "Apache-2.0"}}`))
	if err != nil {
		t.Fatal(err)
	}
	in := "x: go1.27.3\n\tpath\tx\n\tmod\tx\t(devel)\t\n" +
		"\tdep\tgithub.com/OI4/oec/spec\tv0.0.0\n\t=>\t../spec\t(devel)\t\n" +
		"\tdep\tgolang.org/x/net\tv0.61.0\th1:abc\n" +
		"\tdep\tgo.yaml.in/yaml/v3\tv3.0.5\th1:def\n" +
		"\tdep\tgithub.com/a/b\tv1.0.0\n\t=>\tgithub.com/c/d\tv1.1.0\th1:ghi\n"
	cs, err := GoKnown(strings.NewReader(in), "MIT", known)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cs {
		got = append(got, c.Name+"@"+c.Version+" "+c.License+" "+c.Purl)
	}
	want := []string{
		"stdlib@go1.27.3 BSD-3-Clause pkg:golang/stdlib@1.27.3",
		"github.com/OI4/oec/spec@v0.0.0 MIT pkg:golang/github.com/OI4/oec/spec@v0.0.0",
		"golang.org/x/net@v0.61.0 BSD-3-Clause pkg:golang/golang.org/x/net@v0.61.0",
		"go.yaml.in/yaml/v3@v3.0.5 MIT AND Apache-2.0 pkg:golang/go.yaml.in/yaml/v3@v3.0.5",
		"github.com/c/d@v1.1.0 Apache-2.0 pkg:golang/github.com/c/d@v1.1.0",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
	if _, err := GoKnown(strings.NewReader("x: go1.27.3\n\tdep\tgolang.org/x/net\tv0.62.0\th1:abc\n"), "MIT", known); err == nil {
		t.Error("a version not in the map: accepted")
	}
	for name, data := range map[string]string{
		"not JSON":       `{`,
		"no modules":     `{"comment": "x"}`,
		"no version":     `{"modules": {"golang.org/x/net": "BSD-3-Clause"}}`,
		"bad expression": `{"modules": {"golang.org/x/net@v0.61.0": "MIT OR"}}`,
		"NOASSERTION":    `{"modules": {"golang.org/x/net@v0.61.0": "NOASSERTION"}}`,
	} {
		if _, err := ParseGoLicenses([]byte(data)); err == nil {
			t.Errorf("Go license map %s: accepted", name)
		}
	}
}

// TestConformanceGoLicenses: the map of the tool image covers exactly the
// third-party modules of oec/conformance/go.mod (its go.sum versions), so a
// dependency update fails here before the image build.
func TestConformanceGoLicenses(t *testing.T) {
	data, err := os.ReadFile("../../../conformance/images/go-licenses.json")
	if err != nil {
		t.Fatal(err)
	}
	known, err := ParseGoLicenses(data)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile("../../../conformance/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	need := map[string]bool{}
	for _, line := range strings.Split(string(mod), "\n") {
		f := strings.Fields(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "require")))
		if len(f) >= 2 && strings.Contains(f[0], ".") && strings.HasPrefix(f[1], "v") && !strings.HasPrefix(f[0], "github.com/OI4/") &&
			!strings.HasPrefix(f[1], "v0.0.0-0001") {
			need[f[0]+"@"+f[1]] = true
		}
	}
	if len(need) == 0 {
		t.Fatal("no module requirements in conformance/go.mod")
	}
	for k := range need {
		if _, ok := known[k]; !ok {
			t.Errorf("go-licenses.json lacks %s (a module of conformance/go.mod)", k)
		}
	}
	for k := range known {
		if !need[k] {
			t.Errorf("go-licenses.json lists %s, which conformance/go.mod does not require", k)
		}
	}
}

// Inputs the tool refuses (fail closed): the build stops instead of
// writing an SBOM with a gap.
func TestRefused(t *testing.T) {
	cargo := map[string]string{
		"git source":     "foo v1.0.0 (https://github.com/x/foo#abc)|MIT\n",
		"no license":     "foo v1.0.0|\n",
		"no separator":   "foo v1.0.0 MIT\n",
		"no version":     "foo|MIT\n",
		"empty":          "\n",
		"other registry": "foo v1.0.0 (registry `corp`)|MIT\n",
	}
	for name, in := range cargo {
		if _, err := Cargo(strings.NewReader(in)); err == nil {
			t.Errorf("cargo %s: accepted", name)
		}
	}
	gov := map[string]string{
		"foreign module": "x: go1.27.3\n\tdep\tgolang.org/x/net\tv0.30.0\th1:abc\n",
		"foreign repl.":  "x: go1.27.3\n\tdep\tgithub.com/a/b\tv1.0.0\n\t=>\tgithub.com/c/d\tv1.1.0\n",
		"no version":     "x: devel\n",
		"empty":          "",
	}
	for name, in := range gov {
		if _, err := Go(strings.NewReader(in), "MIT"); err == nil {
			t.Errorf("go %s: accepted", name)
		}
	}
	exprs := []string{"", "NOASSERTION", "NONE", "MIT OR", "(MIT", "MIT)", "MIT Apache-2.0", "MIT AND AND ISC", "MIT,ISC"}
	for _, e := range exprs {
		if _, err := IDs(e); err == nil {
			t.Errorf("expression %q: accepted", e)
		}
	}
	copyright := map[string]string{
		"not DEP-5":      "GNU compiler collection\n",
		"no text":        "Format: x\n\nFiles: *\nLicense: Foo-1\n",
		"comma":          "Format: x\n\nFiles: *\nLicense: GPL-2+ or Artistic, and BSD-3-clause\n",
		"and/or":         "Format: x\n\nFiles: *\nLicense: GPL-2+ or Artistic and BSD-3-clause\n",
		"with exception": "Format: x\n\nFiles: *\nLicense: GPL-2+ with OpenSSL exception\n",
		"no Files":       "Format: x\n\nLicense: GPL-2+\n text\n",
	}
	for name, text := range copyright {
		fsys := fstest.MapFS{
			"etc/os-release":              {Data: []byte("ID=debian\nVERSION_ID=13\n")},
			"var/lib/dpkg/status.d/foo":   {Data: []byte("Package: foo\nVersion: 1\nArchitecture: all\n")},
			"usr/share/doc/foo/copyright": {Data: []byte(text)},
		}
		if _, _, err := Debian(fsys, debianMap(t)); err == nil {
			t.Errorf("copyright %s: accepted", name)
		}
	}
	// a license id without text in images/licenses
	in := fixtureInput(t)
	in.Components = append(in.Components, Component{Kind: KindCargo, Name: "x", Version: "1", License: "WTFPL", Purl: "pkg:cargo/x@1"})
	if _, err := Make(in); err == nil || !strings.Contains(err.Error(), "WTFPL") {
		t.Errorf("license without text: %v", err)
	}
	// a LicenseRef without text
	in = fixtureInput(t)
	in.Components = append(in.Components, Component{Kind: KindCargo, Name: "x", Version: "1", License: "LicenseRef-x", Purl: "pkg:cargo/x@1"})
	if _, err := Make(in); err == nil {
		t.Error("LicenseRef without text: accepted")
	}
	// a component without purl
	in = fixtureInput(t)
	in.Components = append(in.Components, Component{Kind: KindCargo, Name: "x", Version: "1", License: "MIT"})
	if _, err := Make(in); err == nil {
		t.Error("component without purl: accepted")
	}
}

func TestWrite(t *testing.T) {
	res := mustMake(t, fixtureInput(t))
	dir := t.TempDir()
	if err := res.Write(dir); err != nil {
		t.Fatal(err)
	}
	if err := res.Write(dir); err == nil {
		t.Error("second Write into the same directory: accepted")
	}
}

func TestExpression(t *testing.T) {
	for e, want := range map[string]string{
		"MIT":                                 "MIT",
		"(MIT OR Apache-2.0) AND Unicode-3.0": "MIT Apache-2.0 Unicode-3.0",
		"GPL-3.0-or-later WITH GCC-exception-3.1": "GPL-3.0-or-later GCC-exception-3.1",
		"GPL-2.0+ AND LicenseRef-x-y.z":           "GPL-2.0+ LicenseRef-x-y.z",
		"ISC AND (Apache-2.0 OR ISC)":             "ISC Apache-2.0",
	} {
		ids, err := IDs(e)
		if err != nil || strings.Join(ids, " ") != want {
			t.Errorf("IDs(%q) = %v, %v; expected %s", e, ids, err, want)
		}
	}
	if got := Normalize("MIT/Apache-2.0"); got != "MIT OR Apache-2.0" {
		t.Errorf("Normalize: %q", got)
	}
	if got := RefID("GPL-2+-with link_exception"); got != "GPL-2plus-with-link-exception" {
		t.Errorf("RefID: %q", got)
	}
	if got := purlEscape("1:1.3+b1~x"); got != "1%3A1.3%2Bb1~x" {
		t.Errorf("purlEscape: %q", got)
	}
}

const licenseDir = "../../images/licenses"

func debianMap(t *testing.T) *DebianMap {
	t.Helper()
	data, err := os.ReadFile("../../images/debian.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseDebianMap(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func fixtureInput(t *testing.T) Input {
	t.Helper()
	in := Input{App: App("oec-demo", "0.1.0", "MIT"), Texts: os.DirFS(licenseDir),
		Created: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	f, err := os.Open("testdata/cargo-tree.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cs, err := Cargo(f)
	if err != nil {
		t.Fatal(err)
	}
	g, err := os.Open("testdata/go-version.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	gs, err := Go(g, "MIT")
	if err != nil {
		t.Fatal(err)
	}
	debs, refs, err := Debian(os.DirFS("testdata/base"), debianMap(t))
	if err != nil {
		t.Fatal(err)
	}
	in.Components = append(append(append(cs, gs...), RustStd("1.89.0")), debs...)
	in.Refs = refs
	return in
}

func mustMake(t *testing.T, in Input) *Result {
	t.Helper()
	res, err := Make(in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
