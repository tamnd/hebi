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
	for _, want := range []string{"def go_str", "def println", "def _u8", "def _i8", "def _u64", "def _i64", "def _f32", "def _gofloat", "def _gofloat32", "def _clone_array", "def _arraykey", "class Slice", "def _slice_lit", "def _subslice", "def _subslice3", "NIL_SLICE", "def _slice_append", "def _grow", "def _slice_copy", "def _decode_rune", "class _NilMap", "NIL_MAP", "def _map_index", "def _map_lookup", "def _map_delete", "def _map_clear", "def _map_items", "def _map_keys", "true", "false"} {
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

// TestStringHelpers runs the string surface under CPython: a Go string is bytes,
// so printing one decodes the UTF-8 back to text, indexing yields a byte, and the
// rune decoder walks a multibyte string the way Go's range does, returning the
// byte width of each rune and the replacement rune for an invalid lead byte.
func TestStringHelpers(t *testing.T) {
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
		"s = 'héllo'.encode('utf-8')\n" +
		"print(r.go_str(s), len(s), s[1])\n" +
		"print(r._decode_rune(s, 0), r._decode_rune(s, 1), r._decode_rune(s, 3))\n" +
		"print(r._decode_rune(b'\\xff', 0), r._decode_rune(b'\\xe2\\x82', 0))"
	cmd := exec.CommandContext(t.Context(), py, "-c", prog)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run string helpers: %v\n%s", err, out)
	}
	want := "héllo 6 195\n" +
		"(104, 1) (233, 2) (108, 1)\n" +
		"(65533, 1) (65533, 1)\n"
	if got := string(out); got != want {
		t.Errorf("string helper output = %q, want %q", got, want)
	}
}

// TestArrayHelpers runs the array surface under CPython: go_str prints a list
// in Go's bracket form with nested arrays recursing, the clone helper deep-copies
// so a write to the copy leaves the original alone at every level including a
// struct element cloned through its own copy method, and the array-key helper
// projects an array to a hashable tuple.
func TestArrayHelpers(t *testing.T) {
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
		"print(r.go_str([1, 2, 3]), r.go_str([[1, 2], [3, 4]]))\n" +
		"a = [[1, 2], [3, 4]]\n" +
		"b = r._clone_array(a)\n" +
		"b[0][0] = 9\n" +
		"print(r.go_str(a), r.go_str(b))\n" +
		"class P:\n" +
		"    def __init__(self, x):\n" +
		"        self.x = x\n" +
		"    def copy(self):\n" +
		"        return P(self.x)\n" +
		"c = [P(1)]\n" +
		"d = r._clone_array(c)\n" +
		"d[0].x = 9\n" +
		"print(c[0].x, d[0].x)\n" +
		"print(r._arraykey([1, [2, 3]]))"
	cmd := exec.CommandContext(t.Context(), py, "-c", prog)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run array helpers: %v\n%s", err, out)
	}
	want := "[1 2 3] [[1 2] [3 4]]\n" +
		"[[1 2] [3 4]] [[9 2] [3 4]]\n" +
		"1 9\n" +
		"(1, (2, 3))\n"
	if got := string(out); got != want {
		t.Errorf("array helper output = %q, want %q", got, want)
	}
}

// TestSliceHelpers runs the slice surface under CPython: a literal is a header
// over a fresh backing, a sub-slice shares that backing so a write through the
// sub-slice is visible in the original, len reads the header length and cap reads
// the reserved capacity which the sub-slice narrows, go_str prints the visible
// length in Go's bracket form with a nested slice recursing, and the nil sentinel
// has length and capacity zero.
func TestSliceHelpers(t *testing.T) {
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
		"a = r._slice_lit([1, 2, 3, 4])\n" +
		"b = a[1:3]\n" +
		"b[0] = 99\n" +
		"print(r.go_str(a), r.go_str(b), len(b), b.cap)\n" +
		"print(a[1], len(a), a.cap)\n" +
		"print(r.go_str(r.NIL_SLICE), len(r.NIL_SLICE), r.NIL_SLICE.cap)\n" +
		"m = r._slice_lit([r._slice_lit([1, 2]), r._slice_lit([3, 4])])\n" +
		"print(r.go_str(m))"
	cmd := exec.CommandContext(t.Context(), py, "-c", prog)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run slice helpers: %v\n%s", err, out)
	}
	want := "[1 99 3 4] [99 3] 2 3\n" +
		"99 4 4\n" +
		"[] 0 0\n" +
		"[[1 2] [3 4]]\n"
	if got := string(out); got != want {
		t.Errorf("slice helper output = %q, want %q", got, want)
	}
}

