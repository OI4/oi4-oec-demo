package demos

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The images of the demos (ADR-0028 §4, C-084) and the UI port each one
// publishes (ADR-0028 §3).
var images = map[string]string{"logger": "8781", "weather": "8782", "config": "8783"}

// pinned is an image reference by tag and digest.
var pinned = regexp.MustCompile(`^[a-z0-9.-]+(?:/[a-z0-9._-]+)+:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$`)

// instruction is one Dockerfile instruction with its continuation lines.
type instruction struct {
	cmd, args string
}

func dockerfile(t *testing.T, path string) []instruction {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []instruction
	var cur strings.Builder
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if cur.Len() == 0 && (line == "" || strings.HasPrefix(line, "#")) {
			continue
		}
		if strings.HasSuffix(line, "\\") {
			cur.WriteString(strings.TrimSuffix(line, "\\") + " ")
			continue
		}
		cur.WriteString(line)
		cmd, args, _ := strings.Cut(cur.String(), " ")
		out = append(out, instruction{strings.ToUpper(cmd), strings.TrimSpace(args)})
		cur.Reset()
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The three Dockerfiles follow ADR-0028 §4, checked offline: the same
// builder and base images, pinned by tag and digest; every FROM is one of
// them or an own stage; the final stage is the base image without RUN,
// runs as the fixed non-root user 65532:65532 of the base, declares the
// volumes /etc/oi4/app and /opt/oi4/app (precreated for that user), copies
// the SBOM and the license texts to /opt/oi4/licenses (RIG 4.1.4), listens
// with its UI on 0.0.0.0:<port> (published on 127.0.0.1 only by compose),
// carries the OCI labels and no ADD, no secret and no key material.
func TestDockerfiles(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	demos := filepath.Dir(file)
	var args map[string]string
	for demo, port := range images {
		t.Run(demo, func(t *testing.T) {
			ins := dockerfile(t, filepath.Join(demos, demo, "Dockerfile"))
			own := map[string]string{}
			stages := map[string]bool{}
			var final []instruction
			for _, in := range ins {
				switch in.cmd {
				case "ARG":
					if k, v, ok := strings.Cut(in.args, "="); ok && strings.HasSuffix(k, "_IMAGE") {
						if !pinned.MatchString(v) {
							t.Errorf("%s=%s: not pinned by tag and digest", k, v)
						}
						own[k] = v
					}
				case "FROM":
					f := strings.Fields(in.args)
					if len(f) > 0 && strings.HasPrefix(f[0], "--platform=") {
						f = f[1:]
					}
					ref := strings.Trim(f[0], "${}")
					if _, ok := own[ref]; !ok && !stages[f[0]] {
						t.Errorf("FROM %s: neither a pinned image ARG nor an own stage", in.args)
					}
					if len(f) == 3 && strings.EqualFold(f[1], "AS") {
						stages[f[2]] = true
					}
					final = nil
					if ref == "BASE_IMAGE" && len(f) == 1 {
						final = []instruction{in}
					}
					continue
				case "ADD":
					t.Errorf("ADD %s: use COPY", in.args)
				}
				if final != nil {
					final = append(final, in)
				}
				lower := strings.ToLower(in.args)
				for _, bad := range []string{"dev/generated", ".key", ".pem", "passwd", "secret", "password", "token"} {
					if strings.Contains(lower, bad) {
						t.Errorf("%s %s: mentions %q", in.cmd, in.args, bad)
					}
				}
			}
			if args == nil {
				args = own
			} else if len(own) != len(args) {
				t.Errorf("images %v, others %v", own, args)
			} else {
				for k, v := range args {
					if own[k] != v {
						t.Errorf("%s=%s, others %s", k, own[k], v)
					}
				}
			}
			for _, k := range []string{"RUST_IMAGE", "GO_IMAGE", "BASE_IMAGE"} {
				if own[k] == "" {
					t.Errorf("no ARG %s", k)
				}
			}
			if !strings.HasPrefix(own["BASE_IMAGE"], "gcr.io/distroless/cc-debian13:nonroot@") {
				t.Errorf("BASE_IMAGE %s: not the base of A-367", own["BASE_IMAGE"])
			}
			if final == nil {
				t.Fatal("the final stage is not FROM ${BASE_IMAGE}")
			}
			got := map[string][]string{}
			for _, in := range final[1:] {
				got[in.cmd] = append(got[in.cmd], in.args)
			}
			if len(got["RUN"]) > 0 {
				t.Errorf("RUN in the final stage: %v", got["RUN"])
			}
			want := map[string][]string{
				"USER":       {"65532:65532"},
				"VOLUME":     {`["/etc/oi4/app", "/opt/oi4/app"]`},
				"ENV":        {"OEC_UI_LISTEN=0.0.0.0:" + port},
				"EXPOSE":     {port},
				"ENTRYPOINT": {`["/usr/local/bin/oec-` + demo + `"]`},
			}
			for cmd, w := range want {
				if !slices.Equal(got[cmd], w) {
					t.Errorf("%s %q, want %q", cmd, got[cmd], w)
				}
			}
			copies := strings.Join(got["COPY"], "\n")
			for _, c := range []string{
				"--from=build /out/oec-" + demo + " /usr/local/bin/oec-" + demo,
				"--from=build /out/licenses/ /opt/oi4/licenses/",
				"--from=build --chown=65532:65532 /out/app/ /etc/oi4/app/",
				"--from=build --chown=65532:65532 /out/app/ /opt/oi4/app/",
			} {
				if !strings.Contains(copies, c) {
					t.Errorf("no COPY %s", c)
				}
			}
			labels := strings.Join(got["LABEL"], " ")
			for _, l := range []string{"title=\"oec-" + demo + "\"", "version=\"${VERSION}\"", "licenses=\"MIT\"", "source=", "vendor=", "base.name="} {
				if !strings.Contains(labels, "org.opencontainers.image."+l) {
					t.Errorf("no label org.opencontainers.image.%s", l)
				}
			}
		})
	}
}

// oec/.dockerignore keeps the build context to what the builds need: no
// build output, no generated storages or keys of oec/dev.
func TestDockerignore(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 || lines[0] != "*" {
		t.Fatalf("not an allowlist (first pattern %q)", lines)
	}
	for _, l := range []string{"!schema", "!sdk/rust", "!sdk/go", "!demos", "**/target", "sdk/go/lib"} {
		if !slices.Contains(lines, l) {
			t.Errorf("no %s", l)
		}
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "!") && strings.Contains(l, "dev") && l != "!demos" {
			t.Errorf("%s: oec/dev is not part of the build context", l)
		}
	}
}

