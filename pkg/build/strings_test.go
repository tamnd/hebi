package build

import (
	"testing"
)

// TestStringsFuncs checks the strings package lowering against go run. Each
// program exercises a family of the mapped functions, and the differential
// harness holds the compiled output byte for byte against Go's, so a function
// that splits, trims, or folds even one character differently fails here.
func TestStringsFuncs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"contains prefix suffix and index",
			`package main

import (
	"fmt"
	"strings"
)

func main() {
	s := "hello world"
	fmt.Println(strings.Contains(s, "o w"), strings.Contains(s, "xyz"))
	fmt.Println(strings.HasPrefix(s, "hell"), strings.HasSuffix(s, "rld"))
	fmt.Println(strings.Index(s, "o"), strings.LastIndex(s, "o"))
	fmt.Println(strings.IndexByte(s, 'w'), strings.ContainsRune(s, 'z'))
	fmt.Println(strings.Count(s, "l"), strings.Count("", ""), strings.Count("ab", ""))
}
`,
		},
		{
			"split and fields and join",
			`package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println(strings.Split("a,b,c", ","))
	fmt.Println(strings.Split("abc", ""))
	fmt.Println(strings.SplitN("a,b,c,d", ",", 2))
	fmt.Println(strings.SplitN("a,b,c,d", ",", -1))
	fmt.Println(strings.Fields("  foo   bar baz  "))
	fmt.Println(strings.Join([]string{"x", "y", "z"}, "-"))
}
`,
		},
		{
			"case space and trim",
			`package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println(strings.ToUpper("Go rocks"), strings.ToLower("Go ROCKS"))
	fmt.Printf("[%s]\n", strings.TrimSpace("  \t spaced \n "))
	fmt.Printf("[%s]\n", strings.Trim("xxhixx", "x"))
	fmt.Printf("[%s] [%s]\n", strings.TrimLeft("xxhi", "x"), strings.TrimRight("hixx", "x"))
	fmt.Println(strings.TrimPrefix("foobar", "foo"), strings.TrimSuffix("foobar", "bar"))
	fmt.Println(strings.TrimPrefix("foobar", "zzz"))
}
`,
		},
		{
			"repeat replace and equal fold",
			`package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println(strings.Repeat("ab", 3))
	fmt.Println(strings.Replace("aaaa", "a", "b", 2))
	fmt.Println(strings.ReplaceAll("a.b.c", ".", "/"))
	fmt.Println(strings.EqualFold("Go", "GO"), strings.EqualFold("a", "b"))
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

// TestRuneLiterals checks that a rune literal lowers to its Unicode code point,
// the integer Go treats it as, including arithmetic over runes and a multi-byte
// literal, all held against go run.
func TestRuneLiterals(t *testing.T) {
	t.Parallel()
	const source = `package main

import "fmt"

func main() {
	r := 'A'
	fmt.Println(r, r+1, 'z'-'a')
	fmt.Printf("%c %c %d\n", 'A', 'λ', 'λ')
}
`
	assertProgramMatchesGo(t, source)
}
