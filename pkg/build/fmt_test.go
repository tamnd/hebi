package build

import (
	"testing"
)

// TestPrintfVerbs checks the fmt.Printf and fmt.Sprintf verb engine against go
// run. Each program exercises a family of verbs, flags, widths, and precisions,
// and the differential harness holds the compiled output byte for byte against
// Go's, so a verb that renders even one character differently fails here.
func TestPrintfVerbs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"integer flags width and precision",
			`package main

import "fmt"

func main() {
	fmt.Printf("[%d] [%5d] [%-5d] [%05d] [%+d] [% d]\n", 42, 42, 42, 42, 42, 42)
	fmt.Printf("[%.3d] [%6.3d] [%d] [%05d]\n", 42, 42, -42, -42)
}
`,
		},
		{
			"integer bases",
			`package main

import "fmt"

func main() {
	fmt.Printf("[%x] [%#x] [%X] [%b] [%o] [%#o]\n", 255, 255, 255, 5, 64, 64)
}
`,
		},
		{
			"strings quoting width and precision",
			`package main

import "fmt"

func main() {
	fmt.Printf("[%s] [%q] [%10s] [%-10s|] [%.3s]\n", "hello", "hi\tthere", "pad", "pad", "hello")
}
`,
		},
		{
			"floats fixed exponent and shortest",
			`package main

import "fmt"

func main() {
	fmt.Printf("[%f] [%.2f] [%8.2f] [%e] [%g] [%.3g] [%08.2f]\n", 3.14159, 3.14159, 3.14159, 1500.0, 0.0001, 3.14159, 3.14)
}
`,
		},
		{
			"bool rune and unicode",
			`package main

import "fmt"

func main() {
	fmt.Printf("[%t] [%c] [%U]\n", true, 65, 0x41)
}
`,
		},
		{
			"star width and precision",
			`package main

import "fmt"

func main() {
	fmt.Printf("[%*d] [%.*f]\n", 6, 7, 2, 3.14159)
}
`,
		},
		{
			"sprintf returns a string",
			`package main

import "fmt"

func main() {
	s := fmt.Sprintf("%d-%s-%v", 9, "z", true)
	fmt.Println(s)
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

// TestPrintfValueVerbs checks the value verbs, %v, %+v, %#v, and %T, over the
// composite kinds. A struct dumps its fields, plus labels each field, sharp
// prints the Go-syntax form with the package-qualified type name, and %T prints
// that name, all held against go run.
func TestPrintfValueVerbs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"struct value verbs",
			`package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	p := Point{1, 2}
	fmt.Printf("%v %+v %#v %T\n", p, p, p, p)
}
`,
		},
		{
			"nested struct value verbs",
			`package main

import "fmt"

type Point struct{ X, Y int }
type Outer struct {
	Name string
	P    Point
}

func main() {
	o := Outer{"hi", Point{3, 4}}
	fmt.Printf("%v\n%+v\n%#v\n", o, o, o)
}
`,
		},
		{
			"scalar and composite v",
			`package main

import "fmt"

func main() {
	fmt.Printf("%v %v %v %v\n", 100, "txt", 2.5, true)
	fmt.Printf("%v %+v\n", []int{1, 2, 3}, map[string]int{"b": 2, "a": 1})
}
`,
		},
		{
			"stringer and error take precedence over the field dump",
			`package main

import "fmt"

type temp struct{ v int }

func (t temp) String() string { return fmt.Sprintf("%dK", t.v) }

type myErr struct{ code int }

func (e myErr) Error() string { return fmt.Sprintf("err %d", e.code) }

func main() {
	fmt.Printf("%v %s\n", temp{300}, temp{300})
	var e error = myErr{7}
	fmt.Printf("%v | %s\n", e, e)
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

// TestFmtPlainPrinters checks fmt.Print, fmt.Sprint, and fmt.Sprintln against go
// run. Print and Sprint put a space between two operands only when neither is a
// string, the one rule that separates them from Println, and Sprintln always
// spaces and ends with a newline.
func TestFmtPlainPrinters(t *testing.T) {
	t.Parallel()
	const source = `package main

import "fmt"

func main() {
	fmt.Print("a", "b", 1, 2, "c\n")
	fmt.Print(1, 2, 3)
	fmt.Println()
	fmt.Print(fmt.Sprint("x", 1, "y", 2))
	fmt.Println()
	fmt.Print(fmt.Sprintln("done", 1, true))
}
`
	assertProgramMatchesGo(t, source)
}
