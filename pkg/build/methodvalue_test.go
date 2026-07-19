package build

import "testing"

// TestMethodValues checks method values against go run: a value receiver is
// snapshotted when the value is taken so a later mutation of the original is not
// seen through the bound method, a pointer receiver keeps the shared instance so a
// call through the bound method mutates the original, and a method value passed to
// a function is called there against its captured receiver.
func TestMethodValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"value receiver method value reads",
			methodPoint + "func main() {\n\tp := Point{3, 4}\n\tf := p.Sum\n\tfmt.Println(f())\n}\n",
		},
		{
			"value receiver method value snapshots the receiver",
			"package main\n\nimport \"fmt\"\n\ntype Counter struct {\n\tN int\n}\n\n" +
				"func (c Counter) Peek() int {\n\treturn c.N\n}\n\n" +
				"func main() {\n\tc := Counter{5}\n\tpeek := c.Peek\n\tc.N = 100\n\tfmt.Println(peek())\n}\n",
		},
		{
			"pointer receiver method value mutates the original",
			methodPoint + "func main() {\n\tp := Point{1, 2}\n\tscale := p.Scale\n\tscale(3)\n\tfmt.Println(p.X, p.Y)\n}\n",
		},
		{
			"method value called several times",
			"package main\n\nimport \"fmt\"\n\ntype Acc struct {\n\tN int\n}\n\n" +
				"func (a *Acc) Add(x int) {\n\ta.N = a.N + x\n}\n\n" +
				"func main() {\n\ta := Acc{0}\n\tadd := a.Add\n\tadd(2)\n\tadd(3)\n\tfmt.Println(a.N)\n}\n",
		},
		{
			"method value passed to a function",
			methodPoint + "func apply(f func() int) int {\n\treturn f()\n}\n\n" +
				"func main() {\n\tp := Point{3, 4}\n\tfmt.Println(apply(p.Sum))\n}\n",
		},
		{
			"method value with parameters",
			"package main\n\nimport \"fmt\"\n\ntype Adder struct {\n\tBase int\n}\n\n" +
				"func (a Adder) Add(x, y int) int {\n\treturn a.Base + x + y\n}\n\n" +
				"func main() {\n\ta := Adder{10}\n\tf := a.Add\n\tfmt.Println(f(3, 4))\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestMethodExpressions checks method expressions against go run: an unbound
// method expression takes the receiver as an explicit first argument, a value
// receiver copies that argument so a mutation inside the method does not touch the
// caller, and a pointer receiver runs against the caller's instance.
func TestMethodExpressions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"value receiver method expression bound then called",
			methodPoint + "func main() {\n\tp := Point{3, 4}\n\tf := Point.Sum\n\tfmt.Println(f(p))\n}\n",
		},
		{
			"value receiver method expression called immediately",
			methodPoint + "func main() {\n\tp := Point{3, 4}\n\tfmt.Println(Point.Sum(p))\n}\n",
		},
		{
			"value receiver method expression copies the argument",
			"package main\n\nimport \"fmt\"\n\ntype Box struct {\n\tN int\n}\n\n" +
				"func (b Box) Bump() int {\n\tb.N = b.N + 1\n\treturn b.N\n}\n\n" +
				"func main() {\n\tb := Box{5}\n\tf := Box.Bump\n\tr := f(b)\n\tfmt.Println(r, b.N)\n}\n",
		},
		{
			"pointer receiver method expression bound then called",
			methodPoint + "func main() {\n\tp := Point{1, 2}\n\tf := (*Point).Scale\n\tf(&p, 3)\n\tfmt.Println(p.X, p.Y)\n}\n",
		},
		{
			"pointer receiver method expression called immediately",
			methodPoint + "func main() {\n\tp := Point{1, 2}\n\t(*Point).Scale(&p, 4)\n\tfmt.Println(p.X, p.Y)\n}\n",
		},
		{
			"method expression with parameters",
			"package main\n\nimport \"fmt\"\n\ntype Adder struct {\n\tBase int\n}\n\n" +
				"func (a Adder) Add(x, y int) int {\n\treturn a.Base + x + y\n}\n\n" +
				"func main() {\n\ta := Adder{10}\n\tfmt.Println(Adder.Add(a, 3, 4))\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestMethodValueEmit pins the Python a method value and a method expression lower
// to: a value receiver method value snapshots with a copy, a pointer receiver
// method value binds directly, a value receiver method expression wraps in a lambda
// that copies the receiver argument, and a pointer receiver method expression is
// the bare class function.
func TestMethodValueEmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			"value receiver method value copies",
			methodPoint + "func main() {\n\tp := Point{1, 2}\n\tf := p.Sum\n\tfmt.Println(f())\n}\n",
			"f = p.copy().Sum\n",
		},
		{
			"pointer receiver method value binds directly",
			methodPoint + "func main() {\n\tp := Point{1, 2}\n\tf := p.Scale\n\tf(2)\n\tfmt.Println(p.X)\n}\n",
			"f = p.Scale\n",
		},
		{
			"value receiver method expression wraps in a lambda",
			methodPoint + "func main() {\n\tp := Point{1, 2}\n\tf := Point.Sum\n\tfmt.Println(f(p))\n}\n",
			"f = lambda _s, *_a: Point.Sum(_s.copy(), *_a)\n",
		},
		{
			"pointer receiver method expression is the class function",
			methodPoint + "func main() {\n\tp := Point{1, 2}\n\tf := (*Point).Scale\n\tf(&p, 2)\n\tfmt.Println(p.X)\n}\n",
			"f = Point.Scale\n",
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
