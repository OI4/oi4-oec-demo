// Command cembed writes the files of a directory as a C source file (see
// package cembed). Run from oec/demos:
//
//	go run ./internal/cembed/cmd/cembed -o logger/src/ui_assets.c logger/ui
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/OI4/oec/demos/internal/cembed"
)

func main() {
	out := flag.String("o", "", "the C file to write")
	flag.Parse()
	if *out == "" || flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: cembed -o file.c dir")
		os.Exit(2)
	}
	data, err := cembed.Generate(flag.Arg(0))
	if err == nil {
		err = os.WriteFile(*out, data, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cembed:", err)
		os.Exit(1)
	}
}
