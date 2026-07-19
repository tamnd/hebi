package build

import (
	"testing"
)

// TestUTF8Funcs checks the unicode/utf8 package lowering against go run. The
// package is pure UTF-8 mechanics, so the counting, validation, length, and
// decoding surface holds byte for byte against Go across valid and malformed
// input.
func TestUTF8Funcs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"count length valid",
			`package main

import (
	"fmt"
	"unicode/utf8"
)

func main() {
	s := "héllo, 世界"
	fmt.Println(utf8.RuneCountInString(s), utf8.RuneCount([]byte(s)))
	fmt.Println(utf8.ValidString(s), utf8.Valid([]byte(s)), utf8.ValidString(string([]byte{0xff, 0xc0})))
	fmt.Println(utf8.RuneLen('a'), utf8.RuneLen('世'), utf8.RuneLen(0x1F600), utf8.RuneLen(0x110000))
	fmt.Println(utf8.ValidRune('a'), utf8.ValidRune(0xD800), utf8.ValidRune(0x10FFFF))
	fmt.Println(utf8.RuneError, utf8.RuneSelf, utf8.MaxRune, utf8.UTFMax)
}
`,
		},
		{
			"decode runes",
			`package main

import (
	"fmt"
	"unicode/utf8"
)

func main() {
	r, size := utf8.DecodeRuneInString("世x")
	fmt.Println(r, size)
	lr, ls := utf8.DecodeLastRuneInString("世x")
	fmt.Println(lr, ls)
	er, es := utf8.DecodeRuneInString("")
	fmt.Println(er, es)
	br, bs := utf8.DecodeRune([]byte("é"))
	fmt.Println(br, bs)
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}
