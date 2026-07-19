package build

import "testing"

// TestPointerLocals checks the pointer-to-local surface against go run: a scalar
// local whose address is taken is boxed into a cell, so a write through the
// pointer is seen in the local and a later write to the local is seen through the
// pointer, pointer equality is same-slot, a reseated pointer follows the new
// local, and the nil pointer compares only to nil. Each case matches go run.
func TestPointerLocals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"write through pointer", "x := 3\n\tp := &x\n\t*p = 10\n\tfmt.Println(x)"},
		{"read through pointer", "x := 5\n\tp := &x\n\tfmt.Println(*p)"},
		{"write to local seen through pointer", "x := 1\n\tp := &x\n\tx = 2\n\tfmt.Println(*p)"},
		{"write through pointer seen in local", "x := 1\n\tp := &x\n\t*p = 9\n\tfmt.Println(x)"},
		{"pointer identity", "x := 1\n\ty := 2\n\tp := &x\n\tq := &x\n\tr := &y\n\tfmt.Println(p == q, p == r)"},
		{"reseated pointer", "x := 1\n\ty := 2\n\tp := &x\n\tfmt.Println(*p)\n\tp = &y\n\tfmt.Println(*p)"},
		{"nil comparison", "var p *int\n\tfmt.Println(p == nil)\n\tx := 5\n\tp = &x\n\tfmt.Println(p == nil, *p)"},
		{"nil inequality", "var p *int\n\tx := 7\n\tp = &x\n\tfmt.Println(p != nil)"},
		{"compound assign on boxed local", "x := 5\n\tp := &x\n\tx += 3\n\tfmt.Println(*p, x)"},
		{"increment on boxed local", "x := 5\n\tp := &x\n\tx++\n\tfmt.Println(*p, x)"},
		{"two pointers to distinct locals", "x := 1\n\ty := 1\n\tp := &x\n\tq := &y\n\t*p = 8\n\tfmt.Println(x, y, p == q)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestPointerPrograms checks the whole-program pointer shapes go run and hebi must
// agree on: a pointer passed to a function that writes through it reaches the
// caller's local, a pointer to a parameter boxes the parameter, and a pointer to a
// local returned from a function keeps the local alive so the caller reads and
// writes the same slot.
func TestPointerPrograms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"pointer passed to a function",
			"package main\n\nimport \"fmt\"\n\nfunc inc(p *int) {\n\t*p = *p + 1\n}\n\nfunc main() {\n\tx := 5\n\tinc(&x)\n\tfmt.Println(x)\n}\n",
		},
		{
			"pointer to a parameter",
			"package main\n\nimport \"fmt\"\n\nfunc double(n int) int {\n\tp := &n\n\t*p = *p * 2\n\treturn n\n}\n\nfunc main() {\n\tfmt.Println(double(21))\n}\n",
		},
		{
			"pointer to a local escapes",
			"package main\n\nimport \"fmt\"\n\nfunc makePtr() *int {\n\tx := 42\n\treturn &x\n}\n\nfunc main() {\n\tp := makePtr()\n\tfmt.Println(*p)\n\t*p = 7\n\tfmt.Println(*p)\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestPointerLocalsEmit pins the Python the pointer-to-local surface lowers to: a
// boxed local is a cell, taking its address names the cell, a deref read goes
// through get and a deref write through set, a nil pointer is the sentinel, and a
// boxed parameter is re-homed into a cell at the top of the body.
func TestPointerLocalsEmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			"boxed local",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tx := 3\n\tp := &x\n\t*p = 10\n\tfmt.Println(x)\n}\n",
			"def main():\n    x = _hebirt.Cell(3)\n    p = x\n    p.set(10)\n    _hebirt.println(x.get())\n",
		},
		{
			"nil pointer zero and comparison",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar p *int\n\tfmt.Println(p == nil)\n}\n",
			"def main():\n    p = _hebirt.NIL_PTR\n    _hebirt.println((p == _hebirt.NIL_PTR))\n",
		},
		{
			"boxed parameter",
			"package main\n\nimport \"fmt\"\n\nfunc double(n int) int {\n\tp := &n\n\t*p = *p * 2\n\treturn n\n}\n\nfunc main() {\n\tfmt.Println(double(21))\n}\n",
			"def double(n):\n    n = _hebirt.Cell(n)\n    p = n\n    p.set(_hebirt._i64((p.get() * 2)))\n    return n.get()\n",
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
