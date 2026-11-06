// Command oec-sbom makes the SBOM (SPDX 2.3 JSON) and the license texts
// of a demo, registry or tool image for /opt/oi4/licenses (ADR-0028 §4,
// RIG 4.1.4, SF-114), and of every other deliverable with a license report
// (oec/ci/sbom.sh, C-108). It runs in the build stage of the Dockerfiles:
//
//	oec-sbom -name oec-logger -version 0.1.0 -license MIT \
//	    -cargo cargo-tree.txt [-go go-version.txt [-golicenses map.json]] \
//	    [-rust 1.89.0] \
//	    [-npm ui/package-lock.json] \
//	    -base /base -debian images/debian.json -texts images/licenses \
//	    -out /licenses
//
// and in oec/ci/sbom.sh without a base image, the Go modules from go list
// instead of a built binary:
//
//	oec-sbom -name oec-check -version 0.1.0 \
//	    -golist go-list.txt -golicenses map.json -gostd go1.27.3 \
//	    -texts images/licenses -out dist/sbom/oec-check \
//	    -report dist/sbom/oec-check/LICENSES.md
//
// -cargo is the output of cargo tree --edges normal,no-proc-macro
// --target <triple> -p <crate> --prefix none --format '{p}|{l}', -go the
// output of go version -m <binary>, -golicenses the reviewed licenses of
// its third-party modules (oec/conformance/images/go-licenses.json, the
// tool image, C-107), -npm the package-lock.json of a Web UI
// whose bundle is embedded in the binary (its non-dev packages, the
// registry image, C-090), -base the file tree of the base image (dpkg
// status, copyright files, os-release; with -debian, only for an image),
// -golist the output of go list -deps with the format of sbom.GoList,
// -gostd the Go version of a Go binary (its standard library), -report the
// file of the license report (Markdown). The creation time is
// SOURCE_DATE_EPOCH if set (reproducible builds), else now.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/OI4/oec/demos/internal/sbom"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "oec-sbom:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fl := flag.NewFlagSet("oec-sbom", flag.ContinueOnError)
	name := fl.String("name", "", "name of the application (e.g. oec-logger)")
	version := fl.String("version", "", "version of the application")
	license := fl.String("license", "MIT", "license of the application and of the local crates and Go modules")
	cargo := fl.String("cargo", "", "cargo tree output (optional)")
	gov := fl.String("go", "", "go version -m output (optional)")
	goLicenses := fl.String("golicenses", "", "license map of third-party Go modules (optional, with -go)")
	rust := fl.String("rust", "", "version of the Rust standard library linked in (optional)")
	npm := fl.String("npm", "", "package-lock.json of an embedded Web UI bundle (optional)")
	golist := fl.String("golist", "", "go list -deps output of the Go packages (optional, sbom.GoList)")
	gostd := fl.String("gostd", "", "Go version of a Go binary, its standard library (optional, e.g. go1.27.3)")
	base := fl.String("base", "", "file tree of the base image (Debian; optional, with -debian)")
	debian := fl.String("debian", "", "Debian license map (images/debian.json; optional, with -base)")
	texts := fl.String("texts", "", "directory of the SPDX license texts <id>.txt")
	out := fl.String("out", "", "output directory (/opt/oi4/licenses of the image)")
	reportFile := fl.String("report", "", "file of the license report (Markdown, optional)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	for f, v := range map[string]string{"name": *name, "version": *version, "texts": *texts, "out": *out} {
		if v == "" {
			return fmt.Errorf("-%s is required", f)
		}
	}
	if (*base == "") != (*debian == "") {
		return fmt.Errorf("-base and -debian go together (the base image of an image)")
	}
	in := sbom.Input{App: sbom.App(*name, *version, *license), Texts: os.DirFS(*texts), Created: time.Now()}
	if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
		sec, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("SOURCE_DATE_EPOCH: %v", err)
		}
		in.Created = time.Unix(sec, 0)
	}
	if *cargo != "" {
		f, err := os.Open(*cargo)
		if err != nil {
			return err
		}
		cs, err := sbom.Cargo(f)
		f.Close()
		if err != nil {
			return err
		}
		in.Components = append(in.Components, cs...)
	}
	var known map[string]string
	if *goLicenses != "" {
		data, err := os.ReadFile(*goLicenses)
		if err != nil {
			return err
		}
		if known, err = sbom.ParseGoLicenses(data); err != nil {
			return err
		}
	}
	if *gov != "" {
		f, err := os.Open(*gov)
		if err != nil {
			return err
		}
		cs, err := sbom.GoKnown(f, *license, known)
		f.Close()
		if err != nil {
			return err
		}
		in.Components = append(in.Components, cs...)
	}
	if *golist != "" {
		f, err := os.Open(*golist)
		if err != nil {
			return err
		}
		cs, err := sbom.GoList(f, *license, known)
		f.Close()
		if err != nil {
			return err
		}
		in.Components = append(in.Components, cs...)
	}
	if *gostd != "" {
		c, err := sbom.GoStd(*gostd)
		if err != nil {
			return err
		}
		in.Components = append(in.Components, c)
	}
	if *npm != "" {
		f, err := os.Open(*npm)
		if err != nil {
			return err
		}
		cs, err := sbom.Npm(f)
		f.Close()
		if err != nil {
			return err
		}
		in.Components = append(in.Components, cs...)
	}
	if *rust != "" {
		in.Components = append(in.Components, sbom.RustStd(*rust))
	}
	if *base != "" {
		data, err := os.ReadFile(*debian)
		if err != nil {
			return err
		}
		m, err := sbom.ParseDebianMap(data)
		if err != nil {
			return err
		}
		debs, refs, err := sbom.Debian(os.DirFS(*base), m)
		if err != nil {
			return err
		}
		in.Components, in.Refs = append(in.Components, debs...), refs
	}
	res, err := sbom.Make(in)
	if err != nil {
		return err
	}
	if err := res.Write(*out); err != nil {
		return err
	}
	if *reportFile != "" {
		if err := os.WriteFile(*reportFile, res.Report, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("oec-sbom: %s with %d license texts in %s\n", sbom.SPDXFile, len(res.Texts), *out)
	return nil
}
