package build

import "testing"

// embedded shares a value-receiver Get and a pointer-receiver Inc promoted from
// an embedded Counter, plus a Bumper interface the outer Widget satisfies through
// those promoted methods, used by the programs below.
const embedded = "package main\n\nimport \"fmt\"\n\n" +
	"type Counter struct {\n\tN int\n}\n\n" +
	"func (c *Counter) Inc() {\n\tc.N = c.N + 1\n}\n\n" +
	"func (c Counter) Get() int {\n\treturn c.N\n}\n\n" +
	"type Widget struct {\n\tCounter\n\tLabel string\n}\n\n" +
	"type Bumper interface {\n\tInc()\n\tGet() int\n}\n\n"

// TestPromotedMethods checks method dispatch through embedding against go run: a
// promoted value-receiver call and a promoted pointer-receiver call reach the
// embedded method, a pointer receiver mutates in place while a value receiver
// copies so the caller is untouched, an interface value dispatches to a promoted
// method whether the concrete type satisfies it by value or through a pointer,
// promotion threads through two embedding levels, a promoted method carries its
// parameters, and a directly declared method shadows a promoted one of the same
// name.
func TestPromotedMethods(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"promoted pointer method mutates through the outer",
			embedded + "func main() {\n\tw := &Widget{}\n\tw.Inc()\n\tw.Inc()\n\tfmt.Println(w.Get())\n}\n",
		},
		{
			"promoted value method copies so the caller is untouched",
			embedded + "func main() {\n\tw := Widget{Counter{5}, \"y\"}\n\t_ = w.Get()\n\tfmt.Println(w.Counter.N)\n}\n",
		},
		{
			"interface dispatches to a promoted pointer method",
			embedded + "func main() {\n\tvar b Bumper = &Widget{Counter{10}, \"x\"}\n\tb.Inc()\n\tfmt.Println(b.Get())\n}\n",
		},
		{
			"interface dispatches to a promoted value method",
			"package main\n\nimport \"fmt\"\n\n" +
				"type Animal struct {\n\tName string\n}\n\n" +
				"func (a Animal) Speak() string {\n\treturn a.Name + \" makes a sound\"\n}\n\n" +
				"type Dog struct {\n\tAnimal\n\tBreed string\n}\n\n" +
				"type Speaker interface {\n\tSpeak() string\n}\n\n" +
				"func main() {\n\tvar s Speaker = Dog{Animal{\"Rex\"}, \"lab\"}\n\tfmt.Println(s.Speak())\n}\n",
		},
		{
			"promotion threads through two embedding levels",
			"package main\n\nimport \"fmt\"\n\n" +
				"type Base struct {\n\tV int\n}\n\n" +
				"func (b Base) Val() int {\n\treturn b.V\n}\n\n" +
				"type Mid struct {\n\tBase\n}\n\n" +
				"type Top struct {\n\tMid\n}\n\n" +
				"func main() {\n\tt := Top{Mid{Base{7}}}\n\tfmt.Println(t.Val())\n}\n",
		},
		{
			"promoted method carries a parameter",
			"package main\n\nimport \"fmt\"\n\n" +
				"type Base struct {\n\tN int\n}\n\n" +
				"func (b *Base) Add(x int) int {\n\tb.N = b.N + x\n\treturn b.N\n}\n\n" +
				"type User struct {\n\tBase\n\tName string\n}\n\n" +
				"func main() {\n\tu := &User{}\n\tfmt.Println(u.Add(3))\n\tfmt.Println(u.Add(4))\n}\n",
		},
		{
			"a directly declared method shadows a promoted one",
			embedded + "func (w Widget) Get() int {\n\treturn 99\n}\n\n" +
				"func main() {\n\tw := Widget{Counter{5}, \"z\"}\n\tfmt.Println(w.Get())\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestPromotedMethodEmit pins the forwarder a promoted method lowers to: a value
// receiver clones the embedded value before the call so the copy matches a direct
// value-receiver call, a pointer receiver passes the live embedded value, and a
// parameter threads through under a synthetic name.
func TestPromotedMethodEmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			"value receiver forwarder clones the embedded value",
			embedded + "func main() {\n\tw := Widget{}\n\tfmt.Println(w.Get())\n}\n",
			"    def Get(self):\n        return self.Counter.copy().Get()\n",
		},
		{
			"pointer receiver forwarder passes the live value",
			embedded + "func main() {\n\tw := &Widget{}\n\tw.Inc()\n\tfmt.Println(w.Get())\n}\n",
			"    def Inc(self):\n        return self.Counter.Inc()\n",
		},
		{
			"forwarder threads a parameter",
			"package main\n\nimport \"fmt\"\n\n" +
				"type Base struct {\n\tN int\n}\n\n" +
				"func (b *Base) Add(x int) int {\n\tb.N = b.N + x\n\treturn b.N\n}\n\n" +
				"type User struct {\n\tBase\n\tName string\n}\n\n" +
				"func main() {\n\tu := &User{}\n\tfmt.Println(u.Add(3))\n}\n",
			"    def Add(self, p0):\n        return self.Base.Add(p0)\n",
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
