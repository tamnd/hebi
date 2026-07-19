//go:build ignore

// Command gen fills the pinned Unicode category tables the runtime shim uses to
// classify a rune. The tables come straight from the standard unicode package,
// so they carry Go's Unicode version rather than the host Python's, which drifts
// by CPython release and would otherwise break the byte-exact match against go
// run. It rewrites the block between the BEGIN and END markers in _hebirt.py in
// place, leaving the single embedded shim file intact. Run it with go generate
// ./pkg/shim/... and commit the result.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"
)

const (
	beginMark = "# BEGIN GENERATED UNICODE TABLES"
	endMark   = "# END GENERATED UNICODE TABLES"
)

// shimPath resolves _hebirt.py from this source file's own location, so the
// generator writes the right file whatever the working directory go generate
// runs it from.
func shimPath() string {
	_, self, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(self), "..", "_hebirt.py")
}

// flat turns a RangeTable into a flat list of low, high, stride triples over the
// whole code space, merging the 16-bit and 32-bit halves so the shim searches
// one array. The ranges stay sorted by low bound, which the shim's binary search
// relies on.
func flat(t *unicode.RangeTable) []uint32 {
	type rng struct{ lo, hi, stride uint32 }
	var rs []rng
	for _, r := range t.R16 {
		rs = append(rs, rng{uint32(r.Lo), uint32(r.Hi), uint32(r.Stride)})
	}
	for _, r := range t.R32 {
		rs = append(rs, rng{r.Lo, r.Hi, r.Stride})
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].lo < rs[j].lo })
	out := make([]uint32, 0, len(rs)*3)
	for _, r := range rs {
		out = append(out, r.lo, r.hi, r.stride)
	}
	return out
}

func pyTuple(name string, t *unicode.RangeTable) string {
	nums := flat(t)
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return name + " = (" + strings.Join(parts, ", ") + ")"
}

func block() string {
	tables := []struct {
		name  string
		table *unicode.RangeTable
	}{
		{"_U_LETTER", unicode.L},
		{"_U_DIGIT", unicode.Nd},
		{"_U_NUMBER", unicode.N},
		{"_U_UPPER", unicode.Upper},
		{"_U_LOWER", unicode.Lower},
		{"_U_PUNCT", unicode.P},
	}
	var b strings.Builder
	b.WriteString(beginMark + "\n")
	fmt.Fprintf(&b, "# Pinned from Go's unicode package, version %s, so a rune classifies the\n", unicode.Version)
	b.WriteString("# same as go run whatever the host Python's unicodedata version is. Each table\n")
	b.WriteString("# is a flat run of low, high, stride triples sorted by low bound for the binary\n")
	b.WriteString("# search in _in_ranges. Regenerate with go generate ./pkg/shim/...\n")
	fmt.Fprintf(&b, "_UNICODE_VERSION = %q\n", unicode.Version)
	for _, t := range tables {
		b.WriteString(pyTuple(t.name, t.table))
		b.WriteString("\n")
	}
	b.WriteString(endMark)
	return b.String()
}

func main() {
	src, err := os.ReadFile(shimPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	text := string(src)
	start := strings.Index(text, beginMark)
	end := strings.Index(text, endMark)
	if start < 0 || end < 0 || end < start {
		fmt.Fprintf(os.Stderr, "markers %q and %q not found in %s\n", beginMark, endMark, shimPath())
		os.Exit(1)
	}
	out := text[:start] + block() + text[end+len(endMark):]
	if err := os.WriteFile(shimPath(), []byte(out), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
