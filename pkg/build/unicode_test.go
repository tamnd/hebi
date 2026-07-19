package build

import (
	"testing"
)

// TestUnicodeFuncs checks the unicode package lowering against go run. A rune
// classifies against the pinned category tables the shim embeds, so the result
// holds byte for byte against Go across the ASCII, Latin-1, and wider ranges,
// whatever the host Python's unicodedata version is.
func TestUnicodeFuncs(t *testing.T) {
	t.Parallel()
	source := `package main

import (
	"fmt"
	"unicode"
)

func main() {
	rs := []rune{'A', 'z', '5', ' ', '\t', 0x00A0, '_', '.', ',', '!', 0x4e16, 0x03B1, 0x0391, 0xB5, 0x2160, 0x00BD, 0x0660, 0x2028, 0x1F600, 0x7F, 0x9F}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		fmt.Println(r,
			unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsNumber(r),
			unicode.IsUpper(r), unicode.IsLower(r), unicode.IsSpace(r),
			unicode.IsControl(r), unicode.IsPunct(r))
	}
}
`
	assertProgramMatchesGo(t, source)
}
