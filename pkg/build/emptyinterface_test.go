package build

import "testing"

// TestEmptyInterface checks the empty interface round trip against go run. A bare
// nil argument to an interface parameter resolves the nil interface from the
// parameter type, an any conversion boxes a value the identity way, a nil into a
// pointer parameter keeps the pointer sentinel rather than becoming the nil
// interface, and a value flows out through an assertion or a type switch to the
// empty interface, which every non-nil value satisfies and the nil interface does
// not.
func TestEmptyInterface(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"a bare nil argument resolves the nil interface",
			"package main\n\nimport \"fmt\"\n\nfunc want(x interface{}) {\n\tfmt.Println(x == nil)\n}\n\nfunc main() {\n\twant(nil)\n\twant(7)\n}\n",
		},
		{
			"an any conversion boxes a value the identity way",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar i interface{} = any(nil)\n\tfmt.Println(i == nil, interface{}(nil) == nil, interface{}(7) == nil)\n}\n",
		},
		{
			"a bare nil into a pointer parameter keeps the sentinel",
			"package main\n\nimport \"fmt\"\n\nfunc takesPtr(p *int) {\n\tfmt.Println(p == nil)\n}\n\nfunc main() {\n\ttakesPtr(nil)\n}\n",
		},
		{
			"a value flows out through an empty interface assertion",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar i interface{} = 7\n\tv := i.(interface{})\n\tw, ok := i.(interface{})\n\tfmt.Println(v, w, ok)\n}\n",
		},
		{
			"a nil interface fails the comma-ok empty interface assertion",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar z interface{}\n\t_, ok := z.(interface{})\n\tfmt.Println(ok)\n}\n",
		},
		{
			"a type switch to the empty interface separates nil from a value",
			"package main\n\nimport \"fmt\"\n\nfunc kind(x interface{}) string {\n\tswitch x.(type) {\n\tcase interface{}:\n\t\treturn \"value\"\n\tdefault:\n\t\treturn \"nil\"\n\t}\n}\n\nfunc main() {\n\tvar z interface{}\n\tfmt.Println(kind(3), kind(z))\n}\n",
		},
		{
			"a deferred call takes a bare nil argument",
			"package main\n\nimport \"fmt\"\n\nfunc want(x interface{}) {\n\tfmt.Println(x == nil)\n}\n\nfunc main() {\n\tdefer want(nil)\n\tfmt.Println(\"body\")\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestEmptyInterfaceAssertNilCrashes pins that the single-result empty interface
// assertion on the nil interface panics and crashes with Go's status, the same as
// any failed assertion.
func TestEmptyInterfaceAssertNilCrashes(t *testing.T) {
	t.Parallel()
	src := "package main\n\nfunc main() {\n\tvar z interface{}\n\t_ = z.(interface{})\n}\n"
	assertProgramCrashesLikeGo(t, src)
}

// TestEmptyInterfaceEmit pins the shape the empty interface lowers to. A bare nil
// argument to an interface parameter emits None, an any conversion is the identity
// so any(x) lowers to x, and the empty interface assertion tests against object
// with the structural flag, which passes every non-nil value and fails None.
func TestEmptyInterfaceEmit(t *testing.T) {
	t.Parallel()
	src := "package main\n\nimport \"fmt\"\n\nfunc want(x interface{}) {\n\tfmt.Println(x == nil)\n}\n\nfunc main() {\n\twant(nil)\n\tvar i interface{} = 7\n\t_ = i.(interface{})\n\tfmt.Println(interface{}(i) == nil)\n}\n"
	got := emitOf(t, src)
	wants := []string{
		"want(None)\n",
		"_hebirt._type_assert(i, object, True,",
	}
	for _, w := range wants {
		if !bytesContains(got, w) {
			t.Errorf("emitted main.py = \n%s\nwant it to contain\n%s", got, w)
		}
	}
}
