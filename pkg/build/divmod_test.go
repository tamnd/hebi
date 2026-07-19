package build

import "testing"

// TestDivMod checks integer and float division and remainder against go run. Go
// integer division truncates toward zero and a Go remainder takes the sign of the
// dividend, so a negative operand diverges from Python's flooring // and %: a signed
// quotient and remainder route through helpers, a signed result renarrows to its
// width so int8 -128 / -1 wraps back to -128 the way Go overflows, an unsigned
// operand uses the bare floor operators, and float division follows IEEE rules,
// yielding signed infinity and NaN on a zero divisor rather than raising.
func TestDivMod(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"signed division and remainder truncate toward zero",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(7/2, -7/2, 7/-2, -7/-2)\n\tfmt.Println(7%3, -7%3, 7%-3, -7%-3)\n}\n",
		},
		{
			"a signed quotient renarrows to its width and overflows like Go",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar a int8 = -128\n\tvar b int8 = -1\n\tfmt.Println(a / b)\n}\n",
		},
		{
			"an unsigned operand uses the bare floor operators",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar u uint8 = 200\n\tfmt.Println(u/7, u%7)\n}\n",
		},
		{
			"float division on a zero divisor yields infinity and NaN",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar z float64 = 0\n\tvar one float64 = 1\n\tfmt.Println(7.0/2.0, one/z, -one/z, z/z)\n}\n",
		},
		{
			"a float32 quotient renarrows to single precision",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar f float32 = 1.0\n\tfmt.Println(f / 3)\n}\n",
		},
		{
			"a mixed int and float type parameter dispatches division at runtime",
			"package main\n\nimport \"fmt\"\n\nfunc Div[T int | float64](a, b T) T {\n\treturn a / b\n}\n\nfunc main() {\n\tfmt.Println(Div(7, 2), Div(7.0, 2.0))\n}\n",
		},
		{
			"an all-integer type parameter truncates and takes the remainder sign",
			"package main\n\nimport \"fmt\"\n\nfunc Div[T int | int64](a, b T) T {\n\treturn a / b\n}\n\nfunc Mod[T int | int64](a, b T) T {\n\treturn a % b\n}\n\nfunc main() {\n\tfmt.Println(Div(-7, 2), Mod(-7, 3))\n}\n",
		},
		{
			"an unsigned type parameter uses the bare floor operators",
			"package main\n\nimport \"fmt\"\n\nfunc Div[T uint | uint8](a, b T) T {\n\treturn a / b\n}\n\nfunc main() {\n\tfmt.Println(Div[uint8](200, 7))\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestDivZeroCrashes checks that an integer divide by zero panics the Go way, with
// the runtime-error banner and exit status 2, rather than surfacing Python's
// ZeroDivisionError, so a recover would see Go's message.
func TestDivZeroCrashes(t *testing.T) {
	t.Parallel()
	assertProgramCrashesLikeGo(t, "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tx := 1\n\ty := 0\n\tfmt.Println(x / y)\n}\n")
}

// TestDivModEmit pins the emitted forms: a signed quotient masks the _idiv helper to
// its width, a signed remainder calls _imod, an unsigned operand keeps the bare //
// and % operators, a float quotient calls _fdiv, and a mixed int-or-float type
// parameter routes division through the runtime _quo dispatcher.
func TestDivModEmit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			"signed quotient masks idiv",
			"package main\n\nfunc div(a, b int32) int32 {\n\treturn a / b\n}\n\nfunc main() {\n\tdiv(6, 2)\n}\n",
			"_hebirt._i32(_hebirt._idiv(a, b))",
		},
		{
			"signed remainder calls imod",
			"package main\n\nfunc mod(a, b int) int {\n\treturn a % b\n}\n\nfunc main() {\n\tmod(6, 2)\n}\n",
			"_hebirt._imod(a, b)",
		},
		{
			"unsigned quotient stays a floor divide",
			"package main\n\nfunc div(a, b uint) uint {\n\treturn a / b\n}\n\nfunc main() {\n\tdiv(6, 2)\n}\n",
			"(a // b)",
		},
		{
			"float quotient calls fdiv",
			"package main\n\nfunc div(a, b float64) float64 {\n\treturn a / b\n}\n\nfunc main() {\n\tdiv(6, 2)\n}\n",
			"_hebirt._fdiv(a, b)",
		},
		{
			"mixed type parameter routes through quo",
			"package main\n\nfunc Div[T int | float64](a, b T) T {\n\treturn a / b\n}\n\nfunc main() {\n\tDiv(6, 2)\n}\n",
			"_hebirt._quo(a, b)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := emitOf(t, c.source)
			if !bytesContains(got, c.want) {
				t.Errorf("emitted main.py = \n%s\nwant it to contain\n%s", got, c.want)
			}
		})
	}
}
