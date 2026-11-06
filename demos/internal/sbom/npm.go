package sbom

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Npm reads the npm packages shipped in a bundled Web UI from its
// package-lock.json (lockfile version 2 or 3, "packages"): every package
// below node_modules/ that is not "dev" – the build tools are "dev", the
// code the bundler copies into the bundle is not (the same rule as the UI
// policy of the registry, ADR-0029 §10). The root package is the
// application itself and is left out. A package gets the purl
// pkg:npm/<name>@<version> (a scope as %40scope); a package without a
// string license, a link or a package outside node_modules/ is refused
// (fail closed).
func Npm(r io.Reader) ([]Component, error) {
	var lock struct {
		LockfileVersion int `json:"lockfileVersion"`
		Packages        map[string]struct {
			Version string `json:"version"`
			License any    `json:"license"`
			Dev     bool   `json:"dev"`
			Link    bool   `json:"link"`
		} `json:"packages"`
	}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&lock); err != nil {
		return nil, fmt.Errorf("package-lock.json: %v", err)
	}
	if lock.LockfileVersion < 2 || lock.Packages == nil {
		return nil, fmt.Errorf("package-lock.json: lockfile version %d without \"packages\" (npm 7 or later writes version 2 or 3)", lock.LockfileVersion)
	}
	keys := make([]string, 0, len(lock.Packages))
	for k := range lock.Packages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Component
	for _, k := range keys {
		e := lock.Packages[k]
		if k == "" || e.Dev {
			continue
		}
		i := strings.LastIndex(k, "node_modules/")
		if !strings.HasPrefix(k, "node_modules/") || e.Link {
			return nil, fmt.Errorf("package-lock.json: %s is not a package of node_modules", k)
		}
		name := k[i+len("node_modules/"):]
		if name == "" || e.Version == "" {
			return nil, fmt.Errorf("package-lock.json: %s has no name or version", k)
		}
		lic, ok := e.License.(string)
		if !ok || strings.TrimSpace(lic) == "" {
			return nil, fmt.Errorf("npm package %s %s states no license", name, e.Version)
		}
		purl := "pkg:npm/" + purlEscape(name) + "@" + purlEscape(e.Version)
		if scope, base, ok := strings.Cut(name, "/"); ok {
			purl = "pkg:npm/" + purlEscape(scope) + "/" + purlEscape(base) + "@" + purlEscape(e.Version)
		}
		out = append(out, Component{Kind: KindNpm, Name: name, Version: e.Version, License: Normalize(lic), Purl: purl})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("package-lock.json: no shipped package")
	}
	return out, nil
}
