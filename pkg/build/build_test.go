package build

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tamnd/hebi/pkg/shim"
)

// goRunTokens bounds how many go run oracles compile at once across the parallel
// differential tests. go run drives the Go compiler and linker, which are memory
// heavy, so an unbounded t.Parallel fan-out launches one compile per core at
// peak and spikes memory, more so under the race detector. A token per allowed
// compile caps that peak. The ceiling mirrors the conformance harness: a small
// fraction of the cores, never below two.
var goRunTokens = make(chan struct{}, maxGoRun())

func maxGoRun() int {
	n := runtime.GOMAXPROCS(0) / 2
	if n < 2 {
		return 2
	}
	return n
}

// acquireGoRun takes a compile token for the duration of the test, releasing it
// when the test ends, so the oracle stays bounded without threading a release
// call through each harness helper.
func acquireGoRun(t *testing.T) {
	t.Helper()
	goRunTokens <- struct{}{}
	t.Cleanup(func() { <-goRunTokens })
}

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

	acquireGoRun(t)
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
		{"method on a non-struct type", "package main\n\ntype Celsius float64\n\nfunc (c Celsius) Freezing() bool {\n\treturn c <= 0\n}\n\nfunc main() {\n\tvar c Celsius\n\t_ = c.Freezing()\n}\n"},
		{"promoted method call", "package main\n\ntype Base struct{ N int }\n\nfunc (b Base) Get() int {\n\treturn b.N\n}\n\ntype User struct {\n\tBase\n\tName string\n}\n\nfunc main() {\n\tu := User{}\n\t_ = u.Get()\n}\n"},
		{"reassigning an address-taken struct", "package main\n\ntype Point struct{ X int }\n\nfunc main() {\n\tp := Point{1}\n\tq := &p\n\tp = Point{2}\n\t_ = q\n}\n"},
		{"writing through a pointer to a struct", "package main\n\ntype Point struct{ X int }\n\nfunc main() {\n\tp := Point{1}\n\tq := &p\n\t*q = Point{2}\n\t_ = q\n}\n"},
		{"promoted method value", "package main\n\ntype Base struct{ N int }\n\nfunc (b Base) Get() int {\n\treturn b.N\n}\n\ntype User struct {\n\tBase\n\tName string\n}\n\nfunc main() {\n\tu := User{}\n\tf := u.Get\n\t_ = f\n}\n"},
		{"promoted method expression", "package main\n\ntype Base struct{ N int }\n\nfunc (b Base) Get() int {\n\treturn b.N\n}\n\ntype User struct {\n\tBase\n\tName string\n}\n\nfunc main() {\n\tf := User.Get\n\t_ = f\n}\n"},
		{"variadic parameter", "package main\n\nfunc f(xs ...int) {}\n\nfunc main() { f() }\n"},
		{"embedded pointer field", "package main\n\ntype Inner struct{ N int }\n\ntype Outer struct {\n\t*Inner\n\tM int\n}\n\nfunc main() {\n\tvar o Outer\n\t_ = o\n}\n"},
		{"division compound assign", "package main\n\nfunc main() {\n\tx := 8\n\tx /= 2\n\t_ = x\n}\n"},
		{"labeled switch", "package main\n\nfunc main() {\n\tx := 1\nSw:\n\tswitch x {\n\tcase 1:\n\t\tbreak Sw\n\t}\n}\n"},
		{"goto", "package main\n\nfunc main() {\n\ti := 0\nLoop:\n\tif i < 3 {\n\t\ti++\n\t\tgoto Loop\n\t}\n}\n"},
		{"non-tail break in switch", "package main\n\nfunc main() {\n\tx := 1\n\tswitch x {\n\tcase 1:\n\t\tbreak\n\t\tprintln(\"after\")\n\t}\n}\n"},
		{"statement label", "package main\n\nfunc main() {\nDone:\n\tprintln(\"hi\")\n\t_ = 0\n\tgoto Done\n}\n"},
		{"unsupported call", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Exit(0)\n}\n"},
		{"pointer field", "package main\n\ntype Node struct {\n\tV int\n\tNext *Node\n}\n\nfunc main() {\n\tvar n Node\n\t_ = n\n}\n"},
		{"array reslice", "package main\n\nfunc main() {\n\ta := [3]int{1, 2, 3}\n\ts := a[0:2]\n\t_ = s\n}\n"},
		{"map with an array key", "package main\n\nfunc main() {\n\tm := map[[2]int]int{}\n\t_ = m\n}\n"},
		{"append spread", "package main\n\nfunc main() {\n\ta := []int{1, 2}\n\tb := []int{3, 4}\n\ta = append(a, b...)\n\t_ = a\n}\n"},
		{"closure writes package level var", "package main\n\nvar count int\n\nfunc main() {\n\tf := func() { count = 5 }\n\tf()\n}\n"},
		{"address of a loop variable", "package main\n\nfunc main() {\n\tfor i := 0; i < 3; i++ {\n\t\tp := &i\n\t\t_ = p\n\t}\n}\n"},
		{"address of a slice literal", "package main\n\nfunc main() {\n\tp := &[]int{1, 2}\n\t_ = p\n}\n"},
		{"address of a local inside a closure", "package main\n\nfunc main() {\n\tf := func() {\n\t\tx := 1\n\t\tp := &x\n\t\t_ = p\n\t}\n\tf()\n}\n"},
		{"address of a package level variable", "package main\n\nvar g int\n\nfunc main() {\n\tp := &g\n\t_ = p\n}\n"},
		{"boxed local in a parallel assignment", "package main\n\nfunc main() {\n\tx := 1\n\ty := 2\n\tp := &x\n\tx, y = y, x\n\t_ = p\n\t_ = y\n}\n"},
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

	acquireGoRun(t)
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
// __slots__ tuple, a zero-value constructor, a copy method, and the field-wise
// __eq__ and matching __hash__ a comparable struct earns, plus the constructor
// call, the copy at the value assignment, and the field reads in main.
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

    def __eq__(self, other):
        if other.__class__ is not Point:
            return NotImplemented
        return self.X == other.X and self.Y == other.Y

    def __hash__(self):
        return hash((self.X, self.Y))


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

    def __eq__(self, other):
        if other.__class__ is not Inner:
            return NotImplemented
        return self.N == other.N

    def __hash__(self):
        return hash((self.N,))


