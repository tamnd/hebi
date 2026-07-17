package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceEmbedded(t *testing.T) {
	t.Parallel()
	src := Source()
	if strings.TrimSpace(src) == "" {
		t.Fatal("embedded shim source is empty")
	}
	for _, want := range []string{"def go_str", "def println", "def _u8", "def _i8", "def _u64", "def _i64", "def _f32", "def _gofloat", "def _gofloat32", "true", "false"} {
		if !strings.Contains(src, want) {
			t.Errorf("shim source missing %q", want)
		}
	}
}

// TestWidthHelpers runs the masking helpers under CPython and checks they wrap
// two's-complement the Go way: unsigned masks, signed masks then sign-extends.
func TestWidthHelpers(t *testing.T) {
	t.Parallel()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name+".py"), []byte(Source()), 0o644); err != nil {
		t.Fatal(err)
	}
	prog := "import _hebirt as r\n" +
		"print(r._u8(300), r._i8(200), r._u32(-1), r._i16(-1), r._u64(-1), r._i64(0x8000000000000000))"
	cmd := exec.CommandContext(t.Context(), py, "-c", prog)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run helpers: %v\n%s", err, out)
	}
	want := "44 -56 4294967295 -1 18446744073709551615 -9223372036854775808\n"
	if got := string(out); got != want {
		t.Errorf("helper output = %q, want %q", got, want)
	}
}

// TestFloatHelpers runs the float helpers under CPython and pins Go's float
// formatting: the shortest form, the exponent-notation threshold that differs
// from Python's, the integer-valued float that drops its point, the special
// values, and the single-precision narrowing and its shortest form.
func TestFloatHelpers(t *testing.T) {
	t.Parallel()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name+".py"), []byte(Source()), 0o644); err != nil {
		t.Fatal(err)
	}
	prog := "import _hebirt as r\n" +
		"print(r._gofloat(3.14), r._gofloat(3.0), r._gofloat(1000000.0), r._gofloat(1e-5), r._gofloat(-2.5))\n" +
		"print(r._gofloat(float('nan')), r._gofloat(float('inf')), r._gofloat(float('-inf')))\n" +
		"print(r._gofloat32(r._f32(0.1)), r._gofloat32(r._f32(r._f32(0.1) + r._f32(0.2))))"
	cmd := exec.CommandContext(t.Context(), py, "-c", prog)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run float helpers: %v\n%s", err, out)
	}
	want := "3.14 3 1e+06 1e-05 -2.5\n" +
		"NaN +Inf -Inf\n" +
		"0.1 0.3\n"
	if got := string(out); got != want {
		t.Errorf("float helper output = %q, want %q", got, want)
	}
}

// TestShimBehavior runs the embedded shim under CPython and checks it formats
// values the Go way: booleans as true and false, operands space-joined with a
// trailing newline, matching fmt.Println. It is skipped where python3 is not
// on the path so the unit suite stays runnable without a toolchain.
func TestShimBehavior(t *testing.T) {
	t.Parallel()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name+".py"), []byte(Source()), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), py, "-c", "import _hebirt; _hebirt.println(1, True, False, \"hi\")")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run shim: %v\n%s", err, out)
	}
	if got, want := string(out), "1 true false hi\n"; got != want {
		t.Errorf("println output = %q, want %q", got, want)
	}
}
