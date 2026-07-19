package build

import (
	"testing"
)

// TestBytesFuncs checks the bytes package lowering against go run. It covers the
// search, comparison, case, and reshaping surface over byte slices, held byte
// for byte against Go's output.
func TestBytesFuncs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"search and compare",
			`package main

import (
	"bytes"
	"fmt"
)

func main() {
	b := []byte("Hello, World")
	fmt.Println(bytes.Contains(b, []byte("World")), bytes.HasPrefix(b, []byte("Hell")), bytes.HasSuffix(b, []byte("rld")))
	fmt.Println(bytes.Index(b, []byte("o")), bytes.LastIndex(b, []byte("o")), bytes.IndexByte(b, 'W'))
	fmt.Println(bytes.Count(b, []byte("l")), bytes.Equal(b, []byte("Hello, World")))
	fmt.Println(bytes.Compare([]byte("a"), []byte("b")), bytes.Compare([]byte("b"), []byte("b")), bytes.Compare([]byte("c"), []byte("b")))
}
`,
		},
		{
			"case and reshape",
			`package main

import (
	"bytes"
	"fmt"
)

func main() {
	fmt.Println(string(bytes.ToUpper([]byte("mixEd"))), string(bytes.ToLower([]byte("mixEd"))))
	fmt.Println(string(bytes.TrimSpace([]byte("  hi  "))))
	fmt.Println(string(bytes.Repeat([]byte("ab"), 3)))
	parts := bytes.Split([]byte("a,b,c"), []byte(","))
	fmt.Println(len(parts), string(parts[0]), string(parts[1]), string(parts[2]))
	fmt.Println(string(bytes.Join(parts, []byte("-"))))
}
`,
		},
		{
			"string and rune conversions",
			`package main

import "fmt"

func main() {
	b := []byte("héllo")
	fmt.Println(len(b), string(b))
	r := []rune("héllo")
	fmt.Println(len(r), string(r))
	fmt.Println(string(rune(65)), string(rune(0x4e16)), string(rune(-1)))
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
