package sbom

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Cargo reads the crates linked into a binary from the output of
//
//	cargo tree --edges normal,no-proc-macro --target <triple> -p <crate> \
//	    --prefix none --format '{p}|{l}'
//
// one line "<name> v<version>[ (<source>)][ (*)]|<license>" per crate.
// cargo tree resolves the features like the build; the resolve graph of
// cargo metadata also holds optional dependencies that are not built
// (e.g. ring next to aws-lc-rs), so it is not used (C-084). Build
// dependencies and proc macros run only at build time and are left out.
// A crate of crates.io gets the purl pkg:cargo/<name>@<version>, a local
// crate (source is a path) pkg:generic/oi4/<name>@<version>; any other
// source (git, other registries) is refused.
func Cargo(r io.Reader) ([]Component, error) {
	var out []Component
	seen := map[string]bool{}
	scan := bufio.NewScanner(r)
	for n := 1; scan.Scan(); n++ {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		pkg, lic, ok := strings.Cut(line, "|")
		if !ok {
			return nil, fmt.Errorf("cargo tree line %d: no '|': %q", n, line)
		}
		pkg = strings.TrimSuffix(strings.TrimSpace(pkg), " (*)")
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		name, rest, ok := strings.Cut(pkg, " v")
		if !ok || name == "" {
			return nil, fmt.Errorf("cargo tree line %d: no '<name> v<version>': %q", n, line)
		}
		version, source, _ := strings.Cut(rest, " ")
		if version == "" || strings.ContainsAny(version, "()") {
			return nil, fmt.Errorf("cargo tree line %d: no version: %q", n, line)
		}
		c := Component{Kind: KindCargo, Name: name, Version: version, License: Normalize(lic)}
		switch {
		case source == "":
			c.Purl = "pkg:cargo/" + name + "@" + purlEscape(version)
		case strings.HasPrefix(source, "(/") && strings.HasSuffix(source, ")"):
			c.Purl = "pkg:generic/oi4/" + name + "@" + purlEscape(version)
		default:
			return nil, fmt.Errorf("cargo tree line %d: crate %s %s from %s: only crates.io and local crates are supported", n, name, version, source)
		}
		if c.License == "" {
			return nil, fmt.Errorf("crate %s %s states no license", name, version)
		}
		out = append(out, c)
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("cargo tree: no crate")
	}
	return out, nil
}
