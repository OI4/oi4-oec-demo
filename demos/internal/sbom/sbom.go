// Package sbom makes the SBOM (SPDX 2.3 JSON) and the license texts of a
// demo image for /opt/oi4/licenses (ADR-0028 §4, RIG 4.1.4, SF-114):
// every component with name, version, license expression and purl; the
// text of every license and license exception as <id>.txt. Inputs are the
// crates of the binary (cargo tree), its Go build information (go version
// -m), the npm packages of a bundled Web UI (package-lock.json), the Rust
// standard library and the Debian packages of the base image. Standard library only; the command is cmd/oec-sbom.
package sbom

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind orders the components of the SBOM.
type Kind int

// The kinds of components.
const (
	KindApp Kind = iota
	KindCargo
	KindGo
	KindNpm
	KindRust
	KindDebian
)

// Component is one software component of the image.
type Component struct {
	Kind    Kind
	Name    string
	Version string
	License string // SPDX license expression
	Purl    string
}

// RustStd is the Rust standard library linked into a Rust binary.
func RustStd(version string) Component {
	return Component{Kind: KindRust, Name: "rust-std", Version: version,
		License: "MIT OR Apache-2.0", Purl: "pkg:generic/rust-lang/rust-std@" + version}
}

// App is the application itself.
func App(name, version, license string) Component {
	return Component{Kind: KindApp, Name: name, Version: version, License: license,
		Purl: "pkg:generic/oi4/" + name + "@" + version}
}

// Input is everything one SBOM is made of.
type Input struct {
	App        Component
	Components []Component
	// Refs are the texts of LicenseRef- licenses (from Debian).
	Refs map[string]string
	// Texts holds the SPDX license texts as <id>.txt.
	Texts fs.FS
	// Created is the creation time of the document.
	Created time.Time
}

// SPDX file and document constants.
const (
	SPDXFile = "sbom.spdx.json"
	creator  = "Tool: oec-sbom"
	org      = "Organization: OI4 Alliance"
	nsBase   = "https://github.com/OI4/oec/spdx/"
)

type spdxRef struct {
	Category string `json:"referenceCategory"`
	Type     string `json:"referenceType"`
	Locator  string `json:"referenceLocator"`
}

type spdxPackage struct {
	SPDXID           string    `json:"SPDXID"`
	Name             string    `json:"name"`
	VersionInfo      string    `json:"versionInfo"`
	DownloadLocation string    `json:"downloadLocation"`
	FilesAnalyzed    bool      `json:"filesAnalyzed"`
	LicenseConcluded string    `json:"licenseConcluded"`
	LicenseDeclared  string    `json:"licenseDeclared"`
	CopyrightText    string    `json:"copyrightText"`
	Purpose          string    `json:"primaryPackagePurpose,omitempty"`
	ExternalRefs     []spdxRef `json:"externalRefs"`
}

type spdxExtracted struct {
	LicenseID     string `json:"licenseId"`
	Name          string `json:"name"`
	ExtractedText string `json:"extractedText"`
}

type spdxRelationship struct {
	Element string `json:"spdxElementId"`
	Type    string `json:"relationshipType"`
	Related string `json:"relatedSpdxElement"`
}

type spdxDoc struct {
	SPDXVersion       string `json:"spdxVersion"`
	DataLicense       string `json:"dataLicense"`
	SPDXID            string `json:"SPDXID"`
	Name              string `json:"name"`
	DocumentNamespace string `json:"documentNamespace"`
	CreationInfo      struct {
		Created  string   `json:"created"`
		Creators []string `json:"creators"`
	} `json:"creationInfo"`
	Packages      []spdxPackage      `json:"packages"`
	Extracted     []spdxExtracted    `json:"hasExtractedLicensingInfos,omitempty"`
	Relationships []spdxRelationship `json:"relationships"`
}

// Result is a made SBOM: the document, the license texts by id and the
// license report.
type Result struct {
	SPDX  []byte
	Texts map[string][]byte // <id> → text of <id>.txt
	// Report is the license report in Markdown (C-108): Write does not
	// store it, oec-sbom -report does.
	Report []byte
}

// kindNames are the kinds of components as the license report names them.
var kindNames = map[Kind]string{KindApp: "application", KindCargo: "cargo", KindGo: "go", KindNpm: "npm", KindRust: "rust", KindDebian: "debian"}

