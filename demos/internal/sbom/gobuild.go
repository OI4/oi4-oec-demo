package sbom

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Go reads the output of "go version -m <binary>": the Go standard
// library (pkg:golang/stdlib, BSD-3-Clause) and every module dependency.
// The build information names no licenses: a module replaced by a local
// directory belongs to this workspace and gets localLicense; any other
// module is refused until its license is known (fail closed). The main
// module is the application itself and is left out.
func Go(r io.Reader, localLicense string) ([]Component, error) {
	return GoKnown(r, localLicense, nil)
}

// GoKnown is Go with the reviewed licenses of third-party modules (C-107,
// the tool image of oec/conformance): known maps "<module>@<version>" to an
// SPDX license expression (ParseGoLicenses). A module of another version
// or not in the map is refused as in Go.
func GoKnown(r io.Reader, localLicense string, known map[string]string) ([]Component, error) {
	var out []Component
	scan := bufio.NewScanner(r)
	first := true
	var dep *Component
	flush := func() error {
		if dep == nil {
			return nil
		}
		if dep.License == "" {
			dep.License = known[dep.Name+"@"+dep.Version]
		}
		if dep.License == "" {
			return fmt.Errorf("Go module %s %s: license unknown (neither a module of this workspace nor in the Go license map)", dep.Name, dep.Version)
		}
		out = append(out, *dep)
		dep = nil
		return nil
	}
	for scan.Scan() {
		line := scan.Text()
		if first {
			first = false
			i := strings.LastIndex(line, ": go")
			if i < 0 {
				return nil, fmt.Errorf("go version -m: no Go version in %q", line)
			}
			v := strings.TrimPrefix(strings.TrimSpace(line[i+2:]), "go")
			out = append(out, Component{Kind: KindGo, Name: "stdlib", Version: "go" + v,
				License: "BSD-3-Clause", Purl: "pkg:golang/stdlib@" + v})
			continue
		}
		f := strings.Split(strings.TrimPrefix(line, "\t"), "\t")
		switch f[0] {
		case "dep":
			if err := flush(); err != nil {
				return nil, err
			}
			if len(f) < 3 {
				return nil, fmt.Errorf("go version -m: bad dep line %q", line)
			}
			dep = &Component{Kind: KindGo, Name: f[1], Version: f[2], Purl: "pkg:golang/" + f[1] + "@" + f[2]}
		case "=>":
			if dep == nil || len(f) < 2 {
				return nil, fmt.Errorf("go version -m: replacement without dep: %q", line)
			}
			if strings.HasPrefix(f[1], ".") || strings.HasPrefix(f[1], "/") {
				dep.License = localLicense
			} else if len(f) >= 3 {
				dep.Name, dep.Version, dep.Purl = f[1], f[2], "pkg:golang/"+f[1]+"@"+f[2]
			}
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if first {
		return nil, fmt.Errorf("go version -m: empty")
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// ParseGoLicenses reads a Go license map (e.g. oec/conformance/images/
// go-licenses.json): {"comment": "…", "modules": {"<module>@<version>":
// "<SPDX expression>"}}. Every key names a version and every expression
// parses (IDs); NOASSERTION and NONE are refused.
func ParseGoLicenses(data []byte) (map[string]string, error) {
	var doc struct {
		Modules map[string]string `json:"modules"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("Go license map: %v", err)
	}
	if len(doc.Modules) == 0 {
		return nil, fmt.Errorf("Go license map: no modules")
	}
	for k, e := range doc.Modules {
		if i := strings.LastIndex(k, "@v"); i <= 0 {
			return nil, fmt.Errorf("Go license map: %q is not <module>@<version>", k)
		}
		if _, err := IDs(e); err != nil {
			return nil, fmt.Errorf("Go license map: %s: %v", k, err)
		}
	}
	return doc.Modules, nil
}

// GoList reads the Go modules of a binary or library from the output of
//
//	go list -deps -f '{{with .Module}}{{.Path}}|{{.Version}}|{{with .Replace}}{{.Path}}|{{.Version}}{{end}}{{end}}' <packages>
//
// one line per package (C-108, the SBOMs of oec/ci/sbom.sh): a package of
// the standard library gives an empty line, a package of the main module
// "<path>||". It needs no build, so it also serves a library and a cgo
// binary whose C library is not built. The main module is the application
// and is left out; the licenses follow GoKnown: a module replaced by a
// local directory gets localLicense, any other module the license of known
// for exactly its version (fail closed).
func GoList(r io.Reader, localLicense string, known map[string]string) ([]Component, error) {
	var out []Component
	seen := map[string]bool{}
	main := ""
	scan := bufio.NewScanner(r)
	for n := 1; scan.Scan(); n++ {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 3 && len(f) != 4 || f[0] == "" {
			return nil, fmt.Errorf("go list line %d: not <module>|<version>|[<replacement>|<version>]: %q", n, line)
		}
		if f[1] == "" && f[2] == "" {
			if main != "" && main != f[0] {
				return nil, fmt.Errorf("go list line %d: a second main module %s (%s)", n, f[0], main)
			}
			main = f[0]
			continue
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		c := Component{Kind: KindGo, Name: f[0], Version: f[1]}
		switch {
		case f[1] == "":
			return nil, fmt.Errorf("go list line %d: module %s without version", n, f[0])
		case f[2] == "":
		case strings.HasPrefix(f[2], ".") || strings.HasPrefix(f[2], "/"):
			c.License = localLicense
		case len(f) == 4 && f[3] != "":
			c.Name, c.Version = f[2], f[3]
		default:
			return nil, fmt.Errorf("go list line %d: replacement %s of %s without version", n, f[2], f[0])
		}
		if c.License == "" {
			c.License = known[c.Name+"@"+c.Version]
		}
		if c.License == "" {
			return nil, fmt.Errorf("Go module %s %s: license unknown (neither a module of this workspace nor in the Go license map)", c.Name, c.Version)
		}
		c.Purl = "pkg:golang/" + c.Name + "@" + c.Version
		out = append(out, c)
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if main == "" {
		return nil, fmt.Errorf("go list: no main module (empty output or not go list -deps)")
	}
	return out, nil
}

// goVersion is a released Go version as go env GOVERSION prints it.
var goVersion = regexp.MustCompile(`^(go)?([0-9]+\.[0-9]+(\.[0-9]+)?)$`)

// GoStd is the Go standard library linked into a Go binary (C-108), from
// the version of its toolchain ("go1.27.3" or "1.27.3").
func GoStd(version string) (Component, error) {
	m := goVersion.FindStringSubmatch(version)
	if m == nil {
		return Component{}, fmt.Errorf("Go version %q: not a released version like go1.27.3", version)
	}
	return Component{Kind: KindGo, Name: "stdlib", Version: "go" + m[2], License: "BSD-3-Clause",
		Purl: "pkg:golang/stdlib@" + m[2]}, nil
}
