package build

import (
	"testing"
)

// TestNamedTypeMethods checks methods on named non-struct types against go run. A
// type like Celsius float64 or Duration int64 boxes into a Python subclass of its
// base, so a method call dispatches on the value, arithmetic and comparison read
// through the base, and a Stringer or an Error prints through fmt the Go way.
func TestNamedTypeMethods(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"float method and arithmetic",
			`package main

import "fmt"

type Celsius float64

func (c Celsius) Fahrenheit() float64 {
	return float64(c)*9/5 + 32
}

func main() {
	c := Celsius(100)
	fmt.Println(c.Fahrenheit())
	d := c + Celsius(10)
	fmt.Println(d.Fahrenheit())
}
`,
		},
		{
			"int method and stringer",
			`package main

import "fmt"

type Duration int64

func (d Duration) Seconds() int64 {
	return int64(d) / 1000
}

func (d Duration) String() string {
	return fmt.Sprintf("%dms", int64(d))
}

func main() {
	var d Duration = 2500
	fmt.Println(d)
	fmt.Println(d.Seconds())
	e := d + 500
	fmt.Println(e)
	fmt.Println(e.String())
}
`,
		},
		{
			"named string type with method",
			`package main

import (
	"fmt"
	"strings"
)

type Name string

func (n Name) Shout() string {
	return strings.ToUpper(string(n)) + "!"
}

func main() {
	n := Name("hebi")
	fmt.Println(n.Shout())
	fmt.Println(len(n))
}
`,
		},
		{
			"stringer through printf verb",
			`package main

import "fmt"

type Level int

func (l Level) String() string {
	switch l {
	case 0:
		return "low"
	case 1:
		return "high"
	default:
		return "unknown"
	}
}

func main() {
	l := Level(1)
	fmt.Printf("%v %s %d\n", l, l, int(l))
}
`,
		},
		{
			"error method on named type",
			`package main

import "fmt"

type Code int

func (c Code) Error() string {
	return fmt.Sprintf("code %d", int(c))
}

func main() {
	var err error = Code(42)
	fmt.Println(err)
}
`,
		},
		{
			"method value and expression",
			`package main

import "fmt"

type Meters float64

func (m Meters) Double() Meters {
	return m * 2
}

func main() {
	m := Meters(3)
	f := m.Double
	fmt.Println(f().Double())
	fmt.Println(Meters.Double(m))
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