// images/build.sh runs BuildKit pinned by tag and digest, not the shared
// tag buildx-stable-1 that other builders of the host use; it removes its
// builder and the BuildKit image it pulled, and never prunes.
func TestBuildScript(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("images", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	m := regexp.MustCompile(`(?m)^buildkit="([^"]+)"$`).FindStringSubmatch(s)
	if m == nil || !pinned.MatchString(m[1]) || !strings.HasPrefix(m[1], "docker.io/moby/buildkit:v") {
		t.Fatalf("BuildKit image not pinned by version tag and digest: %v", m)
	}
	for _, want := range []string{`--driver-opt "image=$buildkit"`, `docker buildx rm "$builder"`} {
		if !strings.Contains(s, want) {
			t.Errorf("build.sh lacks %s", want)
		}
	}
	if regexp.MustCompile(`(?m)^[^#]*\bprune\b`).MatchString(s) {
		t.Error("build.sh must not prune")
	}
	// D8 levels are Service Container levels: all three demos target L3
	// (ADR-0028 table), whatever their Messaging level (A-377).
	for _, want := range []string{`oec-infra" image --level 3 `, `oec-infra" run --level 3 `} {
		if !strings.Contains(s, want) {
			t.Errorf("build.sh lacks D8 %s…", want)
		}
	}
	if regexp.MustCompile(`--level "?\$`).MatchString(s) {
		t.Error("build.sh must not take the D8 level from the demo (Messaging level)")
	}
	// A rebuild replaces the tag; the image it replaced is removed by ID
	// (without -f: an image that has another tag stays).
	if !strings.Contains(s, `docker image rm "$old"`) {
		t.Error("build.sh must remove the image a rebuild replaced")
	}
	// A D8 fail or inconclusive verdict must fail the script, not end in "images ok".
	if !regexp.MustCompile(`(?m)^\[ "\$d8" = 0 \] \|\| \{`).MatchString(s) {
		t.Error("build.sh must exit non-zero when a D8 run did not pass")
	}
}
