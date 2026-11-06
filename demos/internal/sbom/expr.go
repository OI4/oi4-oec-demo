package sbom

import (
	"fmt"
	"regexp"
	"strings"
)

// licenseID is an SPDX license or exception identifier, a LicenseRef- or
// DocumentRef- reference; "+" (or later) is allowed as suffix.
var licenseID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*\+?$`)

// refID is the idstring of a LicenseRef-/SPDXRef- identifier (SPDX 2.3).
var refID = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

// Normalize writes the old Cargo form "MIT/Apache-2.0" as SPDX expression
// "MIT OR Apache-2.0" and collapses white space.
func Normalize(expr string) string {
	expr = strings.ReplaceAll(expr, "/", " OR ")
	return strings.Join(strings.Fields(expr), " ")
}

// IDs checks the SPDX license expression expr and returns its license
// and exception identifiers in order of appearance, each once. NONE,
// NOASSERTION and an empty expression state no license (SF-114).
func IDs(expr string) ([]string, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, fmt.Errorf("no license stated")
	}
	toks := strings.Fields(strings.NewReplacer("(", " ( ", ")", " ) ").Replace(expr))
	var ids []string
	seen := map[string]bool{}
	depth, operand := 0, true // operand: an id or "(" is expected
	for _, t := range toks {
		switch {
		case t == "(":
			if !operand {
				return nil, fmt.Errorf("%q: unexpected (", expr)
			}
			depth++
		case t == ")":
			if operand || depth == 0 {
				return nil, fmt.Errorf("%q: unexpected )", expr)
			}
			depth--
		case t == "AND" || t == "OR" || t == "WITH":
			if operand {
				return nil, fmt.Errorf("%q: unexpected %s", expr, t)
			}
			operand = true
		case t == "NONE" || t == "NOASSERTION":
			return nil, fmt.Errorf("%q: %s states no license", expr, t)
		default:
			if !operand {
				return nil, fmt.Errorf("%q: operator expected before %q", expr, t)
			}
			if !licenseID.MatchString(t) {
				return nil, fmt.Errorf("%q: %q is no license identifier", expr, t)
			}
			if !seen[t] {
				seen[t] = true
				ids = append(ids, t)
			}
			operand = false
		}
	}
	if operand || depth != 0 {
		return nil, fmt.Errorf("%q: incomplete expression", expr)
	}
	return ids, nil
}

// RefID makes s usable inside a LicenseRef-/SPDXRef- identifier: "+"
// becomes "plus", every other character outside [A-Za-z0-9.-] a "-".
func RefID(s string) string {
	return strings.Trim(refID.ReplaceAllString(strings.ReplaceAll(s, "+", "plus"), "-"), "-")
}

// purlEscape percent-encodes a purl component: every octet outside the
// unreserved characters A-Z a-z 0-9 . - _ ~ (e.g. "+" and the ":" of a
// Debian epoch).
func purlEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte(".-_~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
