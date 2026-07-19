package build

import "testing"

// TestTypeSwitch checks the type switch surface against go run. The cases test in
// source order and the first match runs, a concrete case binds the guard variable
// to the asserted type, a multi-type case and the default keep the interface value,
// an interface case matches structurally through the value's methods, the nil case
// matches the nil interface, a struct value case binds an independent copy the case
// body may mutate without touching the interface, a pointer case shares the stored
// pointer, and the guardless form switches without binding.
func TestTypeSwitch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"concrete cases with a default",
			asserts + "func d(x interface{}) {\n\tswitch v := x.(type) {\n\tcase int:\n\t\tfmt.Println(\"int\", v+1)\n\tcase bool:\n\t\tfmt.Println(\"bool\", v)\n\tdefault:\n\t\tfmt.Println(\"other\")\n\t}\n}\n\nfunc main() {\n\td(7)\n\td(true)\n\td(1.5)\n}\n",
		},
		{
			"a multi-type case keeps the interface value",
			asserts + "func d(x interface{}) {\n\tswitch v := x.(type) {\n\tcase int, float64:\n\t\tfmt.Println(\"number\", v)\n\tcase string:\n\t\tfmt.Println(\"string\", v)\n\t}\n}\n\nfunc main() {\n\td(7)\n\td(1.5)\n\td(\"hi\")\n}\n",
		},
		{
			"an interface case matches structurally",
			asserts + "func d(x interface{}) {\n\tswitch v := x.(type) {\n\tcase Speaker:\n\t\tfmt.Println(\"speaker\", v.Speak())\n\tdefault:\n\t\tfmt.Println(\"other\")\n\t}\n}\n\nfunc main() {\n\td(Named{\"Rex\"})\n\td(7)\n}\n",
		},
		{
			"the nil case matches the nil interface",
			asserts + "func d(x interface{}) {\n\tswitch x.(type) {\n\tcase int:\n\t\tfmt.Println(\"int\")\n\tcase nil:\n\t\tfmt.Println(\"nil\")\n\tdefault:\n\t\tfmt.Println(\"other\")\n\t}\n}\n\nfunc main() {\n\tvar z interface{}\n\td(7)\n\td(z)\n\td(\"x\")\n}\n",
		},
		{
			"a struct value case binds an independent copy",
			asserts + "func d(x interface{}) {\n\tswitch v := x.(type) {\n\tcase Counter:\n\t\tv.N = 99\n\t\tfmt.Println(\"copy\", v.N)\n\t}\n}\n\nfunc main() {\n\tc := Counter{5}\n\td(c)\n\tfmt.Println(\"after\", c.N)\n}\n",
		},
		{
			"a pointer case shares the stored pointer",
			asserts + "func d(x interface{}) {\n\tswitch v := x.(type) {\n\tcase *Counter:\n\t\tv.N = 42\n\t\tfmt.Println(\"ptr\", v.N)\n\t}\n}\n\nfunc main() {\n\tp := &Counter{5}\n\td(p)\n\tfmt.Println(\"after\", p.N)\n}\n",
		},
		{
			"the guardless form switches without binding",
			asserts + "func kind(x interface{}) string {\n\tswitch x.(type) {\n\tcase int:\n\t\treturn \"int\"\n\tcase string:\n\t\treturn \"string\"\n\t}\n\treturn \"unknown\"\n}\n\nfunc main() {\n\tfmt.Println(kind(3), kind(\"x\"), kind(1.5))\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestTypeSwitchEmit pins the shape a type switch lowers to. The guard spills to a
// temporary, a concrete case tests _assert_pass against the target class, the nil
// case tests the temporary against None by identity, and the named guard variable
// binds to the temporary in each case.
func TestTypeSwitchEmit(t *testing.T) {
	t.Parallel()
	src := asserts + "func d(x interface{}) {\n\tswitch v := x.(type) {\n\tcase int:\n\t\tfmt.Println(v)\n\tcase nil:\n\t\tfmt.Println(\"nil\")\n\t}\n}\n\nfunc main() {\n\td(7)\n}\n"
	got := emitOf(t, src)
	wants := []string{
		"_typ = x\n",
		"if _hebirt._assert_pass(_typ, int, False):\n",
		"v = _typ\n",
		"elif (_typ is None):\n",
	}
	for _, w := range wants {
		if !bytesContains(got, w) {
			t.Errorf("emitted main.py = \n%s\nwant it to contain\n%s", got, w)
		}
	}
}