// Make builds the SBOM. It fails if a component has no name, version,
// license or purl, if an expression is invalid or if the text of a
// license or license exception is missing.
func Make(in Input) (*Result, error) {
	comps := append([]Component{in.App}, dedupe(in.App, in.Components)...)
	sort.SliceStable(comps[1:], func(i, j int) bool {
		a, b := comps[1+i], comps[1+j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Version < b.Version
	})
	res := &Result{Texts: map[string][]byte{}}
	var doc spdxDoc
	doc.SPDXVersion, doc.DataLicense, doc.SPDXID = "SPDX-2.3", "CC0-1.0", "SPDXRef-DOCUMENT"
	doc.Name = in.App.Name + "-" + in.App.Version
	doc.CreationInfo.Created = in.Created.UTC().Format(time.RFC3339)
	doc.CreationInfo.Creators = []string{creator, org}
	ids := map[string]bool{}
	var refs []string
	for i, c := range comps {
		if c.Name == "" || c.Version == "" || c.Purl == "" {
			return nil, fmt.Errorf("component %d (%s %s): name, version and purl are required", i, c.Name, c.Version)
		}
		lids, err := IDs(c.License)
		if err != nil {
			return nil, fmt.Errorf("component %s %s: %v", c.Name, c.Version, err)
		}
		for _, id := range lids {
			if _, done := res.Texts[id]; done {
				continue
			}
			var text []byte
			if strings.HasPrefix(id, "LicenseRef-") {
				t, ok := in.Refs[id]
				if !ok {
					return nil, fmt.Errorf("component %s %s: no text for %s", c.Name, c.Version, id)
				}
				text = []byte(t)
				refs = append(refs, id)
			} else if text, err = fs.ReadFile(in.Texts, id+".txt"); err != nil {
				return nil, fmt.Errorf("component %s %s: no text for %s: add images/licenses/%s.txt (SPDX License List)", c.Name, c.Version, id, id)
			}
			if strings.TrimSpace(string(text)) == "" {
				return nil, fmt.Errorf("the text of %s is empty", id)
			}
			res.Texts[id] = text
		}
		sid := "SPDXRef-Package-" + RefID(c.Name+"-"+c.Version)
		for n := 2; ids[sid]; n++ {
			sid = fmt.Sprintf("SPDXRef-Package-%s-%d", RefID(c.Name+"-"+c.Version), n)
		}
		ids[sid] = true
		p := spdxPackage{SPDXID: sid, Name: c.Name, VersionInfo: c.Version, DownloadLocation: "NOASSERTION",
			LicenseConcluded: c.License, LicenseDeclared: c.License, CopyrightText: "NOASSERTION",
			ExternalRefs: []spdxRef{{"PACKAGE-MANAGER", "purl", c.Purl}}}
		if c.Kind == KindApp {
			p.Purpose = "APPLICATION"
			doc.Relationships = append(doc.Relationships, spdxRelationship{"SPDXRef-DOCUMENT", "DESCRIBES", sid})
		} else {
			doc.Relationships = append(doc.Relationships, spdxRelationship{doc.Packages[0].SPDXID, "DEPENDS_ON", sid})
		}
		doc.Packages = append(doc.Packages, p)
	}
	sort.Strings(refs)
	for _, id := range refs {
		doc.Extracted = append(doc.Extracted, spdxExtracted{LicenseID: id, Name: strings.TrimPrefix(id, "LicenseRef-"), ExtractedText: in.Refs[id]})
	}
	// the namespace is unique per content: a hash of the packages
	sum, err := json.Marshal(doc.Packages)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(sum)
	doc.DocumentNamespace = nsBase + doc.Name + "-" + hex.EncodeToString(h[:8])
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // purl qualifiers keep their &
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	res.SPDX = b.Bytes()
	res.Report = report(in.App, comps)
	return res, nil
}

// report is the license report of the components (checked by Make): the
// licenses with the number of components that name them and their text
// files, then the components in the order of the SBOM.
func report(app Component, comps []Component) []byte {
	count := map[string]int{}
	for _, c := range comps {
		ids, _ := IDs(c.License)
		for _, id := range ids {
			count[id]++
		}
	}
	ids := make([]string, 0, len(count))
	for id := range count {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	cell := func(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
	var b bytes.Buffer
	fmt.Fprintf(&b, "# License report: %s %s\n\n", app.Name, app.Version)
	fmt.Fprintf(&b, "The SBOM `%s` (SPDX 2.3) of %s %s lists %d components, the application included; the text of every license is next to it as `<id>.txt`. Made by oec-sbom (ADR-0028 §4, amendment C-108).\n\n", SPDXFile, app.Name, app.Version, len(comps))
	b.WriteString("## Licenses\n\n| License | Components | Text |\n|---|---:|---|\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "| %s | %d | `%s.txt` |\n", id, count[id], id)
	}
	b.WriteString("\n## Components\n\n| Kind | Name | Version | License | purl |\n|---|---|---|---|---|\n")
	for _, c := range comps {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | `%s` |\n", kindNames[c.Kind], cell(c.Name), cell(c.Version), cell(c.License), c.Purl)
	}
	return b.Bytes()
}

// dedupe drops repeated components and the application itself (e.g. the
// root crate of a Rust application).
func dedupe(app Component, cs []Component) []Component {
	seen := map[string]bool{}
	var out []Component
	for _, c := range cs {
		if seen[c.Purl] || c.Name == app.Name && c.Version == app.Version {
			continue
		}
		seen[c.Purl] = true
		out = append(out, c)
	}
	return out
}

// Write stores the SBOM and the license texts in dir (created if
// missing), which must not hold an SBOM yet.
func (r *Result) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, SPDXFile)); err == nil {
		return fmt.Errorf("%s exists already", filepath.Join(dir, SPDXFile))
	}
	for id, t := range r.Texts {
		if err := os.WriteFile(filepath.Join(dir, id+".txt"), t, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, SPDXFile), r.SPDX, 0o644)
}
