package sbom

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/OI4/oec/demos/internal/demotest"
)

// D8 (`oec-infra image`, the program of OEC_D8 – run, not imported:
// ADR-0006) accepts the SBOM of the fixtures: a minimal OCI layout for
// linux/amd64 and linux/arm64 with only /opt/oi4/licenses passes
// T8-IMG-03 (SBOM, licenses, texts) without a finding. Offline; the real
// images are checked by the opt-in run (images/build.sh).
// Without the text of MIT, T8-IMG-03 fails (the check is not vacuous).
func TestD8Image(t *testing.T) {
	d8 := demotest.Tool(t, "OEC_D8", "", "go build ./cmd/oec-infra in oec/conformance")
	res := mustMake(t, fixtureInput(t))
	files := map[string][]byte{SPDXFile: res.SPDX}
	for id, text := range res.Texts {
		files[id+".txt"] = text
	}
	if got := d8Image(t, d8, files); got["T8-IMG-03"] != "pass" {
		t.Errorf("T8-IMG-03: %s", got["T8-IMG-03"])
	}
	delete(files, "MIT.txt")
	if got := d8Image(t, d8, files); got["T8-IMG-03"] != "fail" {
		t.Errorf("T8-IMG-03 without MIT.txt: %s, want fail", got["T8-IMG-03"])
	}
}

// d8Image runs `oec-infra image` on an OCI layout of files and returns the
// outcome of every test; T8-IMG-03 must name every failed requirement.
func d8Image(t *testing.T, d8 string, files map[string][]byte) map[string]string {
	t.Helper()
	dir := t.TempDir()
	ociLayout(t, dir, files)
	cmd := exec.Command(d8, "image", "--format", "json", dir)
	out, err := cmd.Output()
	if err != nil && cmd.ProcessState.ExitCode() > 1 {
		t.Fatalf("oec-infra image: %v\n%s", err, out)
	}
	var rep struct {
		Tests []struct {
			ID      string `json:"id"`
			Outcome string `json:"outcome"`
			Reqs    []struct {
				Req     string `json:"req"`
				Outcome string `json:"outcome"`
				Reason  string `json:"reason"`
			} `json:"reqs"`
		} `json:"tests"`
	}
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, tc := range rep.Tests {
		got[tc.ID] = tc.Outcome
		for _, r := range tc.Reqs {
			if r.Outcome != "pass" {
				t.Logf("%s %s %s: %s", tc.ID, r.Req, r.Outcome, r.Reason)
			}
		}
	}
	if _, ok := got["T8-IMG-03"]; !ok {
		t.Fatalf("no T8-IMG-03 in the report:\n%s", out)
	}
	return got
}

// ociLayout writes an OCI image layout with one layer holding files under
// /opt/oi4/licenses for linux/amd64 and linux/arm64/v8.
func ociLayout(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	blob := func(data []byte) (string, int) {
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])
		p := filepath.Join(dir, "blobs", "sha256", h)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return "sha256:" + h, len(data)
	}
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	for _, d := range []string{"opt/", "opt/oi4/", "opt/oi4/licenses/"} {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: d, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "opt/oi4/licenses/" + n, Mode: 0o644, Size: int64(len(files[n]))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(files[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	ld, ls := blob(layer.Bytes())
	type desc struct {
		MediaType string            `json:"mediaType"`
		Digest    string            `json:"digest"`
		Size      int               `json:"size"`
		Platform  map[string]string `json:"platform,omitempty"`
	}
	mustJSON := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var manifests []desc
	for _, p := range []map[string]string{{"os": "linux", "architecture": "amd64"}, {"os": "linux", "architecture": "arm64", "variant": "v8"}} {
		cd, cs := blob(mustJSON(map[string]any{
			"architecture": p["architecture"], "os": "linux", "variant": p["variant"],
			"config": map[string]any{"User": "65532:65532"},
			"rootfs": map[string]any{"type": "layers", "diff_ids": []string{ld}},
		}))
		md, ms := blob(mustJSON(map[string]any{
			"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
			"config": desc{MediaType: "application/vnd.oci.image.config.v1+json", Digest: cd, Size: cs},
			"layers": []desc{{MediaType: "application/vnd.oci.image.layer.v1.tar", Digest: ld, Size: ls}},
		}))
		manifests = append(manifests, desc{MediaType: "application/vnd.oci.image.manifest.v1+json", Digest: md, Size: ms, Platform: p})
	}
	index := mustJSON(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": manifests})
	for name, data := range map[string][]byte{"index.json": index, "oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
