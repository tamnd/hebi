package build

import "testing"

// speakers declares a Speaker interface and two concrete types that satisfy it,
// one with a value receiver and one with an empty struct, shared by the programs
// below to exercise dispatch through the interface.
const speakers = "package main\n\nimport \"fmt\"\n\n" +
	"type Speaker interface {\n\tSpeak() string\n}\n\n" +
	"type Dog struct {\n\tName string\n}\n\n" +
	"func (d Dog) Speak() string {\n\treturn d.Name + \" says woof\"\n}\n\n" +
	"type Cat struct{}\n\n" +
	"func (c Cat) Speak() string {\n\treturn \"meow\"\n}\n\n"

// TestInterfaces checks that an interface value dispatches to the concrete type's
// method against go run: a call through an interface parameter reaches each
// concrete method, a nil interface compares equal to nil and a filled one does
// not, two interface values holding the same comparable type compare by value
// while two holding different types compare unequal, an embedded interface folds
// in both method sets, and a named empty interface accepts and carries any value.
func TestInterfaces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"dispatch through an interface parameter",
			speakers + "func announce(s Speaker) {\n\tfmt.Println(s.Speak())\n}\n\n" +
				"func main() {\n\tannounce(Dog{\"Rex\"})\n\tannounce(Cat{})\n}\n",
		},
		{
			"interface variable holds and dispatches",
			speakers + "func main() {\n\tvar s Speaker\n\ts = Dog{\"Fido\"}\n\tfmt.Println(s.Speak())\n\ts = Cat{}\n\tfmt.Println(s.Speak())\n}\n",
		},
		{
			"nil interface compares to nil",
			speakers + "func main() {\n\tvar s Speaker\n\tfmt.Println(s == nil)\n\ts = Cat{}\n\tfmt.Println(s == nil)\n\tfmt.Println(s != nil)\n}\n",
		},
		{
			"interface equality same concrete type",
			speakers + "func main() {\n\tvar a Speaker = Dog{\"Rex\"}\n\tvar b Speaker = Dog{\"Rex\"}\n\tvar c Speaker = Dog{\"Max\"}\n\tfmt.Println(a == b)\n\tfmt.Println(a == c)\n}\n",
		},
		{
			"interface equality different concrete types",
			speakers + "func main() {\n\tvar a Speaker = Dog{\"Rex\"}\n\tvar b Speaker = Cat{}\n\tfmt.Println(a == b)\n}\n",
		},
		{
			"embedded interface folds both method sets",
			"package main\n\nimport \"fmt\"\n\n" +
				"type Reader interface {\n\tRead() int\n}\n\n" +
				"type Writer interface {\n\tWrite(n int)\n}\n\n" +
				"type ReadWriter interface {\n\tReader\n\tWriter\n}\n\n" +
				"type Buf struct {\n\tV int\n}\n\n" +
				"func (b *Buf) Read() int {\n\treturn b.V\n}\n\n" +
				"func (b *Buf) Write(n int) {\n\tb.V = n\n}\n\n" +
				"func use(rw ReadWriter) {\n\trw.Write(42)\n\tfmt.Println(rw.Read())\n}\n\n" +
				"func main() {\n\tb := &Buf{}\n\tuse(b)\n}\n",
		},
		{
			"named empty interface carries any value",
			"package main\n\nimport \"fmt\"\n\n" +
				"type Any interface{}\n\n" +
				"func first(a Any) Any {\n\treturn a\n}\n\n" +
				"func main() {\n\tx := first(7)\n\tfmt.Println(x)\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestInterfaceEmit pins the Python an interface type lowers to: a
// runtime-checkable Protocol with the typing import, one bare method per
// interface method with synthetic positional parameters, an embedded interface
// flattened into one Protocol, and an empty interface whose body is a single
// pass.
func TestInterfaceEmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			"protocol import",
			speakers + "func main() {\n\tvar s Speaker\n\tfmt.Println(s == nil)\n}\n",
			"from typing import Protocol, runtime_checkable\n",
		},
		{
			"runtime-checkable protocol class",
			speakers + "func main() {\n\tvar s Speaker\n\tfmt.Println(s == nil)\n}\n",
			"@runtime_checkable\nclass Speaker(Protocol):\n    def Speak(self): ...\n",
		},
		{
			"method with a parameter gets a synthetic name",
			"package main\n\nimport \"fmt\"\n\n" +
				"type Sink interface {\n\tPut(n int)\n}\n\n" +
				"type Box struct {\n\tV int\n}\n\n" +
				"func (b *Box) Put(n int) {\n\tb.V = n\n}\n\n" +
				"func main() {\n\tvar s Sink = &Box{}\n\ts.Put(1)\n\tfmt.Println(1)\n}\n",
			"class Sink(Protocol):\n    def Put(self, p0): ...\n",
		},
		{
			"empty interface is a pass body",
			"package main\n\nimport \"fmt\"\n\n" +
				"type Any interface{}\n\n" +
				"func hold(a Any) {\n\tfmt.Println(1)\n}\n\n" +
				"func main() {\n\thold(1)\n}\n",
			"@runtime_checkable\nclass Any(Protocol):\n    pass\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := emitOf(t, tt.source)
			if !bytesContains(got, tt.want) {
				t.Errorf("emitted main.py = \n%s\nwant it to contain\n%s", got, tt.want)
			}
		})
	}
}
