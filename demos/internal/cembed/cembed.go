// Package cembed writes the files of a directory as a C source file of
// byte arrays: the embedded Web UI of the logger demo (ADR-0028 §3,
// amendment C-083), without a build step at run time. The C file is
// committed; a test of the logger compares it with the files.
package cembed

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Command is how the logger's file is generated (in its header comment).
const Command = "go run ./internal/cembed/cmd/cembed -o logger/src/ui_assets.c logger/ui"

// Types are the content types by file extension.
var Types = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".svg":  "image/svg+xml",
}

// Generate returns the C file for the regular files directly in dir:
// lg_ui_assets (lg_asset of ui.h: path "/<name>", content type, data,
// size) sorted by name, and lg_ui_asset_count.
func Generate(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var b bytes.Buffer
	fmt.Fprintf(&b, "/* The files of the Web UI (ui/). Generated, do not edit:\n *   %s */\n", Command)
	b.WriteString("#include \"ui.h\"\n")
	for i, name := range names {
		if _, ok := Types[path.Ext(name)]; !ok {
			return nil, fmt.Errorf("%s: no content type for %q", name, path.Ext(name))
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "\n/* %s */\nstatic const unsigned char file%d[] = {", name, i)
		for j, c := range data {
			if j%16 == 0 {
				b.WriteString("\n   ")
			}
			fmt.Fprintf(&b, " 0x%02x,", c)
		}
		b.WriteString("\n};\n")
	}
	b.WriteString("\nconst lg_asset lg_ui_assets[] = {\n")
	for i, name := range names {
		fmt.Fprintf(&b, "    {\"/%s\", \"%s\", file%d, sizeof file%d},\n", name, Types[path.Ext(name)], i, i)
	}
	fmt.Fprintf(&b, "};\nconst size_t lg_ui_asset_count = %d;\n", len(names))
	return b.Bytes(), nil
}
