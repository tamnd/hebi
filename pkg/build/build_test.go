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