class Outer:
    __slots__ = ("V", "K")

    def __init__(self, V=None, K=0):
        self.V = V if V is not None else Inner()
        self.K = K

    def copy(self):
        return Outer(self.V.copy(), self.K)

    def __eq__(self, other):
        if other.__class__ is not Outer:
            return NotImplemented
        return self.V == other.V and self.K == other.K

    def __hash__(self):
        return hash((self.V, self.K))


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

// TestStructValueSemantics checks the copy-on-call and copy-on-return sites and
// struct comparison by value against go run. A struct passed by value is an
// independent copy, so a mutation inside the callee does not touch the caller's;
// a struct returned by value is independent of the callee's frame; and == and !=
// compare field by field, recursing into a value-struct field, so two structs
// with equal fields are equal and one differing field makes them unequal.
func TestStructValueSemantics(t *testing.T) {
	t.Parallel()
	const point = "package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n\tY int\n}\n\n"
	const boxed = "package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n\tY int\n}\n\ntype Box struct {\n\tP Point\n}\n\n"
	const mixed = "package main\n\nimport \"fmt\"\n\ntype Inner struct {\n\tN int\n}\n\ntype Outer struct {\n\tV Inner\n\tK int\n}\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{"copy on call is independent", point + "func bump(p Point) {\n\tp.X = 99\n}\n\nfunc main() {\n\ta := Point{1, 2}\n\tbump(a)\n\tfmt.Println(a.X)\n}\n"},
		{"copy on return of a field", boxed + "func get(b Box) Point {\n\treturn b.P\n}\n\nfunc main() {\n\tb := Box{P: Point{1, 2}}\n\tp := get(b)\n\tp.X = 99\n\tfmt.Println(b.P.X, p.X)\n}\n"},
		{"return of a param copies", point + "func idp(p Point) Point {\n\treturn p\n}\n\nfunc main() {\n\ta := Point{1, 2}\n\tc := idp(a)\n\tc.X = 7\n\tfmt.Println(a.X, c.X)\n}\n"},
		{"fresh literal arg not double copied", point + "func first(p Point) int {\n\treturn p.X\n}\n\nfunc main() {\n\tfmt.Println(first(Point{5, 6}))\n}\n"},
		{"scalar param and return", "package main\n\nimport \"fmt\"\n\nfunc add(a, b int) int {\n\treturn a + b\n}\n\nfunc main() {\n\tfmt.Println(add(2, 3))\n}\n"},
		{"comparison by value", point + "func main() {\n\ta := Point{1, 2}\n\tb := Point{1, 2}\n\tc := Point{1, 3}\n\tfmt.Println(a == b, a == c, a != c)\n}\n"},
		{"nested comparison recurses", mixed + "func main() {\n\ta := Outer{V: Inner{1}, K: 2}\n\tb := Outer{V: Inner{1}, K: 2}\n\tc := Outer{V: Inner{9}, K: 2}\n\tfmt.Println(a == b, a == c)\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestEmbeddedPromotion checks embedded-field promotion against go run: a
// promoted field reads and writes through the embedded slot, the embedded field
// is reachable by its type name too, a value copy of the outer struct copies the
// embedded value deeply, and comparison recurses through the embedded field.
func TestEmbeddedPromotion(t *testing.T) {
	t.Parallel()
	const base = "package main\n\nimport \"fmt\"\n\ntype Base struct {\n\tID int\n}\n\ntype User struct {\n\tBase\n\tName int\n}\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{"promoted read and write", base + "func main() {\n\tvar u User\n\tu.ID = 7\n\tu.Name = 3\n\tfmt.Println(u.ID, u.Name, u.Base.ID)\n}\n"},
		{"keyed literal with embedded field", base + "func main() {\n\tu := User{Base: Base{ID: 1}, Name: 2}\n\tfmt.Println(u.ID, u.Name)\n}\n"},
		{"copy is deep through embed", base + "func main() {\n\ta := User{Base: Base{ID: 1}, Name: 2}\n\tb := a\n\tb.ID = 99\n\tfmt.Println(a.ID, b.ID)\n}\n"},
		{"comparison recurses through embed", base + "func main() {\n\ta := User{Base: Base{1}, Name: 2}\n\tb := User{Base: Base{1}, Name: 2}\n\tc := User{Base: Base{5}, Name: 2}\n\tfmt.Println(a == b, a == c)\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestEmbeddedPromotionEmit pins the Python an embedded struct lowers to: the
// embedded field is a value-struct slot named for its type, its constructor
// parameter is suffixed so the Base() call that builds a zero still resolves, and
// a promoted field access reads through the embedded slot with no runtime
// delegation, resolved at the selector the way go/types resolved it.
func TestEmbeddedPromotionEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nimport \"fmt\"\n\ntype Base struct {\n\tID int\n}\n\ntype User struct {\n\tBase\n\tName int\n}\n\nfunc main() {\n\tvar u User\n\tu.ID = 7\n\tfmt.Println(u.ID)\n}\n"
	want := "import " + shim.Name + `


class Base:
    __slots__ = ("ID",)

    def __init__(self, ID=0):
        self.ID = ID

    def copy(self):
        return Base(self.ID)

    def __eq__(self, other):
        if other.__class__ is not Base:
            return NotImplemented
        return self.ID == other.ID

    def __hash__(self):
        return hash((self.ID,))


class User:
    __slots__ = ("Base", "Name")

    def __init__(self, Base_=None, Name=0):
        self.Base = Base_ if Base_ is not None else Base()
        self.Name = Name

    def copy(self):
        return User(self.Base.copy(), self.Name)

    def __eq__(self, other):
        if other.__class__ is not User:
            return NotImplemented
        return self.Base == other.Base and self.Name == other.Name

    def __hash__(self):
        return hash((self.Base, self.Name))


def main():
    u = User()
    u.Base.ID = 7
    ` + shim.Name + `.println(u.Base.ID)


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestFuncParamsEmit pins the plain-function surface: parameters become the
// Python signature and a single return becomes a return statement, with a struct
// argument cloned at the call and a returned struct value cloned at the return,
// the copy-on-call and copy-on-return sites.
func TestFuncParamsEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\ntype Point struct {\n\tX int\n\tY int\n}\n\nfunc idp(p Point) Point {\n\treturn p\n}\n\nfunc main() {\n\ta := Point{1, 2}\n\t_ = idp(a)\n}\n"
	want := `class Point:
    __slots__ = ("X", "Y")

    def __init__(self, X=0, Y=0):
        self.X = X
        self.Y = Y

    def copy(self):
        return Point(self.X, self.Y)

    def __eq__(self, other):
        if other.__class__ is not Point:
            return NotImplemented
        return self.X == other.X and self.Y == other.Y

    def __hash__(self):
        return hash((self.X, self.Y))


def idp(p):
    return p.copy()


def main():
    a = Point(1, 2)
    _ = idp(a.copy())


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestArrayValueSemantics checks that a fixed-length array is a value against go
// run: assignment copies so a later index write to one array leaves the other
// alone, a partial literal pads the missing tail with zeros, a call and a return
// each copy the array, a two-dimensional array copies at every level, an
// array-typed struct field copies deeply with the enclosing struct, an
// array-of-struct literal copies its struct elements, and array equality is
// element-wise like Go's.
func TestArrayValueSemantics(t *testing.T) {
	t.Parallel()
	const point = "package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n\tY int\n}\n\n"
	const grid = "package main\n\nimport \"fmt\"\n\ntype Grid struct {\n\tCells [3]int\n}\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{"copy on assignment is independent", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [3]int{1, 2, 3}\n\tb := a\n\tb[0] = 9\n\tfmt.Println(a, b)\n}\n"},
		{"partial literal pads with zeros", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [5]int{1, 2}\n\tfmt.Println(a)\n}\n"},
		{"copy on call is independent", "package main\n\nimport \"fmt\"\n\nfunc mut(a [3]int) {\n\ta[0] = 99\n}\n\nfunc main() {\n\ta := [3]int{1, 2, 3}\n\tmut(a)\n\tfmt.Println(a)\n}\n"},
		{"copy on return is independent", "package main\n\nimport \"fmt\"\n\nfunc make3() [3]int {\n\tvar a [3]int\n\ta[1] = 5\n\treturn a\n}\n\nfunc main() {\n\ta := make3()\n\tb := make3()\n\ta[0] = 7\n\tfmt.Println(a, b)\n}\n"},
		{"two dimensional array copies deeply", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [2][3]int{{7}}\n\tb := a\n\tb[0][0] = 8\n\tfmt.Println(a, b)\n}\n"},
		{"array struct field copies deeply", grid + "func main() {\n\ta := Grid{}\n\ta.Cells[0] = 1\n\tb := a\n\tb.Cells[0] = 9\n\tfmt.Println(a.Cells, b.Cells)\n}\n"},
		{"array of struct literal copies elements", point + "func main() {\n\tp := Point{1, 2}\n\ta := [2]Point{p}\n\tp.X = 99\n\tfmt.Println(a[0].X, p.X)\n}\n"},
		{"array equality is element wise", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [3]int{1, 2, 3}\n\tb := [3]int{1, 2, 3}\n\tc := [3]int{1, 2, 4}\n\tfmt.Println(a == b, a == c, a != c)\n}\n"},
		{"index read copies a struct element", point + "func main() {\n\ta := [2]Point{{1, 2}, {3, 4}}\n\tq := a[0]\n\tq.X = 99\n\tfmt.Println(a[0].X, q.X)\n}\n"},
		{"len of an array", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [4]int{}\n\tfmt.Println(len(a))\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestArrayEmit pins the Python an array lowers to: a partial literal is a list
// padded to length, a value copy at an assignment goes through the array clone
// helper, an index write is a subscript assignment, and len reads the list
// length with no copy of its argument.
func TestArrayEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [3]int{1, 2}\n\tb := a\n\tb[0] = 9\n\tfmt.Println(a, b, len(a))\n}\n"
	want := "import " + shim.Name + `


def main():
    a = [1, 2, 0]
    b = ` + shim.Name + `._clone_array(a)
    b[0] = 9
    ` + shim.Name + `.println(a, b, len(a))


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestArrayFieldEmit pins the array-typed struct field: the constructor defaults
// it to a fresh zero list through the None sentinel, copy clones it through the
// array helper so the copy never aliases, and the hash projects it to a hashable
// tuple since a Python list is not hashable but a comparable array is a valid Go
// map key.
func TestArrayFieldEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\ntype Grid struct {\n\tCells [3]int\n}\n\nfunc main() {\n\tvar g Grid\n\t_ = g\n}\n"
	want := "import " + shim.Name + `


class Grid:
    __slots__ = ("Cells",)

    def __init__(self, Cells=None):
        self.Cells = Cells if Cells is not None else [0] * 3

    def copy(self):
        return Grid(` + shim.Name + `._clone_array(self.Cells))

    def __eq__(self, other):
        if other.__class__ is not Grid:
            return NotImplemented
        return self.Cells == other.Cells

    def __hash__(self):
        return hash((` + shim.Name + `._arraykey(self.Cells),))


def main():
    g = Grid()
    _ = g


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestArrayMutableZeroEmit pins the fresh-per-slot zero for an array whose
// element is itself a value, a struct or a nested array, where a shared
// multiplied list would alias every slot: each slot is built by a comprehension
// so a write to one element leaves the others untouched.
func TestArrayMutableZeroEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\ntype Point struct {\n\tX int\n\tY int\n}\n\nfunc main() {\n\tvar pts [2]Point\n\tvar grid [2][3]int\n\t_ = pts\n\t_ = grid\n}\n"
	want := `class Point:
    __slots__ = ("X", "Y")

    def __init__(self, X=0, Y=0):
        self.X = X
        self.Y = Y

    def copy(self):
        return Point(self.X, self.Y)

    def __eq__(self, other):
        if other.__class__ is not Point:
            return NotImplemented
        return self.X == other.X and self.Y == other.Y

    def __hash__(self):
        return hash((self.X, self.Y))


def main():
    pts = [Point() for _ in range(2)]
    grid = [[0] * 3 for _ in range(2)]
    _ = pts
    _ = grid


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestSliceValueSemantics checks the slice surface against go run: a slice is a
// header over a shared backing, so a sub-slice aliases the original and a write
// through one is visible through the other, len and cap read the header, and cap
// can exceed len after a make with spare capacity or a reslice into it. It covers
// the literal, the sub-slice alias, the omitted bounds, a nested slice of slices
// that aliases at each level, the nil-slice zero value distinct from an empty
// literal, a slice-typed struct field that shallow-shares its header on a struct
// copy, make with a length and with a length and cap, and a reslice that reaches
// into the reserved capacity.
func TestSliceValueSemantics(t *testing.T) {
	t.Parallel()
	const bag = "package main\n\nimport \"fmt\"\n\ntype Bag struct {\n\tItems []int\n}\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{"literal index and len", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{10, 20, 30}\n\ts[1] = 99\n\tfmt.Println(s, len(s), cap(s))\n}\n"},
		{"sub-slice aliases the backing", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{1, 2, 3, 4}\n\tb := s[1:3]\n\tb[0] = 99\n\tfmt.Println(s, b, len(b), cap(b))\n}\n"},
		{"omitted bounds slice the whole", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{1, 2, 3}\n\tfmt.Println(s[:2], s[1:], s[:])\n}\n"},
		{"nested slice of slices aliases", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tm := [][]int{{1, 2}, {3, 4}}\n\tm[0][0] = 9\n\tfmt.Println(m, m[0][0], m[1][0])\n}\n"},
		{"nil slice zero value", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar s []int\n\tfmt.Println(len(s), cap(s))\n}\n"},
		{"empty literal is not nil", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{}\n\tfmt.Println(len(s), cap(s))\n}\n"},
		{"slice field shares on copy", bag + "func main() {\n\tb := Bag{Items: []int{1, 2, 3}}\n\tc := b\n\tc.Items[0] = 7\n\tfmt.Println(b.Items[0], c.Items[0])\n}\n"},
		{"slice field nil zero value", bag + "func main() {\n\tvar b Bag\n\tfmt.Println(len(b.Items))\n}\n"},
		{"make with a length", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := make([]int, 3)\n\ts[0] = 5\n\tfmt.Println(s, len(s), cap(s))\n}\n"},
		{"make with a length and cap", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := make([]int, 2, 5)\n\tfmt.Println(len(s), cap(s))\n}\n"},
		{"reslice into reserved cap", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := make([]int, 2, 5)\n\tr := s[0:4]\n\tfmt.Println(len(s), cap(s), len(r), cap(r))\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestSliceEmit pins the Python a slice lowers to: a literal is a header over a
// fresh backing through the slice-literal helper, a sub-slice is Python slice
// syntax that the header turns into a new sharing header, an index write is a
// subscript assignment through the header, make builds a header over a zeroed
// backing, len reads the header length, and cap reads the header's cap field
// since the length does not carry the reserved capacity.
func TestSliceEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{1, 2, 3}\n\tt := s[1:3]\n\tt[0] = 9\n\ts = make([]int, 2, 5)\n\tfmt.Println(s, t, len(s), cap(s))\n}\n"
	want := "import " + shim.Name + `


def main():
    s = ` + shim.Name + `._slice_lit([1, 2, 3])
    t = s[1:3]
    t[0] = 9
    s = ` + shim.Name + `.Slice([0] * 5, 0, 2, 5)
    ` + shim.Name + `.println(s, t, len(s), s.cap)


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestSliceFieldEmit pins the slice-typed struct field: it is a scalar-kind field
// whose zero is the nil slice sentinel, so the constructor defaults it to the
// shared sentinel and copy shares the same header, which is the reference
// semantics a slice field wants. The struct holds a slice, so Go makes it not
// comparable, and the emitter leaves it with Python's identity equality, emitting
// no __eq__ or __hash__.
func TestSliceFieldEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\ntype Bag struct {\n\tItems []int\n\tN     int\n}\n\nfunc main() {\n\tvar b Bag\n\t_ = b\n}\n"
	want := "import " + shim.Name + `


class Bag:
    __slots__ = ("Items", "N")

    def __init__(self, Items=` + shim.Name + `.NIL_SLICE, N=0):
        self.Items = Items
        self.N = N

    def copy(self):
        return Bag(self.Items, self.N)


def main():
    b = Bag()
    _ = b


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestSliceMakeEmit pins make's backing form by element mutability: an int slice
// repeats one immutable zero across the backing, while a slice of structs builds
// each slot fresh through a comprehension so the elements do not alias, and the
// header spans the length with the capacity as its reserved size.
func TestSliceMakeEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n\tY int\n}\n\nfunc main() {\n\tps := make([]int, 3, 5)\n\tpts := make([]Point, 2)\n\tpts[0].X = 7\n\tfmt.Println(ps, pts[0].X, len(pts), cap(ps))\n}\n"
	want := "import " + shim.Name + `


class Point:
    __slots__ = ("X", "Y")

    def __init__(self, X=0, Y=0):
        self.X = X
        self.Y = Y

    def copy(self):
        return Point(self.X, self.Y)

    def __eq__(self, other):
        if other.__class__ is not Point:
            return NotImplemented
        return self.X == other.X and self.Y == other.Y

    def __hash__(self):
        return hash((self.X, self.Y))


def main():
    ps = ` + shim.Name + `.Slice([0] * 5, 0, 3, 5)
    pts = ` + shim.Name + `.Slice([Point() for _ in range(2)], 0, 2, 2)
    pts[0].X = 7
    ` + shim.Name + `.println(ps, pts[0].X, len(pts), ps.cap)


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestSliceAppendSemantics runs whole programs three-way against the Go toolchain
// to pin append's behavior: it grows a nil slice into a fresh backing, appends
// several values at once, shares the backing when there is spare capacity so a
// write through the grown slice is visible through the original, reallocates onto
// a fresh backing when the capacity is full so the two slices stop aliasing, copies
// a struct value into the backing rather than sharing it, accumulates across a loop,
// and grows a row of a slice of slices without disturbing the outer slice. None of
// these read cap after a growth, since the exact post-growth capacity is not pinned.
func TestSliceAppendSemantics(t *testing.T) {
	t.Parallel()
	const point = "package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n\tY int\n}\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{"append grows a nil slice", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar s []int\n\ts = append(s, 1)\n\ts = append(s, 2)\n\tfmt.Println(s, len(s))\n}\n"},
		{"append several values at once", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar s []int\n\ts = append(s, 1, 2, 3)\n\tfmt.Println(s, len(s))\n}\n"},
		{"append within cap shares the backing", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := make([]int, 2, 4)\n\ts[0] = 1\n\ts[1] = 2\n\tb := append(s, 3)\n\tb[0] = 99\n\tfmt.Println(s[0], b[0], len(s), len(b))\n}\n"},
		{"append at cap reallocates and un-aliases", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := make([]int, 2, 2)\n\ts[0] = 1\n\ts[1] = 2\n\tb := append(s, 3)\n\tb[0] = 99\n\tfmt.Println(s[0], b[0], len(s), len(b))\n}\n"},
		{"append copies a struct value in", point + "func main() {\n\tvar pts []Point\n\tp := Point{X: 1, Y: 2}\n\tpts = append(pts, p)\n\tp.X = 99\n\tfmt.Println(pts[0].X, pts[0].Y, p.X)\n}\n"},
		{"append accumulates across a loop", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar s []int\n\tfor i := 0; i < 5; i++ {\n\t\ts = append(s, i*i)\n\t}\n\tfmt.Println(s, len(s))\n}\n"},
		{"append grows a row independently", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar grid [][]int\n\trow := []int{1, 2}\n\tgrid = append(grid, row)\n\tr := append(grid[0], 3)\n\tfmt.Println(len(grid), len(r), r[2])\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestSliceAppendEmit pins the Python an append lowers to: the _slice_append
// intrinsic over the slice and the appended values, with a struct value cloned in
// through its copy method the way Go copies a value into the backing, and the
// result assigned back over the slice.
func TestSliceAppendEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\ntype Point struct {\n\tX int\n}\n\nfunc main() {\n\tvar s []int\n\ts = append(s, 1, 2)\n\tvar pts []Point\n\tp := Point{X: 5}\n\tpts = append(pts, p)\n\t_ = s\n\t_ = pts\n}\n"
	want := "import " + shim.Name + `


class Point:
    __slots__ = ("X",)

    def __init__(self, X=0):
        self.X = X

    def copy(self):
        return Point(self.X)

    def __eq__(self, other):
        if other.__class__ is not Point:
            return NotImplemented
        return self.X == other.X

    def __hash__(self):
        return hash((self.X,))


def main():
    s = ` + shim.Name + `.NIL_SLICE
    s = ` + shim.Name + `._slice_append(s, 1, 2)
    pts = ` + shim.Name + `.NIL_SLICE
    p = Point(X=5)
    pts = ` + shim.Name + `._slice_append(pts, p.copy())
    _ = s
    _ = pts


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestSliceThreeIndexSemantics checks the full slice s[low:high:max] against Go:
// the result shares the operand's backing, its length runs low to high, and its
// capacity is capped at max minus low, so an append that overflows the capped
// capacity reallocates and stops sharing exactly where Go's does.
func TestSliceThreeIndexSemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{"capped capacity bounds a later append", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := make([]int, 4, 8)\n\ts[0] = 1\n\ts[1] = 2\n\tt := s[0:1:2]\n\tfmt.Println(len(t), cap(t))\n}\n"},
		{"within capped cap the append shares", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := make([]int, 2, 8)\n\ts[0] = 1\n\ts[1] = 2\n\tt := s[0:1:4]\n\tt = append(t, 9)\n\tfmt.Println(s[1], len(t), cap(t))\n}\n"},
		{"at capped cap the append reallocates", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := make([]int, 2, 8)\n\ts[0] = 1\n\ts[1] = 2\n\tt := s[0:1:1]\n\tt = append(t, 9)\n\tt[0] = 99\n\tfmt.Println(s[0], t[0], len(t))\n}\n"},
		{"omitted low defaults to zero", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := make([]int, 4, 8)\n\tt := s[:2:3]\n\tfmt.Println(len(t), cap(t))\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestSliceThreeIndexEmit pins the Python a full slice lowers to: the _subslice3
// helper over the operand and its three bounds, the low bound defaulting to 0
// when the source omits it, since Python's slice syntax carries no third bound.
func TestSliceThreeIndexEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nfunc main() {\n\ts := make([]int, 4, 8)\n\tt := s[1:2:3]\n\tu := s[:2:3]\n\t_ = t\n\t_ = u\n}\n"
	want := "import " + shim.Name + `


def main():
    s = ` + shim.Name + `.Slice([0] * 8, 0, 4, 8)
    t = ` + shim.Name + `._subslice3(s, 1, 2, 3)
    u = ` + shim.Name + `._subslice3(s, 0, 2, 3)
    _ = t
    _ = u


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestSliceCopySemantics checks copy(dst, src) against Go: it moves the overlap
// of the two slices, returns that count, writes through the destination's backing
// so a slice sharing it sees the change, and moves safely when the source and
// destination overlap in one backing.
func TestSliceCopySemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{"copy returns the shorter length", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tsrc := []int{1, 2, 3}\n\tdst := make([]int, 2)\n\tn := copy(dst, src)\n\tfmt.Println(n, dst[0], dst[1])\n}\n"},
		{"copy fills through the shared backing", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tsrc := []int{7, 8, 9}\n\tdst := make([]int, 3)\n\tcopy(dst, src)\n\tfmt.Println(dst[0], dst[1], dst[2])\n}\n"},
		{"copy from a longer source truncates", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tsrc := []int{1, 2, 3, 4}\n\tdst := make([]int, 2)\n\tn := copy(dst, src)\n\tfmt.Println(n, len(dst))\n}\n"},
		{"copy overlaps within one backing", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{1, 2, 3, 4, 5}\n\tn := copy(s[1:], s[0:4])\n\tfmt.Println(n, s[0], s[1], s[2], s[3], s[4])\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestSliceCopyEmit pins the Python a copy lowers to: the _slice_copy intrinsic
// over the two slices, both lowered plainly since copy moves through the backings
// and copies neither header.
func TestSliceCopyEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nfunc main() {\n\tsrc := []int{1, 2, 3}\n\tdst := make([]int, 3)\n\tn := copy(dst, src)\n\t_ = n\n}\n"
	want := "import " + shim.Name + `


def main():
    src = ` + shim.Name + `._slice_lit([1, 2, 3])
    dst = ` + shim.Name + `.Slice([0] * 3, 0, 3, 3)
    n = ` + shim.Name + `._slice_copy(dst, src)
    _ = n


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestMaps checks the map surface against go run: a literal and a make build a
// dict, an index reads a present value or the value type's zero on a miss, an
// index target writes an entry, the comma-ok read reports presence, delete removes
// a key and is a no-op on a missing one, clear empties the map, len counts the
// entries, fmt prints the entries with sorted keys, and a range walks a snapshot so
// a delete during it is safe. It also covers the nil map, which reads as empty,
// prints as map[], and reports absence, and the value and key copies a struct map
// makes so a mutation never reaches back into the map.
func TestMaps(t *testing.T) {
	t.Parallel()
	const pre = "package main\n\nimport \"fmt\"\n\n"
	const boxPre = pre + "type Box struct {\n\tN int\n}\n\n"
	const pointPre = pre + "type Point struct {\n\tX int\n\tY int\n}\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{"literal and read", pre + "func main() {\n\tm := map[string]int{\"a\": 1, \"b\": 2}\n\tfmt.Println(m[\"a\"], m[\"b\"])\n}\n"},
		{"make and write", pre + "func main() {\n\tm := make(map[string]int)\n\tm[\"x\"] = 5\n\tm[\"y\"] = 7\n\tfmt.Println(m[\"x\"], m[\"y\"])\n}\n"},
		{"read missing is zero", pre + "func main() {\n\tm := map[string]int{\"a\": 1}\n\tfmt.Println(m[\"z\"])\n}\n"},
		{"write overwrites", pre + "func main() {\n\tm := map[string]int{\"a\": 1}\n\tm[\"a\"] = 9\n\tfmt.Println(m[\"a\"])\n}\n"},
		{"comma-ok present and absent", pre + "func main() {\n\tm := map[string]int{\"a\": 1}\n\tv, ok := m[\"a\"]\n\tw, ok2 := m[\"z\"]\n\tfmt.Println(v, ok, w, ok2)\n}\n"},
		{"delete then check", pre + "func main() {\n\tm := map[string]int{\"a\": 1, \"b\": 2}\n\tdelete(m, \"a\")\n\t_, ok := m[\"a\"]\n\tfmt.Println(ok, len(m))\n}\n"},
		{"delete missing is a no-op", pre + "func main() {\n\tm := map[string]int{\"a\": 1}\n\tdelete(m, \"z\")\n\tfmt.Println(len(m))\n}\n"},
		{"len counts entries", pre + "func main() {\n\tm := map[int]int{1: 1, 2: 2, 3: 3}\n\tfmt.Println(len(m))\n}\n"},
		{"print sorts string keys", pre + "func main() {\n\tm := map[string]int{\"b\": 2, \"a\": 1, \"c\": 3}\n\tfmt.Println(m)\n}\n"},
		{"print sorts int keys", pre + "func main() {\n\tm := map[int]string{3: \"c\", 1: \"a\", 2: \"b\"}\n\tfmt.Println(m)\n}\n"},
		{"print empty map", pre + "func main() {\n\tm := make(map[int]int)\n\tfmt.Println(m)\n}\n"},
		{"bool value default", pre + "func main() {\n\tm := map[string]bool{\"a\": true}\n\tfmt.Println(m[\"a\"], m[\"z\"])\n}\n"},
		{"clear empties the map", pre + "func main() {\n\tm := map[string]int{\"a\": 1, \"b\": 2}\n\tclear(m)\n\tfmt.Println(len(m))\n}\n"},
		{"range sums values", pre + "func main() {\n\tm := map[string]int{\"a\": 1, \"b\": 2, \"c\": 3}\n\tsum := 0\n\tfor _, v := range m {\n\t\tsum += v\n\t}\n\tfmt.Println(sum)\n}\n"},
		{"range sums keys", pre + "func main() {\n\tm := map[int]int{1: 0, 2: 0, 4: 0}\n\tn := 0\n\tfor k := range m {\n\t\tn += k\n\t}\n\tfmt.Println(n)\n}\n"},
		{"range key and value", pre + "func main() {\n\tm := map[int]int{1: 10, 2: 20}\n\ts := 0\n\tfor k, v := range m {\n\t\ts += k * v\n\t}\n\tfmt.Println(s)\n}\n"},
		{"delete during range", pre + "func main() {\n\tm := map[int]int{1: 1, 2: 2, 3: 3}\n\tfor k := range m {\n\t\tif k == 2 {\n\t\t\tdelete(m, 2)\n\t\t}\n\t}\n\tfmt.Println(len(m))\n}\n"},
		{"nil map reads empty", pre + "func main() {\n\tvar m map[string]int\n\tfmt.Println(m[\"x\"], len(m))\n}\n"},
		{"nil map prints empty", pre + "func main() {\n\tvar m map[string]int\n\tfmt.Println(m)\n}\n"},
		{"nil map comma-ok", pre + "func main() {\n\tvar m map[string]int\n\tv, ok := m[\"x\"]\n\tfmt.Println(v, ok)\n}\n"},
		{"nil map delete is a no-op", pre + "func main() {\n\tvar m map[string]int\n\tdelete(m, \"x\")\n\tfmt.Println(len(m))\n}\n"},
		{"nil map ranges nothing", pre + "func main() {\n\tvar m map[int]int\n\tn := 0\n\tfor range m {\n\t\tn++\n\t}\n\tfmt.Println(n)\n}\n"},
		{"struct value read copies", boxPre + "func main() {\n\tm := map[string]Box{\"a\": {1}}\n\tb := m[\"a\"]\n\tb.N = 99\n\tfmt.Println(m[\"a\"].N, b.N)\n}\n"},
		{"struct value comma-ok copies", boxPre + "func main() {\n\tm := map[string]Box{\"a\": {5}}\n\tb, ok := m[\"a\"]\n\tb.N = 42\n\tfmt.Println(m[\"a\"].N, b.N, ok)\n}\n"},
		{"struct key lookup", pointPre + "func main() {\n\tm := map[Point]int{}\n\tm[Point{1, 2}] = 7\n\tfmt.Println(m[Point{1, 2}], m[Point{3, 4}])\n}\n"},
		{"struct key copies on insert", pointPre + "func main() {\n\tp := Point{1, 2}\n\tm := map[Point]int{}\n\tm[p] = 5\n\tp.X = 9\n\tfmt.Println(m[Point{1, 2}], m[Point{9, 2}])\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestMapEmit pins the Python the map operations lower to: a make is an empty
// dict, an index target writes an entry, an index read routes through _map_index
// with the value type's zero, the comma-ok read is a tuple assignment from
// _map_lookup, and delete and clear are their intrinsics. A Go string key is
// Python bytes, so it emits with the b prefix.
func TestMapEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nfunc main() {\n\tm := make(map[string]int)\n\tm[\"a\"] = 1\n\tx := m[\"a\"]\n\tv, ok := m[\"b\"]\n\tdelete(m, \"a\")\n\tclear(m)\n\t_ = x\n\t_ = v\n\t_ = ok\n}\n"
	want := "import " + shim.Name + `


def main():
    m = {}
    m[b"a"] = 1
    x = ` + shim.Name + `._map_index(m, b"a", 0)
    v, ok = ` + shim.Name + `._map_lookup(m, b"b", 0)
    ` + shim.Name + `._map_delete(m, b"a")
    ` + shim.Name + `._map_clear(m)
    _ = x
    _ = v
    _ = ok


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestMapRangeEmit pins the Python a range over a map lowers to: a two-variable
// range walks a snapshot of the key-value pairs through _map_items, and a key-only
// range walks a snapshot of the keys through _map_keys, so a delete during the
// range is safe.
func TestMapRangeEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nfunc main() {\n\tm := map[int]int{1: 10, 2: 20}\n\tfor k, v := range m {\n\t\t_ = k\n\t\t_ = v\n\t}\n\tfor k := range m {\n\t\t_ = k\n\t}\n}\n"
	want := "import " + shim.Name + `


def main():
    m = {1: 10, 2: 20}
    for k, v in ` + shim.Name + `._map_items(m):
        _ = k
        _ = v
    for k in ` + shim.Name + `._map_keys(m):
        _ = k


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestCompositeLiterals runs the index-keyed and nested composite literal forms
// three-way against go run. It covers a sparse array and slice that leave zeroed
// gaps, a literal that mixes a keyed and a positional element so the running
// index continues past the key, and nested elided literals where the inner
// element type is left off, which compose through the recursive lowering.
func TestCompositeLiterals(t *testing.T) {
	t.Parallel()
	const pointPre = "package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n\tY int\n}\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{"sparse array fills gaps with zeros", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [5]int{0: 1, 4: 9}\n\tfmt.Println(a)\n}\n"},
		{"sparse array out of order", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [4]int{2: 30, 0: 10}\n\tfmt.Println(a)\n}\n"},
		{"keyed array shorter than length pads", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [6]int{1: 2, 3: 4}\n\tfmt.Println(a, len(a))\n}\n"},
		{"sparse slice spans to highest index", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{2: 5}\n\tfmt.Println(s, len(s))\n}\n"},
		{"mixed keyed and positional slice", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{1, 5: 6, 7}\n\tfmt.Println(s, len(s))\n}\n"},
		{"keyed then continue index", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{3: 30, 40, 50}\n\tfmt.Println(s, len(s))\n}\n"},
		{"nested elided slice of struct", pointPre + "func main() {\n\ts := []Point{{1, 2}, {3, 4}}\n\tfmt.Println(s[0].X, s[1].Y)\n}\n"},
		{"nested elided array of struct", pointPre + "func main() {\n\ta := [2]Point{{1, 2}, {3, 4}}\n\tfmt.Println(a[0].X, a[1].Y)\n}\n"},
		{"nested elided array of array", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [2][3]int{{1, 2, 3}, {4, 5, 6}}\n\tfmt.Println(a)\n}\n"},
		{"nested elided slice of slice", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := [][]int{{1, 2}, {3, 4, 5}}\n\tfmt.Println(s[0], s[1], len(s[1]))\n}\n"},
		{"map value elided struct", pointPre + "func main() {\n\tm := map[string]Point{\"a\": {1, 2}}\n\tfmt.Println(m[\"a\"].X, m[\"a\"].Y)\n}\n"},
		{"struct field nested literal", "package main\n\nimport \"fmt\"\n\ntype Grid struct {\n\tCells [2]int\n}\n\nfunc main() {\n\tg := Grid{Cells: [2]int{7, 8}}\n\tfmt.Println(g.Cells)\n}\n"},
		{"sparse array of struct pads fresh zeros", pointPre + "func main() {\n\ta := [3]Point{1: {5, 6}}\n\ta[0].X = 1\n\tfmt.Println(a[0].X, a[1].X, a[2].X)\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestCompositeLiteralEmit pins the Python the new literal forms lower to: a
// sparse array is a dense list with zeroed gaps, a sparse slice spans to its
// highest index through _slice_lit, and a nested elided slice of structs builds
// the inner constructor calls directly.
func TestCompositeLiteralEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\ntype Point struct {\n\tX int\n\tY int\n}\n\nfunc main() {\n\ta := [5]int{0: 1, 4: 9}\n\ts := []int{2: 5}\n\tp := []Point{{1, 2}, {3, 4}}\n\t_ = a\n\t_ = s\n\t_ = p\n}\n"
	want := "import " + shim.Name + `


class Point:
    __slots__ = ("X", "Y")

    def __init__(self, X=0, Y=0):
        self.X = X
        self.Y = Y

    def copy(self):
        return Point(self.X, self.Y)

    def __eq__(self, other):
        if other.__class__ is not Point:
            return NotImplemented
        return self.X == other.X and self.Y == other.Y

    def __hash__(self):
        return hash((self.X, self.Y))


def main():
    a = [1, 0, 0, 0, 9]
    s = ` + shim.Name + `._slice_lit([0, 0, 5])
    p = ` + shim.Name + `._slice_lit([Point(1, 2), Point(3, 4)])
    _ = a
    _ = s
    _ = p


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPointers(t *testing.T) {
	t.Parallel()
	const pointPre = "package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n\tY int\n}\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{"read through field pointer", pointPre + "func main() {\n\tp := Point{1, 2}\n\tpx := &p.X\n\tfmt.Println(*px)\n}\n"},
		{"write through field pointer", pointPre + "func main() {\n\tp := Point{1, 2}\n\tpx := &p.X\n\t*px = 9\n\tfmt.Println(p.X)\n}\n"},
		{"two field pointers in an expression", pointPre + "func main() {\n\tp := Point{3, 4}\n\tpx := &p.X\n\tpy := &p.Y\n\tfmt.Println(*px + *py)\n}\n"},
		{"write through array element pointer", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := [3]int{1, 2, 3}\n\tq := &a[1]\n\t*q = 20\n\tfmt.Println(a)\n}\n"},
		{"write through slice element pointer", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{1, 2, 3}\n\tq := &s[2]\n\t*q = 30\n\tfmt.Println(s)\n}\n"},
		{"slice element pointer writes shared backing", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ts := []int{1, 2, 3}\n\tt := s[:]\n\tq := &s[0]\n\t*q = 100\n\tfmt.Println(t[0])\n}\n"},
		{"promoted field pointer", "package main\n\nimport \"fmt\"\n\ntype Base struct {\n\tID int\n}\n\ntype User struct {\n\tBase\n\tName string\n}\n\nfunc main() {\n\tu := User{Base{5}, \"a\"}\n\tp := &u.ID\n\t*p = 9\n\tfmt.Println(u.ID)\n}\n"},
		{"write struct value through element pointer", pointPre + "func main() {\n\ta := [2]Point{{1, 2}, {3, 4}}\n\tq := &a[0]\n\t*q = Point{7, 8}\n\tfmt.Println(a[0].X, a[0].Y)\n}\n"},
		{"struct written through field pointer is cloned", "package main\n\nimport \"fmt\"\n\ntype Inner struct {\n\tV int\n}\n\ntype Outer struct {\n\tIn Inner\n}\n\nfunc main() {\n\to := Outer{Inner{5}}\n\tp := &o.In\n\tsrc := Inner{9}\n\t*p = src\n\tsrc.V = 100\n\tfmt.Println(o.In.V)\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestPointerEmit pins the Python the address-of and deref forms lower to: a
// field address is a FieldPtr over the object and the field name, an element
// address is an IndexPtr over the sequence and the index, a deref read is the
// pointer's get, and a deref-assign is the pointer's set.
func TestPointerEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\ntype Point struct {\n\tX int\n\tY int\n}\n\nfunc main() {\n\tp := Point{1, 2}\n\tpx := &p.X\n\t*px = 9\n\ta := [3]int{1, 2, 3}\n\tq := &a[1]\n\t_ = *q\n\t_ = px\n}\n"
	want := "import " + shim.Name + `


class Point:
    __slots__ = ("X", "Y")

    def __init__(self, X=0, Y=0):
        self.X = X
        self.Y = Y

    def copy(self):
        return Point(self.X, self.Y)

    def __eq__(self, other):
        if other.__class__ is not Point:
            return NotImplemented
        return self.X == other.X and self.Y == other.Y

    def __hash__(self):
        return hash((self.X, self.Y))


def main():
    p = Point(1, 2)
    px = ` + shim.Name + `.FieldPtr(p, "X")
    px.set(9)
    a = [1, 2, 3]
    q = ` + shim.Name + `.IndexPtr(a, 1)
    _ = q.get()
    _ = px


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestMultipleReturns checks multiple and named return values against go run: a
// two-value return unpacked at the call site, a three-value return, a named
// result set by name and handed back by a bare return, a named struct result, a
// parallel assignment and a swap, a struct returned twice proving copy on
// return, a struct parallel assignment proving the copy, and a blank target
// discarding one of the returned values.
func TestMultipleReturns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{"two value return", "package main\n\nimport \"fmt\"\n\nfunc swap(a, b int) (int, int) {\n\treturn b, a\n}\n\nfunc main() {\n\tx, y := swap(1, 2)\n\tfmt.Println(x, y)\n}\n"},
		{"three value return", "package main\n\nimport \"fmt\"\n\nfunc three() (int, int, int) {\n\treturn 1, 2, 3\n}\n\nfunc main() {\n\ta, b, c := three()\n\tfmt.Println(a, b, c)\n}\n"},
		{"named results bare return", "package main\n\nimport \"fmt\"\n\nfunc split(n int) (lo, hi int) {\n\tlo = n - 1\n\thi = n + 1\n\treturn\n}\n\nfunc main() {\n\ta, b := split(5)\n\tfmt.Println(a, b)\n}\n"},
		{"named struct result", "package main\n\nimport \"fmt\"\n\ntype P struct {\n\tX int\n}\n\nfunc build() (p P) {\n\tp.X = 7\n\treturn\n}\n\nfunc main() {\n\tq := build()\n\tfmt.Println(q.X)\n}\n"},
		{"parallel assign and swap", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta, b := 1, 2\n\ta, b = b, a\n\tfmt.Println(a, b)\n}\n"},
		{"struct returned twice copies", "package main\n\nimport \"fmt\"\n\ntype Box struct {\n\tV int\n}\n\nfunc two() (Box, Box) {\n\tb := Box{1}\n\treturn b, b\n}\n\nfunc main() {\n\tx, y := two()\n\tx.V = 100\n\tfmt.Println(x.V, y.V)\n}\n"},
		{"struct parallel assign copies", "package main\n\nimport \"fmt\"\n\ntype P struct {\n\tX int\n}\n\nfunc main() {\n\ta := P{1}\n\tb := P{2}\n\ta, b = b, a\n\tb.X = 9\n\tfmt.Println(a.X, b.X)\n}\n"},
		{"blank target discards", "package main\n\nimport \"fmt\"\n\nfunc pair() (int, int) {\n\treturn 3, 4\n}\n\nfunc main() {\n\t_, y := pair()\n\tfmt.Println(y)\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestMultipleReturnEmit pins the Python a multiple return and its consumers
// lower to: a several-value return is a parenthesized tuple, an unpack of a call
// is a tuple-target assignment from the call, and a parallel assignment is a
// tuple-target assignment from a tuple of the right-side values.
func TestMultipleReturnEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nfunc swap(a, b int) (int, int) {\n\treturn b, a\n}\n\nfunc main() {\n\tx, y := swap(1, 2)\n\tx, y = y, x\n\t_ = x\n\t_ = y\n}\n"
	want := `def swap(a, b):
    return (b, a)


def main():
    x, y = swap(1, 2)
    x, y = (y, x)
    _ = x
    _ = y


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestNamedResultEmit pins the Python a named result lowers to: a zero-init
// prelude binds each result name at the function top, and a bare return hands
// back the named bindings as a tuple.
func TestNamedResultEmit(t *testing.T) {
	t.Parallel()
	source := "package main\n\nfunc split(n int) (lo, hi int) {\n\tlo = n\n\thi = n\n\treturn\n}\n\nfunc main() {\n\ta, b := split(3)\n\t_ = a\n\t_ = b\n}\n"
	want := `def split(n):
    lo = 0
    hi = 0
    lo = n
    hi = n
    return (lo, hi)


def main():
    a, b = split(3)
    _ = a
    _ = b


if __name__ == "__main__":
    main()
`
	if got := emitOf(t, source); got != want {
		t.Errorf("emit mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}
