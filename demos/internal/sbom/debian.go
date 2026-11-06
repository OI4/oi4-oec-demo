package sbom

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

// DebianMap is the reviewed mapping of the Debian license short names
// (machine-readable copyright files, DEP-5) to SPDX identifiers
// (images/debian.json, D-1 of C-084).
type DebianMap struct {
	Comment string `json:"comment"`
	// SPDX maps a DEP-5 short name to an SPDX license expression; it
	// is used where the license is the one of the SPDX License List
	// (the Debian copyright file often only points to
	// /usr/share/common-licenses, which the base image does not hold).
	SPDX map[string]string `json:"spdx"`
	// Packages gives the license expression of a binary package whose
	// copyright file is not machine-readable (e.g. the GCC runtime).
	Packages map[string]string `json:"packages"`
}

// ParseDebianMap reads a DebianMap; unknown fields are refused.
func ParseDebianMap(data []byte) (*DebianMap, error) {
	var m DebianMap
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("debian map: %v", err)
	}
	return &m, nil
}

// Debian reads the packages of a Debian based image from its file tree
// root: var/lib/dpkg/status.d/<package> (distroless) or
// var/lib/dpkg/status, the copyright file usr/share/doc/<package>/copyright
// and etc/os-release (or usr/lib/os-release). It returns the packages and
// the texts of the LicenseRef- licenses it made: a DEP-5 short name that
// m.SPDX does not map becomes LicenseRef-<package>-<name> with the text of
// its stand-alone License paragraph (or of the License field of its Files
// paragraph) – exact, without judging equivalence (SF-114). A name
// without text, a copyright file that is not machine-readable and not in
// m.Packages and a combination of "and"/"or" that needs commas are refused.
func Debian(root fs.FS, m *DebianMap) ([]Component, map[string]string, error) {
	distro, err := osRelease(root)
	if err != nil {
		return nil, nil, err
	}
	stanzas, err := dpkgStatus(root)
	if err != nil {
		return nil, nil, err
	}
	var out []Component
	refs := map[string]string{}
	for _, s := range stanzas {
		pkg, version, arch := s["Package"], s["Version"], s["Architecture"]
		if pkg == "" || version == "" || arch == "" {
			return nil, nil, fmt.Errorf("dpkg status: a package without Package, Version or Architecture: %v", s)
		}
		c := Component{Kind: KindDebian, Name: pkg, Version: version,
			Purl: fmt.Sprintf("pkg:deb/%s/%s@%s?arch=%s&distro=%s", distro.id, purlEscape(pkg), purlEscape(version), arch, distro.id+"-"+distro.version)}
		if e, ok := m.Packages[pkg]; ok {
			c.License = e
		} else {
			data, err := fs.ReadFile(root, path.Join("usr/share/doc", pkg, "copyright"))
			if err != nil {
				return nil, nil, fmt.Errorf("package %s: %v", pkg, err)
			}
			if c.License, err = dep5(pkg, string(data), m, refs); err != nil {
				return nil, nil, fmt.Errorf("package %s: %v", pkg, err)
			}
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("no Debian package in the image")
	}
	return out, refs, nil
}

type release struct{ id, version string }

func osRelease(root fs.FS) (release, error) {
	data, err := fs.ReadFile(root, "etc/os-release")
	if err != nil {
		if data, err = fs.ReadFile(root, "usr/lib/os-release"); err != nil {
			return release{}, fmt.Errorf("os-release: %v", err)
		}
	}
	var r release
	for _, l := range strings.Split(string(data), "\n") {
		k, v, _ := strings.Cut(l, "=")
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			r.id = v
		case "VERSION_ID":
			r.version = v
		}
	}
	if r.id == "" || r.version == "" {
		return release{}, fmt.Errorf("os-release: no ID or VERSION_ID")
	}
	return r, nil
}

