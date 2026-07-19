package build

import "testing"

// TestDefer checks defer against go run: the deferred calls run as the function
// returns, in last-in-first-out order, their arguments captured at the defer site
// rather than at the call, and they run on an early return as well as a fall off
// the end.
func TestDefer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"single deferred call runs at return",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tdefer fmt.Println(\"end\")\n\tfmt.Println(\"body\")\n}\n",
		},
		{
			"deferred calls run last in first out",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tdefer fmt.Println(\"a\")\n\tdefer fmt.Println(\"b\")\n\tdefer fmt.Println(\"c\")\n\tfmt.Println(\"body\")\n}\n",
		},
		{
			"deferred argument is captured at the defer site",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\ti := 1\n\tdefer fmt.Println(i)\n\ti = 99\n\tfmt.Println(i)\n}\n",
		},
		{
			"defer in a loop stacks in reverse",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfor i := 0; i < 3; i++ {\n\t\tdefer fmt.Println(i)\n\t}\n\tfmt.Println(\"done\")\n}\n",
		},
		{
			"defer runs on an early return",
			"package main\n\nimport \"fmt\"\n\nfunc cleanup() {\n\tfmt.Println(\"cleanup\")\n}\n\n" +
				"func work(x int) {\n\tdefer cleanup()\n\tif x > 0 {\n\t\tfmt.Println(\"early\")\n\t\treturn\n\t}\n\tfmt.Println(\"late\")\n}\n\n" +
				"func main() {\n\twork(1)\n\twork(0)\n}\n",
		},
		{
			"deferred user function with an argument",
			"package main\n\nimport \"fmt\"\n\nfunc show(x int) {\n\tfmt.Println(x)\n}\n\n" +
				"func main() {\n\tfor i := 0; i < 3; i++ {\n\t\tdefer show(i * 10)\n\t}\n}\n",
		},
		{
			"deferred function literal reads a live local",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tn := 1\n\tdefer func() {\n\t\tfmt.Println(n)\n\t}()\n\tn = 42\n}\n",
		},
		{
			"deferred function literal takes a snapshotted argument",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tn := 1\n\tdefer func(v int) {\n\t\tfmt.Println(v)\n\t}(n)\n\tn = 42\n}\n",
		},
		{
			"pointer receiver method value sees the final state",
			"package main\n\nimport \"fmt\"\n\ntype Counter struct {\n\tN int\n}\n\n" +
				"func (c *Counter) Show() {\n\tfmt.Println(c.N)\n}\n\n" +
				"func main() {\n\tc := Counter{1}\n\tdefer c.Show()\n\tc.N = 5\n}\n",
		},
		{
			"value receiver method value snapshots the receiver",
			"package main\n\nimport \"fmt\"\n\ntype Counter struct {\n\tN int\n}\n\n" +
				"func (c Counter) Show() {\n\tfmt.Println(c.N)\n}\n\n" +
				"func main() {\n\tc := Counter{1}\n\tdefer c.Show()\n\tc.N = 5\n}\n",
		},
		{
			"defer inside a method runs at the method's return",
			"package main\n\nimport \"fmt\"\n\ntype Box struct {\n\tN int\n}\n\n" +
				"func (b Box) Run() {\n\tdefer fmt.Println(\"done\", b.N)\n\tfmt.Println(\"run\", b.N)\n}\n\n" +
				"func main() {\n\tb := Box{7}\n\tb.Run()\n}\n",
		},
		{
			"defer with a struct value argument copies at the defer site",
			"package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n}\n\n" +
				"func show(p Point) {\n\tfmt.Println(p.X)\n}\n\n" +
				"func main() {\n\tp := Point{1}\n\tdefer show(p)\n\tp.X = 9\n\tfmt.Println(p.X)\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestDeferEmit pins the Python a defer lowers to: the body wraps in a try whose
// finally runs the pushed calls in reverse, and a push records the callable and
// its argument tuple, with a single argument carrying the trailing comma a Python
// one-tuple needs.
func TestDeferEmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			"body wraps in a try and a reversed finally",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tdefer fmt.Println(\"end\")\n\tfmt.Println(\"body\")\n}\n",
			"    finally:\n        for _fn, _args in reversed(_defers):\n            _fn(*_args)\n",
		},
		{
			"a single argument push carries a trailing comma",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tdefer fmt.Println(\"end\")\n}\n",
			"_defers.append((_hebirt.println, (b\"end\",)))\n",
		},
		{
			"a no argument push records an empty tuple",
			"package main\n\nfunc cleanup() {}\n\nfunc main() {\n\tdefer cleanup()\n}\n",
			"_defers.append((cleanup, ()))\n",
		},
		{
			"a pointer receiver method value binds the receiver",
			"package main\n\ntype C struct{ N int }\n\nfunc (c *C) Show() {}\n\nfunc main() {\n\tc := C{1}\n\tdefer c.Show()\n}\n",
			"_defers.append((c.Show, ()))\n",
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
