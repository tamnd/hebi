package build

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tamnd/hebi/pkg/shim"
)

// writeModule drops a single-file Go module into a fresh temp dir and returns
// the path to the source file, so a test can load it through the real frontend.
func writeModule(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/prog\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "prog.go")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// assertMatchesGo compiles a main body both ways, once with go run as the
// oracle and once through hebi, and requires the stdout and exit status to
// agree. It is the differential check without a hand-written expected value,
// which is what float formatting needs since the exact digits are Go's to
// decide.
func assertMatchesGo(t *testing.T, body string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	src := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\t" + body + "\n}\n"
	file := writeModule(t, src)

	goCmd := exec.CommandContext(t.Context(), "go", "run", file)
	goOut, err := goCmd.Output()
	if err != nil {
		t.Fatalf("go run: %v", err)
	}

	var out, errb bytes.Buffer
	code, err := Run(context.Background(), file, &out, &errb)
	if err != nil {
		t.Fatalf("Run: %v (stderr: %s)", err, errb.String())
	}
	if code != 0 {
		t.Fatalf("hebi exit = %d (stderr: %s)", code, errb.String())
	}
	if got, want := out.String(), string(goOut); got != want {
		t.Errorf("hebi output = %q, go run output = %q", got, want)
	}
}

// emitOf builds a single-file program through the real frontend and lowering
// and returns the emitted main.py, so a golden test can pin the exact Python a
// construct lowers to without going near the CLI.
func emitOf(t *testing.T, source string) string {
	t.Helper()
	src := writeModule(t, source)
	out := t.TempDir()
	res, err := Build(src, out)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(res.Dir, res.Module))
	if err != nil {
		t.Fatalf("read main.py: %v", err)
	}
	return string(data)
}

