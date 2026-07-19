package build

import "testing"

// point is a struct with a value-receiver method that reads and a
// pointer-receiver method that mutates, the two receiver kinds a method-set slice
// must get right, shared by the method programs below.
const methodPoint = "package main\n\nimport \"fmt\"\n\n" +
	"type Point struct {\n\tX int\n\tY int\n}\n\n" +
	"func (p Point) Sum() int {\n\treturn p.X + p.Y\n}\n\n" +
	"func (p *Point) Scale(f int) {\n\tp.X = p.X * f\n\tp.Y = p.Y * f\n}\n\n"

// TestMethods checks method calls against go run: a value receiver operates on a
// copy so a mutation inside it does not touch the caller, a pointer receiver
// mutates the caller's value in place, a value method call auto-copies, a pointer
// method on an addressable value auto-takes the address, and a method on a pointer
// value with a value receiver copies the pointed-to struct.
func TestMethods(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"value receiver reads",
			methodPoint + "func main() {\n\tp := Point{3, 4}\n\tfmt.Println(p.Sum())\n}\n",
		},
		{
			"pointer receiver mutates in place",
			methodPoint + "func main() {\n\tp := Point{3, 4}\n\tp.Scale(2)\n\tfmt.Println(p.X, p.Y)\n}\n",
		},
		{
			"value receiver does not mutate caller",
			"package main\n\nimport \"fmt\"\n\ntype Box struct {\n\tN int\n}\n\n" +
				"func (b Box) Bump() {\n\tb.N = b.N + 1\n}\n\n" +
				"func main() {\n\tb := Box{5}\n\tb.Bump()\n\tfmt.Println(b.N)\n}\n",
		},
		{
			"pointer method through a pointer variable",
			methodPoint + "func main() {\n\tp := Point{1, 2}\n\tq := &p\n\tq.Scale(3)\n\tfmt.Println(p.X, p.Y)\n}\n",
		},
		{
			"value method through a pointer variable copies",
			"package main\n\nimport \"fmt\"\n\ntype Box struct {\n\tN int\n}\n\n" +
				"func (b Box) Bump() {\n\tb.N = b.N + 1\n}\n\n" +
				"func main() {\n\tb := Box{5}\n\tp := &b\n\tp.Bump()\n\tfmt.Println(b.N)\n}\n",
		},
		{
			"method with parameters",
			"package main\n\nimport \"fmt\"\n\ntype Adder struct {\n\tBase int\n}\n\n" +
				"func (a Adder) Add(x, y int) int {\n\treturn a.Base + x + y\n}\n\n" +
				"func main() {\n\ta := Adder{10}\n\tfmt.Println(a.Add(3, 4))\n}\n",
		},
		{
			"method returns a struct value",
			methodPoint + "func (p Point) Doubled() Point {\n\treturn Point{p.X * 2, p.Y * 2}\n}\n\n" +
				"func main() {\n\tp := Point{3, 4}\n\tq := p.Doubled()\n\tfmt.Println(q.Sum())\n}\n",
		},
		{
			"method calls another method on the receiver",
			methodPoint + "func (p *Point) Grow() {\n\tp.Scale(2)\n\tp.Scale(2)\n}\n\n" +
				"func main() {\n\tp := Point{1, 1}\n\tp.Grow()\n\tfmt.Println(p.X, p.Y)\n}\n",
		},
		{
			"pointer method sees value method mutation isolated",
			"package main\n\nimport \"fmt\"\n\ntype Counter struct {\n\tN int\n}\n\n" +
				"func (c Counter) Peek() int {\n\treturn c.N\n}\n\n" +
				"func (c *Counter) Inc() {\n\tc.N = c.N + 1\n}\n\n" +
				"func main() {\n\tc := Counter{0}\n\tc.Inc()\n\tc.Inc()\n\tfmt.Println(c.Peek())\n}\n",
		},
		{
			"method on a struct returned by value is a fresh copy",
			methodPoint + "func makePoint() Point {\n\treturn Point{2, 3}\n}\n\n" +
				"func main() {\n\tfmt.Println(makePoint().Sum())\n}\n",
		},
		{
			"pointer method on an addressable field",
			"package main\n\nimport \"fmt\"\n\ntype Cell struct {\n\tN int\n}\n\n" +
				"func (c *Cell) Set(v int) {\n\tc.N = v\n}\n\n" +
				"type Holder struct {\n\tC Cell\n}\n\n" +
				"func main() {\n\th := Holder{}\n\th.C.Set(9)\n\tfmt.Println(h.C.N)\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestMethodEmit pins the Python a method surface lowers to: the receiver becomes
// self and drops from the signature, a value-receiver call copies the receiver at
// the call, and a pointer-receiver call passes the instance directly.
func TestMethodEmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			"value receiver becomes self",
			methodPoint + "func main() {\n\tp := Point{1, 2}\n\tfmt.Println(p.Sum())\n}\n",
			"    def Sum(self):\n        return _hebirt._i64((self.X + self.Y))\n",
		},
		{
			"pointer receiver becomes self",
			methodPoint + "func main() {\n\tp := Point{1, 2}\n\tp.Scale(2)\n\tfmt.Println(p.X)\n}\n",
			"    def Scale(self, f):\n        self.X = _hebirt._i64((self.X * f))\n        self.Y = _hebirt._i64((self.Y * f))\n",
		},
		{
			"value method call copies the receiver",
			methodPoint + "func main() {\n\tp := Point{3, 4}\n\tfmt.Println(p.Sum())\n}\n",
			"_hebirt.println(p.copy().Sum())\n",
		},
		{
			"pointer method call passes the instance",
			methodPoint + "func main() {\n\tp := Point{3, 4}\n\tp.Scale(2)\n\tfmt.Println(p.X)\n}\n",
			"    p.Scale(2)\n",
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
