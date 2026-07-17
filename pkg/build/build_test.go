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
		{"compound assign", "package main\n\nfunc main() {\n\tx := 0\n\tx += 1\n\t_ = x\n}\n"},
		{"unsupported call", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Exit(0)\n}\n"},
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
