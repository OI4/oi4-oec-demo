package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeliverable: the SBOM of a deliverable without an image (C-108):
// no -base/-debian, the Go modules from go list (-golist), the standard
// library (-gostd), the license report (-report).
func TestDeliverable(t *testing.T) {
	dir := t.TempDir()
	golist := filepath.Join(dir, "golist.txt")
	if err := os.WriteFile(golist, []byte("github.com/OI4/oec/demos||\ngithub.com/OI4/oec/sdk/go|v0.0.0|../sdk/go|\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	t.Setenv("SOURCE_DATE_EPOCH", "0")
	err := run([]string{"-name", "oec-weather", "-version", "0.1.0", "-golist", golist, "-gostd", "go1.27.3",
		"-texts", "../../../../images/licenses", "-out", out, "-report", filepath.Join(out, "LICENSES.md")})
	if err != nil {
		t.Fatal(err)
	}
	spdx, err := os.ReadFile(filepath.Join(out, "sbom.spdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"name": "oec-weather"`, "pkg:golang/stdlib@1.27.3", "pkg:golang/github.com/OI4/oec/sdk/go@v0.0.0", `"created": "1970-01-01T00:00:00Z"`} {
		if !strings.Contains(string(spdx), s) {
			t.Errorf("SBOM lacks %s", s)
		}
	}
	if strings.Contains(string(spdx), "pkg:deb/") {
		t.Error("SBOM without -base lists Debian packages")
	}
	report, err := os.ReadFile(filepath.Join(out, "LICENSES.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(report), "# License report: oec-weather 0.1.0\n") {
		t.Errorf("report:\n%s", report)
	}
	for _, f := range []string{"MIT.txt", "BSD-3-Clause.txt"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Error(err)
		}
	}
	// -base and -debian go together; -name, -version, -texts and -out stay required
	for name, args := range map[string][]string{
		"base without debian": {"-name", "x", "-version", "1", "-base", dir, "-texts", "../../../../images/licenses", "-out", filepath.Join(dir, "a")},
		"debian without base": {"-name", "x", "-version", "1", "-debian", "../../../../images/debian.json", "-texts", "../../../../images/licenses", "-out", filepath.Join(dir, "b")},
		"no texts":            {"-name", "x", "-version", "1", "-out", filepath.Join(dir, "c")},
		"no out":              {"-name", "x", "-version", "1", "-texts", "../../../../images/licenses"},
		"bad gostd":           {"-name", "x", "-version", "1", "-gostd", "devel", "-texts", "../../../../images/licenses", "-out", filepath.Join(dir, "d")},
	} {
		if err := run(args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