func dpkgStatus(root fs.FS) ([]map[string]string, error) {
	var texts []string
	names, err := fs.Glob(root, "var/lib/dpkg/status.d/*")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	for _, n := range names {
		if strings.HasSuffix(n, ".md5sums") {
			continue
		}
		data, err := fs.ReadFile(root, n)
		if err != nil {
			return nil, err
		}
		texts = append(texts, string(data))
	}
	if len(texts) == 0 {
		data, err := fs.ReadFile(root, "var/lib/dpkg/status")
		if err != nil {
			return nil, fmt.Errorf("dpkg status: %v", err)
		}
		texts = append(texts, string(data))
	}
	var out []map[string]string
	for _, t := range texts {
		for _, p := range paragraphs(t) {
			s := map[string]string{}
			for _, f := range p {
				s[f.name] = f.value
			}
			if st := s["Status"]; st != "" && !strings.HasSuffix(st, " installed") {
				continue
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["Package"] < out[j]["Package"] })
	return out, nil
}

type field struct{ name, value, body string }

// paragraphs splits a Debian control file (deb822) into paragraphs of
// fields: value is the first line, body the continuation lines with one
// leading space removed and " ." as empty line.
func paragraphs(text string) [][]field {
	var out [][]field
	var cur []field
	scan := bufio.NewScanner(strings.NewReader(text))
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	for scan.Scan() {
		l := scan.Text()
		switch {
		case strings.TrimSpace(l) == "":
			if len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
		case l[0] == ' ' || l[0] == '\t':
			if len(cur) == 0 {
				continue
			}
			b := l[1:]
			if strings.TrimSpace(b) == "." {
				b = ""
			}
			f := &cur[len(cur)-1]
			if f.body != "" || b != "" {
				f.body += b + "\n"
			}
		case l[0] == '#':
		default:
			n, v, _ := strings.Cut(l, ":")
			cur = append(cur, field{name: n, value: strings.TrimSpace(v)})
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func get(p []field, name string) (field, bool) {
	for _, f := range p {
		if strings.EqualFold(f.name, name) {
			return f, true
		}
	}
	return field{}, false
}

var dep5Op = regexp.MustCompile(`(?i)\s+(and|or)\s+`)

// dep5 converts the License fields of the Files paragraphs of a DEP-5
// copyright file into one SPDX expression (AND of the distinct Files
// licenses, in order of appearance) and adds the LicenseRef texts to refs.
func dep5(pkg, text string, m *DebianMap, refs map[string]string) (string, error) {
	if !strings.HasPrefix(text, "Format:") {
		return "", fmt.Errorf("the copyright file is not machine-readable (DEP-5): add the package to \"packages\" of the Debian map")
	}
	ps := paragraphs(text)
	standalone := map[string]string{}
	for _, p := range ps[1:] {
		if _, files := get(p, "Files"); files {
			continue
		}
		if l, ok := get(p, "License"); ok && strings.TrimSpace(l.body) != "" {
			standalone[l.value] = l.body
		}
	}
	var exprs []string
	seen := map[string]bool{}
	for _, p := range ps[1:] {
		if _, files := get(p, "Files"); !files {
			continue
		}
		l, ok := get(p, "License")
		if !ok || l.value == "" {
			return "", fmt.Errorf("a Files paragraph without License")
		}
		if strings.Contains(l.value, ",") || regexp.MustCompile(`(?i)\swith\s`).MatchString(l.value) {
			return "", fmt.Errorf("license %q: commas and \"with\" are not supported, map it in \"spdx\" or \"packages\"", l.value)
		}
		names := dep5Op.Split(l.value, -1)
		ops := dep5Op.FindAllStringSubmatch(l.value, -1)
		var and, or bool
		var b strings.Builder
		for i, n := range names {
			id, err := dep5License(pkg, n, l, len(names) == 1, standalone, m, refs)
			if err != nil {
				return "", err
			}
			if i > 0 {
				op := strings.ToUpper(ops[i-1][1])
				and, or = and || op == "AND", or || op == "OR"
				b.WriteString(" " + op + " ")
			}
			b.WriteString(id)
		}
		if and && or {
			return "", fmt.Errorf("license %q mixes \"and\" and \"or\": map it in \"spdx\" or \"packages\"", l.value)
		}
		e := b.String()
		terms := []string{e}
		if or && len(names) > 1 {
			terms = []string{"(" + e + ")"}
		} else if and {
			terms = splitAnd(e)
		}
		for _, t := range terms {
			if !seen[t] {
				seen[t] = true
				exprs = append(exprs, t)
			}
		}
	}
	if len(exprs) == 0 {
		return "", fmt.Errorf("no Files paragraph with a license")
	}
	if len(exprs) == 1 {
		return strings.TrimSuffix(strings.TrimPrefix(exprs[0], "("), ")"), nil
	}
	return strings.Join(exprs, " AND "), nil
}

func dep5License(pkg, name string, l field, single bool, standalone map[string]string, m *DebianMap, refs map[string]string) (string, error) {
	if e, ok := m.SPDX[name]; ok {
		if strings.ContainsAny(e, " ()") {
			return "(" + e + ")", nil
		}
		return e, nil
	}
	text := standalone[name]
	if text == "" && single {
		text = l.body
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("license %q: no SPDX mapping and no text in the copyright file", name)
	}
	id := "LicenseRef-" + RefID(pkg) + "-" + RefID(name)
	// the same name with different inline texts (e.g. public-domain of
	// several Files paragraphs): the LicenseRef holds all of them
	if old, ok := refs[id]; ok && !strings.Contains(old, text) {
		text = old + "\n" + text
	} else if ok {
		text = old
	}
	refs[id] = text
	return id, nil
}

// splitAnd splits an expression at its top-level AND operators.
func splitAnd(e string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(e); i++ {
		switch {
		case e[i] == '(':
			depth++
		case e[i] == ')':
			depth--
		case depth == 0 && strings.HasPrefix(e[i:], " AND "):
			out = append(out, e[start:i])
			start = i + len(" AND ")
			i += len(" AND ") - 1
		}
	}
	return append(out, e[start:])
}