// TestFloats checks float64 and float32 lowering against go run: literals,
// native float64 arithmetic, single-precision float32 narrowing, the number
// conversions, and the formatting reconciliation where fmt's shortest float
// differs from Python's str.
func TestFloats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"float64 literal", "fmt.Println(3.14)"},
		{"integer valued float", "fmt.Println(3.0)"},
		{"exponent notation", "fmt.Println(1000000.0)"},
		{"small exponent", "fmt.Println(1e-5)"},
		{"negative float", "fmt.Println(-2.5)"},
		{"float64 arithmetic", "var a float64 = 0.1\n\tvar b float64 = 0.2\n\tfmt.Println(a + b)"},
		{"float64 zero value", "var f float64\n\tfmt.Println(f)"},
		{"mixed args", "fmt.Println(1.5, 2.5, 3)"},
		{"float32 literal", "var a float32 = 0.1\n\tfmt.Println(a)"},
		{"float32 narrows", "var a float32 = 0.1\n\tvar b float32 = 0.2\n\tfmt.Println(a + b)"},
		{"float32 many digits", "var a float32 = 0.123456789\n\tfmt.Println(a)"},
		{"float32 from float64", "var d float64 = 0.1\n\tfmt.Println(float32(d))"},
		{"int from float truncates", "var f float64 = 3.9\n\tfmt.Println(int(f))"},
		{"int from negative float", "var f float64 = -3.9\n\tfmt.Println(int(f))"},
		{"float from int", "var n int = 7\n\tfmt.Println(float64(n))"},
		{"float to sized int masks", "var f float64 = 300.0\n\tfmt.Println(uint8(f))"},
		{"float32 from int", "var n int = 5\n\tfmt.Println(float32(n))"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestStrings checks the string surface against go run: a string is bytes, so a
// literal prints its text, an index reads a byte, len is the byte count,
// concatenation and comparison work byte-wise, and a range over a string steps
// rune by rune yielding the byte index and rune, including a multibyte string, a
// string bound to a variable, a range over an expression, an invalid UTF-8
// sequence that decodes to the replacement rune, and a nested range whose
// internal names must not collide.
func TestStrings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"literal prints text", `fmt.Println("héllo")`},
		{"length is byte count", "s := \"héllo\"\n\tfmt.Println(len(s))"},
		{"index reads a byte", "s := \"héllo\"\n\tfmt.Println(s[1])"},
		{"byte in arithmetic masks", "s := \"AB\"\n\tfmt.Println(s[0] + 1)"},
		{"concatenation", "a := \"foo\"\n\tb := \"bar\"\n\tfmt.Println(a + b)"},
		{"comparison", `fmt.Println("abc" < "abd", "abc" == "abc")`},
		{"range index and rune", "for i, r := range \"héllo\" {\n\t\tfmt.Println(i, r)\n\t}"},
		{"range index only", "for i := range \"héllo\" {\n\t\tfmt.Println(i)\n\t}"},
		{"range rune only", "for _, r := range \"héllo\" {\n\t\tfmt.Println(r)\n\t}"},
		{"range with no vars", "for range \"héllo\" {\n\t\tfmt.Println(1)\n\t}"},
		{"range over variable", "s := \"héllo\"\n\tfor _, r := range s {\n\t\tfmt.Println(r)\n\t}"},
		{"range over expression", "for _, r := range \"foo\" + \"bar\" {\n\t\tfmt.Println(r)\n\t}"},
		{"range invalid utf8", "s := \"\\xff\\xffhi\"\n\tfor _, r := range s {\n\t\tfmt.Println(r)\n\t}"},
		{"nested range", "for _, a := range \"ab\" {\n\t\tfor _, b := range \"cd\" {\n\t\t\tfmt.Println(a, b)\n\t\t}\n\t}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestBooleans checks the boolean surface against go run: not, and, or, the
// comparisons, a bool bound to a variable and used as a condition, the negation
// of a comparison, and a short-circuit chain. Because Go has no truthiness the
// operands are always proven bools, so Python's and and or return a bool too,
// and every printed result is Go's true or false.
func TestBooleans(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"literals", "fmt.Println(true, false)"},
		{"not", "fmt.Println(!true, !false)"},
		{"and or", "fmt.Println(true && false, true || false)"},
		{"not of comparison", "x := 1\n\ty := 2\n\tfmt.Println(!(x < y))"},
		{"bool variable as condition", "b := 3 > 2\n\tif b {\n\t\tfmt.Println(\"yes\")\n\t}"},
		{"stored logical result", "a := true\n\tc := 1 < 2\n\td := a && c\n\tfmt.Println(d)"},
		{"chain", "x := 5\n\tfmt.Println(x > 0 && x < 10 || x == 100)"},
		{"double negation", "ok := false\n\tfmt.Println(!!ok)"},
		{"bool zero value", "var b bool\n\tfmt.Println(b)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestSwitch checks the switch lowering against go run: an expression switch
// with a default, a multi-value case, a tagless boolean switch, a default that
// appears in the middle but is still tested last, a single fallthrough and a
// chain of them, a fallthrough into the default, a string switch, a switch with
// no match and no default, and a nested switch whose tag names must not collide.
func TestSwitch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"expression", "x := 2\n\tswitch x {\n\tcase 1:\n\t\tfmt.Println(\"one\")\n\tcase 2:\n\t\tfmt.Println(\"two\")\n\tdefault:\n\t\tfmt.Println(\"other\")\n\t}"},
		{"multi value case", "x := 3\n\tswitch x {\n\tcase 1, 2, 3:\n\t\tfmt.Println(\"small\")\n\tdefault:\n\t\tfmt.Println(\"big\")\n\t}"},
		{"tagless", "n := -5\n\tswitch {\n\tcase n < 0:\n\t\tfmt.Println(\"neg\")\n\tcase n == 0:\n\t\tfmt.Println(\"zero\")\n\tdefault:\n\t\tfmt.Println(\"pos\")\n\t}"},
		{"default in middle", "x := 9\n\tswitch x {\n\tcase 1:\n\t\tfmt.Println(\"one\")\n\tdefault:\n\t\tfmt.Println(\"other\")\n\tcase 2:\n\t\tfmt.Println(\"two\")\n\t}"},
		{"fallthrough", "x := 1\n\tswitch x {\n\tcase 1:\n\t\tfmt.Println(\"a\")\n\t\tfallthrough\n\tcase 2:\n\t\tfmt.Println(\"b\")\n\tdefault:\n\t\tfmt.Println(\"c\")\n\t}"},
		{"fallthrough chain", "x := 1\n\tswitch x {\n\tcase 1:\n\t\tfmt.Println(\"a\")\n\t\tfallthrough\n\tcase 2:\n\t\tfmt.Println(\"b\")\n\t\tfallthrough\n\tcase 3:\n\t\tfmt.Println(\"c\")\n\t}"},
		{"fallthrough into default", "x := 5\n\tswitch x {\n\tcase 5:\n\t\tfmt.Println(\"five\")\n\t\tfallthrough\n\tdefault:\n\t\tfmt.Println(\"def\")\n\t}"},
		{"string switch", "s := \"hi\"\n\tswitch s {\n\tcase \"hi\":\n\t\tfmt.Println(\"greet\")\n\tcase \"bye\":\n\t\tfmt.Println(\"leave\")\n\t}"},
		{"no match no default", "x := 7\n\tswitch x {\n\tcase 1:\n\t\tfmt.Println(\"one\")\n\t}\n\tfmt.Println(\"after\")"},
		{"nested switch", "x := 1\n\ty := 2\n\tswitch x {\n\tcase 1:\n\t\tswitch y {\n\t\tcase 2:\n\t\t\tfmt.Println(\"inner\")\n\t\t}\n\t}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestForLoops checks every for shape against go run: the counted loop that
// becomes range, a non-zero start, a stride, the inclusive bounds, the two
// descending forms, the condition-only and infinite-with-break forms lowered to
// while, a range over an integer with and without a variable and over an
// expression, the fallbacks where the body mutates the counter or the condition
// is compound, and nested counted loops.
func TestForLoops(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"counted", "for i := 0; i < 5; i++ {\n\t\tfmt.Println(i)\n\t}"},
		{"nonzero start", "for i := 2; i < 6; i++ {\n\t\tfmt.Println(i)\n\t}"},
		{"stride", "for i := 0; i < 10; i += 3 {\n\t\tfmt.Println(i)\n\t}"},
		{"inclusive up", "for i := 0; i <= 5; i++ {\n\t\tfmt.Println(i)\n\t}"},
		{"descending", "for i := 5; i > 0; i-- {\n\t\tfmt.Println(i)\n\t}"},
		{"descending inclusive", "for i := 5; i >= 0; i-- {\n\t\tfmt.Println(i)\n\t}"},
		{"descending stride", "for i := 10; i > 0; i -= 3 {\n\t\tfmt.Println(i)\n\t}"},
		{"condition only", "sum := 0\n\tfor sum < 20 {\n\t\tsum += 7\n\t}\n\tfmt.Println(sum)"},
		{"while form post", "sum := 0\n\tfor i := 0; sum < 15; i++ {\n\t\tsum += i\n\t}\n\tfmt.Println(sum)"},
		{"range int", "for i := range 4 {\n\t\tfmt.Println(i)\n\t}"},
		{"range int blank", "count := 0\n\tfor range 3 {\n\t\tcount++\n\t}\n\tfmt.Println(count)"},
		{"range int expr", "n := 5\n\tfor i := range n {\n\t\tfmt.Println(i)\n\t}"},
		{"range int zero", "for i := range 0 {\n\t\tfmt.Println(i)\n\t}\n\tfmt.Println(\"done\")"},
		{"body mutates counter", "for i := 0; i < 10; i++ {\n\t\tfmt.Println(i)\n\t\ti++\n\t}"},
		{"compound condition", "for i := 0; i < 10 && i < 4; i++ {\n\t\tfmt.Println(i)\n\t}"},
		{"nested counted", "for i := 0; i < 3; i++ {\n\t\tfor j := 0; j < 2; j++ {\n\t\t\tfmt.Println(i, j)\n\t\t}\n\t}"},
		{"sized counter wraps", "for i := uint8(253); i < 255; i++ {\n\t\tfmt.Println(i)\n\t}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestCompoundAssign checks the compound assignments and the increment and
// decrement statements against go run, including the width wrap a growing
// compound assignment on a sized integer must reproduce.
func TestCompoundAssign(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"add", "x := 10\n\tx += 5\n\tfmt.Println(x)"},
		{"sub", "x := 10\n\tx -= 4\n\tfmt.Println(x)"},
		{"mul", "x := 6\n\tx *= 7\n\tfmt.Println(x)"},
		{"shl", "x := 1\n\tx <<= 4\n\tfmt.Println(x)"},
		{"shr", "x := 256\n\tx >>= 3\n\tfmt.Println(x)"},
		{"increment", "x := 41\n\tx++\n\tfmt.Println(x)"},
		{"decrement", "x := 5\n\tx--\n\tfmt.Println(x)"},
		{"sized wrap", "x := uint8(250)\n\tx += 10\n\tfmt.Println(x)"},
		{"sized decrement wrap", "x := uint8(0)\n\tx--\n\tfmt.Println(x)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestBreakContinue checks unlabeled break and continue against go run across
// the loop shapes where continue is a bare keyword and the ones where it must
// still run a step first: an infinite loop, a condition loop, a for-in-range, a
// while-form loop with a post, a range over a string whose cursor must advance,
// nested loops where an inner break stays inner, and a redundant trailing break
// inside a switch that leaves the switch and lets the loop go on.
func TestBreakContinue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"break infinite", "i := 0\n\tfor {\n\t\tif i >= 3 {\n\t\t\tbreak\n\t\t}\n\t\tfmt.Println(i)\n\t\ti++\n\t}"},
		{"continue condition", "i := 0\n\tfor i < 6 {\n\t\ti++\n\t\tif i == 3 {\n\t\t\tcontinue\n\t\t}\n\t\tfmt.Println(i)\n\t}"},
		{"break range int", "for i := range 10 {\n\t\tif i == 4 {\n\t\t\tbreak\n\t\t}\n\t\tfmt.Println(i)\n\t}"},
		{"continue range int", "for i := range 5 {\n\t\tif i == 2 {\n\t\t\tcontinue\n\t\t}\n\t\tfmt.Println(i)\n\t}"},
		{"continue while form post", "sum := 0\n\tfor i := 0; sum < 12; i++ {\n\t\tif i == 2 {\n\t\t\tcontinue\n\t\t}\n\t\tsum += i\n\t}\n\tfmt.Println(sum)"},
		{"break while form post", "last := 0\n\tfor i := 0; i < 100; i++ {\n\t\tif i == 5 {\n\t\t\tbreak\n\t\t}\n\t\tlast = i\n\t}\n\tfmt.Println(last)"},
		{"continue range string", "for i, c := range \"abcde\" {\n\t\tif i == 2 {\n\t\t\tcontinue\n\t\t}\n\t\tfmt.Println(i, c)\n\t}"},
		{"break range string", "for i, c := range \"hello\" {\n\t\tif i == 3 {\n\t\t\tbreak\n\t\t}\n\t\tfmt.Println(i, c)\n\t}"},
		{"nested inner break", "for i := range 3 {\n\t\tfor j := range 3 {\n\t\t\tif j == 1 {\n\t\t\t\tbreak\n\t\t\t}\n\t\t\tfmt.Println(i, j)\n\t\t}\n\t}"},
		{"nested inner continue", "for i := range 3 {\n\t\tfor j := range 3 {\n\t\t\tif j == 1 {\n\t\t\t\tcontinue\n\t\t\t}\n\t\t\tfmt.Println(i, j)\n\t\t}\n\t}"},
		{"continue in switch in loop", "for i := range 4 {\n\t\tswitch i {\n\t\tcase 1:\n\t\t\tcontinue\n\t\t}\n\t\tfmt.Println(i)\n\t}"},
		{"trailing break in switch", "for i := range 3 {\n\t\tswitch i {\n\t\tcase 1:\n\t\t\tfmt.Println(\"one\")\n\t\t\tbreak\n\t\tdefault:\n\t\t\tfmt.Println(\"other\")\n\t\t}\n\t}"},
		{"trailing break in switch no loop", "x := 2\n\tswitch x {\n\tcase 2:\n\t\tfmt.Println(\"two\")\n\t\tbreak\n\tdefault:\n\t\tfmt.Println(\"other\")\n\t}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestLabeledBreak checks a break that names an outer loop against go run. The
// flag machinery has to unwind one loop at a time, so the cases cross one level
// and several, break from inside an if, break from inside a switch nested in the
// loops, sit beside trailing code that the break must skip at each level, name
// the immediately enclosing loop where the break is really just plain, and run
// two independent labeled loops so their flags do not collide.
func TestLabeledBreak(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"one level", "Outer:\n\tfor i := range 4 {\n\t\tfor j := range 4 {\n\t\t\tif i+j == 3 {\n\t\t\t\tbreak Outer\n\t\t\t}\n\t\t\tfmt.Println(i, j)\n\t\t}\n\t}"},
		{"two levels", "Outer:\n\tfor i := range 3 {\n\t\tfor j := range 3 {\n\t\t\tfor k := range 3 {\n\t\t\t\tif i+j+k == 2 {\n\t\t\t\t\tbreak Outer\n\t\t\t\t}\n\t\t\t\tfmt.Println(i, j, k)\n\t\t\t}\n\t\t}\n\t}"},
		{"skips trailing code", "Outer:\n\tfor i := range 3 {\n\t\tfor j := range 3 {\n\t\t\tif i == 1 && j == 1 {\n\t\t\t\tbreak Outer\n\t\t\t}\n\t\t\tfmt.Println(\"inner\", i, j)\n\t\t}\n\t\tfmt.Println(\"tail\", i)\n\t}"},
		{"break from switch", "Outer:\n\tfor i := range 4 {\n\t\tfor j := range 4 {\n\t\t\tswitch {\n\t\t\tcase i*j == 6:\n\t\t\t\tbreak Outer\n\t\t\t}\n\t\t\tfmt.Println(i, j)\n\t\t}\n\t}"},
		{"names inner loop", "for i := range 3 {\n\tInner:\n\t\tfor j := range 3 {\n\t\t\tif j == 1 {\n\t\t\t\tbreak Inner\n\t\t\t}\n\t\t\tfmt.Println(i, j)\n\t\t}\n\t}"},
		{"while form target", "sum := 0\nOuter:\n\tfor i := 0; i < 10; i++ {\n\t\tfor j := range 10 {\n\t\t\tif i*j > 12 {\n\t\t\t\tbreak Outer\n\t\t\t}\n\t\t\tsum += j\n\t\t}\n\t}\n\tfmt.Println(sum)"},
		{"range string target", "Outer:\n\tfor i, c := range \"abcdef\" {\n\t\tfor j := range 3 {\n\t\t\tif i+j == 4 {\n\t\t\t\tbreak Outer\n\t\t\t}\n\t\t\tfmt.Println(i, c, j)\n\t\t}\n\t}"},
		{"two independent labels", "A:\n\tfor i := range 3 {\n\t\tfor range 3 {\n\t\t\tif i == 1 {\n\t\t\t\tbreak A\n\t\t\t}\n\t\t\tfmt.Println(\"a\", i)\n\t\t}\n\t}\nB:\n\tfor i := range 3 {\n\t\tfor range 3 {\n\t\t\tif i == 2 {\n\t\t\t\tbreak B\n\t\t\t}\n\t\t\tfmt.Println(\"b\", i)\n\t\t}\n\t}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestLabeledBreakEmit pins the readable flag shape a two-level labeled break
// lowers to: the flag is declared before the named loop, set and broken at the
// site, and checked after each nested loop so the break unwinds outward.
func TestLabeledBreakEmit(t *testing.T) {
	t.Parallel()
	body := "Outer:\n\tfor i := range 3 {\n\t\tfor j := range 3 {\n\t\t\tif i == j {\n\t\t\t\tbreak Outer\n\t\t\t}\n\t\t}\n\t}"
	want := `def main():
    _brk_Outer = False
    for i in range(3):
        for j in range(3):
            if (i == j):
                _brk_Outer = True
                break
        if _brk_Outer:
            break


if __name__ == "__main__":
    main()
`
	got := emitOf(t, "package main\n\nfunc main() {\n\t"+body+"\n}\n")
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestLabeledContinue checks a continue that names an outer loop against go run.
// The unwinding mirrors labeled break, but the named loop advances instead of
// ending, so the cases cross one level and several, continue an outer while form
// whose post must still run, continue an outer counted loop and an outer range
// over a string, continue from inside an if and from inside a switch nested in the
// loops, name the immediately enclosing loop where the continue is really plain,
// and mix a labeled break and a labeled continue to the same loop so their flags
// must not collide.
func TestLabeledContinue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"one level", "Outer:\n\tfor i := range 3 {\n\t\tfor j := range 3 {\n\t\t\tif j == 1 {\n\t\t\t\tcontinue Outer\n\t\t\t}\n\t\t\tfmt.Println(i, j)\n\t\t}\n\t\tfmt.Println(\"after inner\", i)\n\t}"},
		{"two levels", "Outer:\n\tfor i := range 3 {\n\t\tfor j := range 3 {\n\t\t\tfor k := range 3 {\n\t\t\t\tif k == 1 {\n\t\t\t\t\tcontinue Outer\n\t\t\t\t}\n\t\t\t\tfmt.Println(i, j, k)\n\t\t\t}\n\t\t\tfmt.Println(\"mid\", i, j)\n\t\t}\n\t}"},
		{"while form post runs", "sum := 0\nOuter:\n\tfor i := 0; i < 4; i = i + 1 {\n\t\tfor j := range 4 {\n\t\t\tif j == 2 {\n\t\t\t\tcontinue Outer\n\t\t\t}\n\t\t\tsum += i * j\n\t\t}\n\t\tsum += 100\n\t}\n\tfmt.Println(sum)"},
		{"while form step two", "total := 0\nOuter:\n\tfor i := 0; i < 10; i += 2 {\n\t\tfor j := range 3 {\n\t\t\tif i+j > 6 {\n\t\t\t\tcontinue Outer\n\t\t\t}\n\t\t\ttotal += i\n\t\t}\n\t}\n\tfmt.Println(total)"},
		{"range string target", "Outer:\n\tfor i, c := range \"abcd\" {\n\t\tfor j := range 3 {\n\t\t\tif j == 1 {\n\t\t\t\tcontinue Outer\n\t\t\t}\n\t\t\tfmt.Println(i, c, j)\n\t\t}\n\t\tfmt.Println(\"tail\", i)\n\t}"},
		{"continue from switch", "Outer:\n\tfor i := range 4 {\n\t\tfor j := range 4 {\n\t\t\tswitch {\n\t\t\tcase j == 2:\n\t\t\t\tcontinue Outer\n\t\t\t}\n\t\t\tfmt.Println(i, j)\n\t\t}\n\t\tfmt.Println(\"row\", i)\n\t}"},
		{"names inner loop", "for i := range 3 {\n\tInner:\n\t\tfor j := range 3 {\n\t\t\tif j == 1 {\n\t\t\t\tcontinue Inner\n\t\t\t}\n\t\t\tfmt.Println(i, j)\n\t\t}\n\t}"},
		{"break and continue same loop", "Outer:\n\tfor i := range 4 {\n\t\tfor j := range 4 {\n\t\t\tif j == 2 {\n\t\t\t\tcontinue Outer\n\t\t\t}\n\t\t\tif i == 3 {\n\t\t\t\tbreak Outer\n\t\t\t}\n\t\t\tfmt.Println(i, j)\n\t\t}\n\t\tfmt.Println(\"done row\", i)\n\t}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestLabeledContinueEmit pins the readable flag shape a two-level labeled
// continue lowers to. The named loop is written with a plain-assignment post so it
// stays a while form rather than a counted range, which is where the step
// threading shows: the flag is declared before the loop, set and broken at the
// site, and after the inner loop it is cleared, the loop's post is run, and the
// loop continues, so the named loop advances while the inner one is abandoned.
func TestLabeledContinueEmit(t *testing.T) {
	t.Parallel()
	body := "Outer:\n\tfor i := 0; i < 3; i = i + 1 {\n\t\tfor j := range 3 {\n\t\t\tif j == 1 {\n\t\t\t\tcontinue Outer\n\t\t\t}\n\t\t}\n\t}"
	want := `import _hebirt


def main():
    i = 0
    _cnt_Outer = False
    while (i < 3):
        for j in range(3):
            if (j == 1):
                _cnt_Outer = True
                break
        if _cnt_Outer:
            _cnt_Outer = False
            i = _hebirt._i64((i + 1))
            continue
        i = _hebirt._i64((i + 1))


if __name__ == "__main__":
    main()
`
	got := emitOf(t, "package main\n\nfunc main() {\n\t"+body+"\n}\n")
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBuildWritesFiles(t *testing.T) {
	t.Parallel()
	src := writeModule(t, "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(1 + 2)\n}\n")
	out := filepath.Join(t.TempDir(), "out")
	res, err := Build(src, out)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.Module != "main.py" {
		t.Errorf("module = %q, want main.py", res.Module)
	}
	for _, name := range []string{"main.py", shim.Name + ".py"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("expected %s to be written: %v", name, err)
		}
	}
}

func TestRunMatchesGo(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	src := writeModule(t, "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tx := 1 + 2\n\tif x < 5 {\n\t\tfmt.Println(x)\n\t}\n}\n")
	var out, errb bytes.Buffer
	code, err := Run(context.Background(), src, &out, &errb)
	if err != nil {
		t.Fatalf("Run: %v (stderr: %s)", err, errb.String())
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr: %s)", code, errb.String())
	}
	if got, want := out.String(), "3\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// TestIntegerWidths checks masking end to end: each program exercises a width,
// an overflow, a conversion, or a negation, and the emitted Python must print
// exactly what go run prints. It is the strongest proof the mask helpers wrap
// two's-complement the Go way.
func TestIntegerWidths(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{"uint8 overflow", "var a uint8 = 200\n\tvar b uint8 = 100\n\tfmt.Println(a + b)", "44\n"},
		{"int8 overflow", "var x int8 = 100\n\tfmt.Println(x * 2)", "-56\n"},
		{"uint32 wrap", "var u uint32 = 0xFFFFFFFF\n\tfmt.Println(u + 1)", "0\n"},
		{"conversion truncates", "var big uint32 = 0x1234\n\tfmt.Println(uint8(big))", "52\n"},
		{"signed to unsigned", "var n int8 = -1\n\tfmt.Println(uint8(n))", "255\n"},
		{"unsigned to signed", "var n uint8 = 255\n\tfmt.Println(int8(n))", "-1\n"},
		{"widen sign extends", "var n int8 = -1\n\tfmt.Println(int32(n))", "-1\n"},
		{"negation wraps", "var x int8 = -128\n\tfmt.Println(-x)", "-128\n"},
		{"int64 arithmetic", "var a int = 1000000\n\tfmt.Println(a * a)", "1000000000000\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\t" + tt.body + "\n}\n"
			var out, errb bytes.Buffer
			code, err := Run(context.Background(), writeModule(t, src), &out, &errb)
			if err != nil {
				t.Fatalf("Run: %v (stderr: %s)", err, errb.String())
			}
			if code != 0 {
				t.Fatalf("exit code = %d (stderr: %s)", code, errb.String())
			}
			if got := out.String(); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestShifts checks shift semantics end to end against go run. It covers a
// masked left shift, oversized shift counts on both directions, arithmetic and
// logical right shifts, and two untyped-constant cases: a shift computed at
// full precision and an expression whose Go grouping differs from Python's,
// which the paren-everything emitter must preserve.
func TestShifts(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{"uint32 left shift masks", "var x uint32 = 0x80000000\n\tfmt.Println(x << 1)", "0\n"},
		{"uint8 left shift oversized", "var x uint8 = 1\n\tfmt.Println(x << 9)", "0\n"},
		{"signed left shift", "var x int8 = -1\n\tfmt.Println(x << 2)", "-4\n"},
		{"signed right shift arithmetic", "var x int8 = -8\n\tfmt.Println(x >> 1)", "-4\n"},
		{"signed right shift oversized", "var x int8 = -1\n\tfmt.Println(x >> 100)", "-1\n"},
		{"unsigned right shift logical", "var x uint8 = 200\n\tfmt.Println(x >> 1)", "100\n"},
		{"unsigned right shift oversized", "var x uint32 = 0xFFFFFFFF\n\tfmt.Println(x >> 40)", "0\n"},
		{"untyped constant full precision", "fmt.Println(1 << 40)", "1099511627776\n"},
		{"precedence preserved", "fmt.Println(1 << 2 * 3)", "12\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\t" + tt.body + "\n}\n"
			var out, errb bytes.Buffer
			code, err := Run(context.Background(), writeModule(t, src), &out, &errb)
			if err != nil {
				t.Fatalf("Run: %v (stderr: %s)", err, errb.String())
			}
			if code != 0 {
				t.Fatalf("exit code = %d (stderr: %s)", code, errb.String())
			}
			if got := out.String(); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildRejectsUnsupported(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{"method", "package main\n\ntype T struct{}\n\nfunc (T) m() {}\n\nfunc main() {}\n"},
		{"parameter", "package main\n\nfunc f(x int) {}\n\nfunc main() {}\n"},
		{"result", "package main\n\nfunc f() int { return 0 }\n\nfunc main() {}\n"},
		{"division compound assign", "package main\n\nfunc main() {\n\tx := 8\n\tx /= 2\n\t_ = x\n}\n"},
		{"labeled switch", "package main\n\nfunc main() {\n\tx := 1\nSw:\n\tswitch x {\n\tcase 1:\n\t\tbreak Sw\n\t}\n}\n"},
		{"goto", "package main\n\nfunc main() {\n\ti := 0\nLoop:\n\tif i < 3 {\n\t\ti++\n\t\tgoto Loop\n\t}\n}\n"},
		{"non-tail break in switch", "package main\n\nfunc main() {\n\tx := 1\n\tswitch x {\n\tcase 1:\n\t\tbreak\n\t\tprintln(\"after\")\n\t}\n}\n"},
		{"statement label", "package main\n\nfunc main() {\nDone:\n\tprintln(\"hi\")\n\t_ = 0\n\tgoto Done\n}\n"},
		{"unsupported call", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Exit(0)\n}\n"},
		{"embedded field", "package main\n\ntype Inner struct{ N int }\n\ntype Outer struct {\n\tInner\n\tM int\n}\n\nfunc main() {\n\tvar o Outer\n\t_ = o\n}\n"},
		{"pointer field", "package main\n\ntype Node struct {\n\tV int\n\tNext *Node\n}\n\nfunc main() {\n\tvar n Node\n\t_ = n\n}\n"},
		{"slice field", "package main\n\ntype Bag struct{ Items []int }\n\nfunc main() {\n\tvar b Bag\n\t_ = b\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := writeModule(t, tt.source)
			out := filepath.Join(t.TempDir(), "out")
			if _, err := Build(src, out); err == nil {
				t.Fatal("Build accepted unsupported source")
			}
		})
	}
}

// assertProgramMatchesGo runs a whole Go program through go run as the oracle
// and through hebi, and requires stdout and exit status to agree. Struct tests
// need this rather than assertMatchesGo because a struct type is declared at
// package level, outside the main body assertMatchesGo wraps.
func assertProgramMatchesGo(t *testing.T, source string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	file := writeModule(t, source)

	goCmd := exec.CommandContext(t.Context(), "go", "run", file)
	goOut, err := goCmd.Output()
	if err != nil {
		t.Fatalf("go run: %v", err)
	}

	var out, errb bytes.Buffer
	code, err := Run(context.Background(), file, &out, &errb)
	if err != nil {
		t.Fatalf("Run: %v (stderr: %s)", err, errb.String())
	}
	if code != 0 {
		t.Fatalf("hebi exit = %d (stderr: %s)", code, errb.String())
	}
	if got, want := out.String(), string(goOut); got != want {
		t.Errorf("hebi output = %q, go run output = %q", got, want)
	}
}

// TestStructs checks the struct surface against go run: a struct is a class,
// construction is a constructor call, field access reads an attribute, and value
// copy on assignment makes an independent instance so a later field write to one
// does not touch the other. It covers the positional and keyed literals, the
// omitted-field zero default, the zero-value var declaration, field assignment,
// the copy on plain assignment and on var-with-value, a nested value-struct field
// that copies deeply, and the field read that yields a value and so copies.
func TestStructs(t *testing.T) {
	t.Parallel()
	const point = "package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n\tY int\n}\n\n"
	const mixed = "package main\n\nimport \"fmt\"\n\ntype Inner struct {\n\tN int\n}\n\ntype Outer struct {\n\tV Inner\n\tK int\n}\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{"positional literal and field read", point + "func main() {\n\tp := Point{1, 2}\n\tfmt.Println(p.X, p.Y)\n}\n"},
		{"keyed literal", point + "func main() {\n\tp := Point{X: 3, Y: 4}\n\tfmt.Println(p.X, p.Y)\n}\n"},
		{"keyed literal omits a field", point + "func main() {\n\tp := Point{X: 7}\n\tfmt.Println(p.X, p.Y)\n}\n"},
		{"zero value var", point + "func main() {\n\tvar p Point\n\tfmt.Println(p.X, p.Y)\n}\n"},
		{"field assignment", point + "func main() {\n\tvar p Point\n\tp.X = 5\n\tp.Y = 6\n\tfmt.Println(p.X, p.Y)\n}\n"},
		{"copy on assignment is independent", point + "func main() {\n\ta := Point{1, 2}\n\tb := a\n\tb.X = 99\n\tfmt.Println(a.X, b.X)\n}\n"},
		{"copy on var with value", point + "func main() {\n\ta := Point{1, 2}\n\tvar b Point = a\n\tb.Y = 42\n\tfmt.Println(a.Y, b.Y)\n}\n"},
		{"plain assignment copies", point + "func main() {\n\ta := Point{1, 2}\n\tvar b Point\n\tb = a\n\tb.X = 8\n\tfmt.Println(a.X, b.X)\n}\n"},
		{"nested value struct copies deeply", mixed + "func main() {\n\ta := Outer{V: Inner{N: 1}, K: 2}\n\tb := a\n\tb.V.N = 9\n\tfmt.Println(a.V.N, b.V.N)\n}\n"},
		{"field read of value struct copies", mixed + "func main() {\n\to := Outer{V: Inner{N: 3}, K: 4}\n\tinner := o.V\n\tinner.N = 5\n\tfmt.Println(o.V.N, inner.N)\n}\n"},
		{"literal element copies", mixed + "func main() {\n\ti := Inner{N: 1}\n\to := Outer{V: i, K: 0}\n\ti.N = 8\n\tfmt.Println(o.V.N, i.N)\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestStructsEmit pins the exact Python a struct lowers to: a class with a
// __slots__ tuple, a zero-value constructor, and a copy method, plus the
// constructor call, the copy at the value assignment, and the field reads in
// main.
func TestStructsEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n\tY int\n}\n\nfunc main() {\n\ta := Point{1, 2}\n\tb := a\n\tb.X = 99\n\tfmt.Println(a.X, b.X)\n}\n"
	want := "import " + shim.Name + `


class Point:
    __slots__ = ("X", "Y")

    def __init__(self, X=0, Y=0):
        self.X = X
        self.Y = Y

    def copy(self):
        return Point(self.X, self.Y)


def main():
    a = Point(1, 2)
    b = a.copy()
    b.X = 99
    ` + shim.Name + `.println(a.X, b.X)


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestStructValueFieldEmit pins the constructor's None-sentinel default and the
// deep copy for a value-struct field, the shape that keeps a keyed literal from
// aliasing a shared zero instance and a copy from aliasing the nested value.
func TestStructValueFieldEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\ntype Inner struct {\n\tN int\n}\n\ntype Outer struct {\n\tV Inner\n\tK int\n}\n\nfunc main() {\n\tvar o Outer\n\t_ = o\n}\n"
	want := `class Inner:
    __slots__ = ("N",)

    def __init__(self, N=0):
        self.N = N

    def copy(self):
        return Inner(self.N)


class Outer:
    __slots__ = ("V", "K")

    def __init__(self, V=None, K=0):
        self.V = V if V is not None else Inner()
        self.K = K

    def copy(self):
        return Outer(self.V.copy(), self.K)


def main():
    o = Outer()
    _ = o


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}
