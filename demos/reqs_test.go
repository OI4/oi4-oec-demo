// Package demos holds the demo services of P6 (ADR-0028); this file only
// tests the repository rules for all of them.
package demos

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var reqID = regexp.MustCompile(`REQ-[A-Z]+(?:-[A-Z]+)*-[0-9]{3}`)

// Every REQ id cited under oec/demos (sources, tests, READMEs) exists in
// oec/spec/requirements and is not deprecated (as oec/sdk, C-060).
func TestCitedRequirements(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	demos := filepath.Dir(file)
	status := requirements(t, filepath.Join(demos, "..", "spec", "requirements"))
	cited := 0
	err := filepath.WalkDir(demos, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "target" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".c", ".h", ".go", ".rs", ".toml", ".md", ".sh", ".html", ".js", ".css", ".yaml", ".yml", "":
		default:
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, id := range reqID.FindAllString(string(b), -1) {
			cited++
			switch s, ok := status[id]; {
			case !ok:
				t.Errorf("%s: %s does not exist", path, id)
			case s == "deprecated":
				t.Errorf("%s: %s is deprecated", path, id)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if cited == 0 {
		t.Fatal("no REQ id cited under oec/demos")
	}
}

// requirements reads id → status from the requirement files (lines
// "  - id: REQ-…" and "    status: …").
func requirements(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no requirement files in %s: %v", dir, err)
	}
	status := map[string]string{}
	for _, f := range files {
		r, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		id := ""
		scan := bufio.NewScanner(r)
		for scan.Scan() {
			line := scan.Text()
			if v, ok := strings.CutPrefix(line, "  - id: "); ok {
				id = strings.TrimSpace(v)
				status[id] = ""
			} else if v, ok := strings.CutPrefix(line, "    status: "); ok && id != "" {
				status[id] = strings.TrimSpace(v)
			}
		}
		r.Close()
		if err := scan.Err(); err != nil {
			t.Fatal(err)
		}
	}
	if len(status) < 400 {
		t.Fatalf("only %d requirements read from %s", len(status), dir)
	}
	return status
}