// TestSliceAppendHelpers runs the append surface under CPython: appending to a
// full slice reallocates onto a fresh backing so a later write through the grown
// slice leaves the original alone, appending into spare capacity shares the backing
// so the write is visible through both, appending to the nil sentinel produces a
// fresh non-nil slice, and the growth curve doubles while small and eases toward a
// quarter once large.
func TestSliceAppendHelpers(t *testing.T) {
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
		"a = r._slice_lit([1, 2])\n" +
		"b = r._slice_append(a, 3)\n" +
		"b[0] = 99\n" +
		"print(r.go_str(a), r.go_str(b), len(b))\n" +
		"s = r.Slice([0, 0, 0, 0], 0, 2, 4)\n" +
		"s[0] = 1\n" +
		"s[1] = 2\n" +
		"t = r._slice_append(s, 5)\n" +
		"t[0] = 7\n" +
		"print(s[0], t[0], len(s), len(t))\n" +
		"n = r._slice_append(r.NIL_SLICE, 8)\n" +
		"print(r.go_str(n), len(n))\n" +
		"print(r._grow(0, 1), r._grow(4, 5), r._grow(2, 3), r._grow(256, 257))"
	cmd := exec.CommandContext(t.Context(), py, "-c", prog)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run append helpers: %v\n%s", err, out)
	}
	want := "[1 2] [99 2 3] 3\n" +
		"7 7 2 3\n" +
		"[8] 1\n" +
		"1 8 4 512\n"
	if got := string(out); got != want {
		t.Errorf("append helper output = %q, want %q", got, want)
	}
}

// TestSliceThreeIndexHelpers runs the full slice surface under CPython: the
// _subslice3 header shares the operand's backing so a write through it is visible
// through the operand, its length runs low to high, and its capacity is capped at
// max minus low rather than running to the end of the backing.
func TestSliceThreeIndexHelpers(t *testing.T) {
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
		"s = r.Slice([1, 2, 3, 4, 5], 0, 4, 5)\n" +
		"t = r._subslice3(s, 1, 3, 4)\n" +
		"print(r.go_str(t), len(t), t.cap)\n" +
		"t[0] = 99\n" +
		"print(s[1])"
	cmd := exec.CommandContext(t.Context(), py, "-c", prog)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run three-index helpers: %v\n%s", err, out)
	}
	want := "[2 3] 2 3\n99\n"
	if got := string(out); got != want {
		t.Errorf("three-index helper output = %q, want %q", got, want)
	}
}

// TestSliceCopyHelpers runs the copy surface under CPython: copy moves the overlap
// of the two slices and returns that count, writing through the destination's
// backing, and it moves safely when the source and destination overlap in one
// backing, matching Go's memmove so a forward-overlapping copy runs backward.
func TestSliceCopyHelpers(t *testing.T) {
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
		"src = r._slice_lit([1, 2, 3])\n" +
		"dst = r.Slice([0, 0], 0, 2, 2)\n" +
		"n = r._slice_copy(dst, src)\n" +
		"print(n, r.go_str(dst))\n" +
		"s = r._slice_lit([1, 2, 3, 4, 5])\n" +
		"m = r._slice_copy(r._subslice(s, slice(1, None)), r._subslice(s, slice(0, 4)))\n" +
		"print(m, r.go_str(s))"
	cmd := exec.CommandContext(t.Context(), py, "-c", prog)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run copy helpers: %v\n%s", err, out)
	}
	want := "2 [1 2]\n4 [1 1 2 3 4]\n"
	if got := string(out); got != want {
		t.Errorf("copy helper output = %q, want %q", got, want)
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

// TestMapHelpers runs the map surface under CPython: a read of a present key
// returns its value and a read of a missing key the supplied zero, the comma-ok
// lookup reports presence, delete removes a key and is a no-op on a missing one,
// clear empties the map, the snapshot helpers let a delete during a range run
// safely, and the nil map reads as empty, yields nothing, prints as map[], and
// panics the Go way on a write.
func TestMapHelpers(t *testing.T) {
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
		"m = {}\n" +
		"m['a'] = 1\n" +
		"print(r._map_index(m, 'a', 0), r._map_index(m, 'z', 0))\n" +
		"print(r._map_lookup(m, 'a', 0), r._map_lookup(m, 'z', 0))\n" +
		"r._map_delete(m, 'a')\n" +
		"r._map_delete(m, 'z')\n" +
		"print(len(m))\n" +
		"d = {1: 1, 2: 2, 3: 3}\n" +
		"for k in r._map_keys(d):\n" +
		"    if k == 2:\n" +
		"        r._map_delete(d, 2)\n" +
		"print(len(d), r.go_str(d))\n" +
		"r._map_clear(d)\n" +
		"print(len(d), r.go_str(d))\n" +
		"print(r._map_index(r.NIL_MAP, 'x', 0), len(r.NIL_MAP), r.go_str(r.NIL_MAP))\n" +
		"print(r._map_lookup(r.NIL_MAP, 'x', 0), r._map_keys(r.NIL_MAP), r._map_items(r.NIL_MAP))\n" +
		"r._map_delete(r.NIL_MAP, 'x')\n" +
		"r._map_clear(r.NIL_MAP)\n" +
		"try:\n" +
		"    r.NIL_MAP['x'] = 1\n" +
		"    print('no panic')\n" +
		"except Exception as e:\n" +
		"    print(e)"
	cmd := exec.CommandContext(t.Context(), py, "-c", prog)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run map helpers: %v\n%s", err, out)
	}
	want := "1 0\n" +
		"(1, True) (0, False)\n" +
		"0\n" +
		"2 map[1:1 3:3]\n" +
		"0 map[]\n" +
		"0 0 map[]\n" +
		"(0, False) [] []\n" +
		"assignment to entry in nil map\n"
	if got := string(out); got != want {
		t.Errorf("map helper output = %q, want %q", got, want)
	}
}
