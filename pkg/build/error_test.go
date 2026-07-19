package build

import "testing"

// TestErrorValues checks the error value world against go run. A Go error is a
// plain value in the compiled tier, never a raised exception, so a nil error is
// Python None, a returned error travels in a tuple slot, the nil check reads as an
// identity comparison, errors.New builds a string-backed value, a custom error
// type is a class whose Error method renders it, and fmt prints an error through
// that method and a nil error as <nil>.
func TestErrorValues(t *testing.T) {
	t.Parallel()
	const custom = "package main\n\nimport \"fmt\"\n\ntype MyErr struct {\n\tMsg string\n}\n\nfunc (e *MyErr) Error() string {\n\treturn \"boom: \" + e.Msg\n}\n\n"
	const finder = "package main\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n)\n\nfunc find(ok bool) (int, error) {\n\tif !ok {\n\t\treturn 0, errors.New(\"not found\")\n\t}\n\treturn 42, nil\n}\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{
			"errors.New and the nil check",
			finder + "func main() {\n\tv, err := find(false)\n\tif err != nil {\n\t\tfmt.Println(\"err:\", err)\n\t}\n\tfmt.Println(v)\n}\n",
		},
		{
			"a successful call returns a nil error",
			finder + "func main() {\n\tv, err := find(true)\n\tif err == nil {\n\t\tfmt.Println(\"got\", v)\n\t}\n}\n",
		},
		{
			"a nil error prints as <nil>",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar err error\n\tfmt.Println(err)\n}\n",
		},
		{
			"a custom error prints through Error",
			custom + "func main() {\n\tvar e error = &MyErr{Msg: \"kaboom\"}\n\tfmt.Println(e)\n}\n",
		},
		{
			"the Error method is callable directly",
			custom + "func main() {\n\te := &MyErr{Msg: \"here\"}\n\tfmt.Println(e.Error())\n}\n",
		},
		{
			"a returned custom error flows through the nil check",
			custom + "func open(bad bool) error {\n\tif bad {\n\t\treturn &MyErr{Msg: \"denied\"}\n\t}\n\treturn nil\n}\n\nfunc main() {\n\terr := open(true)\n\tif err != nil {\n\t\tfmt.Println(err)\n\t}\n\tok := open(false)\n\tif ok == nil {\n\t\tfmt.Println(\"ok\")\n\t}\n}\n",
		},
		{
			"errors.New held in a local and returned",
			"package main\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n)\n\nfunc check(n int) error {\n\tif n < 0 {\n\t\terr := errors.New(\"negative\")\n\t\treturn err\n\t}\n\treturn nil\n}\n\nfunc main() {\n\tfmt.Println(check(-1))\n\tfmt.Println(check(1))\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestErrorEmit pins the shape of the emitted error surface: a nil error is None
// rather than a sentinel, the nil check spells the identity comparison, and
// errors.New routes through the runtime intrinsic.
func TestErrorEmit(t *testing.T) {
	t.Parallel()
	const src = "package main\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n)\n\nfunc find(ok bool) (int, error) {\n\tif !ok {\n\t\treturn 0, errors.New(\"not found\")\n\t}\n\treturn 42, nil\n}\n\nfunc main() {\n\tv, err := find(false)\n\tif err != nil {\n\t\tfmt.Println(v)\n\t}\n}\n"
	got := emitOf(t, src)
	for _, want := range []string{
		"return (0, _hebirt.errors_new(b\"not found\"))",
		"return (42, None)",
		"if (err is not None):",
	} {
		if !bytesContains(got, want) {
			t.Errorf("emit missing %q\n%s", want, got)
		}
	}
}

// TestErrorWrapValues checks the wrapping surface against go run. fmt.Errorf with
// a %w renders the message the fmt way and records the operand, errors.Unwrap
// walks one level to it and reads nil for a plain error, errors.Is finds a
// sentinel down a chain and through a join and reports a miss, and errors.Join
// combines the non-nil messages by newlines. Sentinels are locals since the
// compiled tier does not yet emit package level vars.
func TestErrorWrapValues(t *testing.T) {
	t.Parallel()
	const head = "package main\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n)\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{
			"a wrapped error prints and unwraps to the wrapped one",
			head + "func main() {\n\tbase := errors.New(\"not found\")\n\twrapped := fmt.Errorf(\"open config: %w\", base)\n\tfmt.Println(wrapped)\n\tif errors.Is(wrapped, base) {\n\t\tfmt.Println(\"is base\")\n\t}\n\tinner := errors.Unwrap(wrapped)\n\tfmt.Println(inner)\n}\n",
		},
		{
			"errors.Is reports a miss and unwrap of a plain error is nil",
			head + "func main() {\n\tbase := errors.New(\"boom\")\n\tother := errors.New(\"other\")\n\twrapped := fmt.Errorf(\"wrap: %w\", base)\n\tif !errors.Is(wrapped, other) {\n\t\tfmt.Println(\"not other\")\n\t}\n\tplain := fmt.Errorf(\"code %d: %s\", 42, \"bad\")\n\tfmt.Println(plain)\n\tfmt.Println(errors.Unwrap(plain))\n}\n",
		},
		{
			"a two level chain walks to the root",
			head + "func main() {\n\ta := errors.New(\"a\")\n\tb := fmt.Errorf(\"b: %w\", a)\n\tc := fmt.Errorf(\"c: %w\", b)\n\tfmt.Println(c)\n\tif errors.Is(c, a) {\n\t\tfmt.Println(\"reaches a\")\n\t}\n}\n",
		},
		{
			"a join combines messages and is finds each branch",
			head + "func main() {\n\ta := errors.New(\"one\")\n\tb := errors.New(\"two\")\n\tj := errors.Join(a, b)\n\tfmt.Println(j)\n\tfmt.Println(errors.Is(j, a), errors.Is(j, b))\n}\n",
		},
		{
			"the quote verb quotes its operand",
			head + "func main() {\n\terr := fmt.Errorf(\"key %q: %w\", \"a\\tb\", errors.New(\"missing\"))\n\tfmt.Println(err)\n}\n",
		},
		{
			"a nil join is nil",
			head + "func main() {\n\tvar a, b error\n\tfmt.Println(errors.Join(a, b))\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestErrorWrapEmit pins the shape of the wrapping surface: fmt.Errorf routes to
// the errorf intrinsic with the format carried as bytes, and the errors functions
// route to their own intrinsics over the plainly lowered operands.
func TestErrorWrapEmit(t *testing.T) {
	t.Parallel()
	const src = "package main\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n)\n\nfunc main() {\n\tbase := errors.New(\"base\")\n\twrapped := fmt.Errorf(\"open %s: %w\", \"cfg\", base)\n\tfmt.Println(errors.Is(wrapped, base))\n\tfmt.Println(errors.Unwrap(wrapped))\n\tfmt.Println(errors.Join(base, wrapped))\n}\n"
	got := emitOf(t, src)
	for _, want := range []string{
		"_hebirt.errorf(b\"open %s: %w\", b\"cfg\", base)",
		"_hebirt.errors_is(wrapped, base)",
		"_hebirt.errors_unwrap(wrapped)",
		"_hebirt.errors_join(base, wrapped)",
	} {
		if !bytesContains(got, want) {
			t.Errorf("emit missing %q\n%s", want, got)
		}
	}
}
