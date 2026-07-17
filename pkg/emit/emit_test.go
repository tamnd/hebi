package emit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tamnd/hebi/pkg/ir"
	"github.com/tamnd/hebi/pkg/shim"
)

// hello is the well-formed module: func main() { x := 1 + 2; if x < 3 { println(x) } }.
func hello() *ir.Module {
	return &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{
				&ir.AssignStmt{Name: "x", Define: true, Value: &ir.BinaryExpr{Op: "+", X: &ir.IntLit{Text: "1"}, Y: &ir.IntLit{Text: "2"}}},
				&ir.IfStmt{
					Cond: &ir.BinaryExpr{Op: "<", X: &ir.Ident{Name: "x"}, Y: &ir.IntLit{Text: "3"}},
					Then: []ir.Stmt{&ir.ExprStmt{X: &ir.Intrinsic{Name: "println", Args: []ir.Expr{&ir.Ident{Name: "x"}}}}},
				},
			},
		}},
	}
}

func TestModuleHello(t *testing.T) {
	t.Parallel()
	want := `import _hebirt


def main():
    x = (1 + 2)
    if (x < 3):
        _hebirt.println(x)


if __name__ == "__main__":
    main()
`
	got, err := Module(hello())
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestModuleNoShim covers a module that never touches an intrinsic: it must not
// import the runtime, empty bodies must become pass, and top-level defs must be
// separated by two blank lines.
func TestModuleNoShim(t *testing.T) {
	t.Parallel()
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{
			{Name: "greet", Body: nil},
			{Name: "main", Body: []ir.Stmt{&ir.ExprStmt{X: &ir.CallExpr{Name: "greet"}}}},
		},
	}
	want := `def greet():
    pass


def main():
    greet()


if __name__ == "__main__":
    main()
`
	got, err := Module(m)
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestModuleMaskAndUnary(t *testing.T) {
	t.Parallel()
	// func main() { fmt.Println(_u8(-(a))) } exercises both new nodes and the
	// shim routing for the mask helper.
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{&ir.ExprStmt{X: &ir.Intrinsic{Name: "println", Args: []ir.Expr{
				&ir.Mask{Bits: 8, Signed: false, X: &ir.UnaryExpr{Op: "-", X: &ir.Ident{Name: "a"}}},
			}}}},
		}},
	}
	want := `import _hebirt


def main():
    _hebirt.println(_hebirt._u8((-a)))


if __name__ == "__main__":
    main()
`
	got, err := Module(m)
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestModuleShifts(t *testing.T) {
	t.Parallel()
	// A masked left shift and a bare right shift, the two spellings the
	// lowering produces: the left shift grows and carries a mask, the right
	// shift needs none.
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{
				&ir.AssignStmt{Name: "a", Value: &ir.Mask{Bits: 8, Signed: false, X: &ir.BinaryExpr{Op: "<<", X: &ir.Ident{Name: "x"}, Y: &ir.IntLit{Text: "1"}}}},
				&ir.AssignStmt{Name: "b", Value: &ir.BinaryExpr{Op: ">>", X: &ir.Ident{Name: "y"}, Y: &ir.IntLit{Text: "1"}}},
			},
		}},
	}
	want := `import _hebirt


def main():
    a = _hebirt._u8((x << 1))
    b = (y >> 1)


if __name__ == "__main__":
    main()
`
	got, err := Module(m)
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestModuleFloatAndConvert(t *testing.T) {
	t.Parallel()
	// A float64 literal passes through, a float32 result renarrows through the
	// helper, and an int conversion of a float truncates before the width mask.
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{
				&ir.AssignStmt{Name: "a", Value: &ir.FloatLit{Text: "3.14"}},
				&ir.AssignStmt{Name: "b", Value: &ir.Intrinsic{Name: "_f32", Args: []ir.Expr{&ir.FloatLit{Text: "0.1"}}}},
				&ir.AssignStmt{Name: "c", Value: &ir.Mask{Bits: 8, Signed: false, X: &ir.Convert{To: "int", X: &ir.Ident{Name: "a"}}}},
				&ir.ExprStmt{X: &ir.Intrinsic{Name: "println", Args: []ir.Expr{&ir.Convert{To: "float", X: &ir.Ident{Name: "c"}}}}},
			},
		}},
	}
	want := `import _hebirt


def main():
    a = 3.14
    b = _hebirt._f32(0.1)
    c = _hebirt._u8(int(a))
    _hebirt.println(float(c))


if __name__ == "__main__":
    main()
`
	got, err := Module(m)
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestModuleNot pins the logical not spelling: the keyword operator takes a
// space before its operand where the symbol operators do not, and the operand
// keeps its own parentheses.
func TestModuleNot(t *testing.T) {
	t.Parallel()
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{
				&ir.AssignStmt{Name: "a", Value: &ir.UnaryExpr{Op: "!", X: &ir.Ident{Name: "ok"}}},
				&ir.AssignStmt{Name: "b", Value: &ir.UnaryExpr{Op: "!", X: &ir.BinaryExpr{Op: "<", X: &ir.Ident{Name: "x"}, Y: &ir.Ident{Name: "y"}}}},
			},
		}},
	}
	want := `def main():
    a = (not ok)
    b = (not (x < y))


if __name__ == "__main__":
    main()
`
	got, err := Module(m)
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestModuleSwitchChain pins the shape a switch lowers to: the tag spilled to a
// temporary, an elif ladder rather than nested else blocks, and a multi-value
// case rendered as an or chain of equality tests.
func TestModuleSwitchChain(t *testing.T) {
	t.Parallel()
	tag := &ir.Ident{Name: "_tag"}
	eq := func(text string) ir.Expr {
		return &ir.BinaryExpr{Op: "==", X: tag, Y: &ir.IntLit{Text: text}}
	}
	say := func(word string) ir.Stmt {
		return &ir.ExprStmt{X: &ir.Intrinsic{Name: "println", Args: []ir.Expr{&ir.StringLit{Value: word}}}}
	}
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{
				&ir.AssignStmt{Name: "_tag", Define: true, Value: &ir.Ident{Name: "x"}},
				&ir.IfStmt{
					Cond: eq("1"),
					Then: []ir.Stmt{say("one")},
					Else: []ir.Stmt{&ir.IfStmt{
						Cond: &ir.BinaryExpr{Op: "||", X: eq("2"), Y: eq("3")},
						Then: []ir.Stmt{say("small")},
						Else: []ir.Stmt{say("big")},
					}},
				},
			},
		}},
	}
	want := `import _hebirt


def main():
    _tag = x
    if (_tag == 1):
        _hebirt.println(b"one")
    elif ((_tag == 2) or (_tag == 3)):
        _hebirt.println(b"small")
    else:
        _hebirt.println(b"big")


if __name__ == "__main__":
    main()
`
	got, err := Module(m)
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMaskHelper(t *testing.T) {
	t.Parallel()
	tests := []struct {
		bits   int
		signed bool
		want   string
	}{
		{8, false, "_u8"},
		{16, true, "_i16"},
		{32, false, "_u32"},
		{64, true, "_i64"},
	}
	for _, tt := range tests {
		if got := maskHelper(tt.bits, tt.signed); got != tt.want {
			t.Errorf("maskHelper(%d, %v) = %q, want %q", tt.bits, tt.signed, got, tt.want)
		}
	}
}

func TestModuleDeterministic(t *testing.T) {
	t.Parallel()
	first, err := Module(hello())
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	for range 20 {
		got, err := Module(hello())
		if err != nil {
			t.Fatalf("Module: %v", err)
		}
		if got != first {
			t.Fatal("emit output drifted across runs")
		}
	}
}

func TestModuleRejectsUnsupportedOperator(t *testing.T) {
	t.Parallel()
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{&ir.AssignStmt{Name: "x", Value: &ir.BinaryExpr{Op: "/", X: &ir.IntLit{Text: "6"}, Y: &ir.IntLit{Text: "2"}}}},
		}},
	}
	if _, err := Module(m); err == nil {
		t.Fatal("Module accepted an unsupported operator")
	}
}

func TestPyBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"", `b""`},
		{"hi", `b"hi"`},
		{`a"b`, `b"a\"b"`},
		{"a\nb", `b"a\nb"`},
		{"a\tb", `b"a\tb"`},
		{`c:\x`, `b"c:\\x"`},
		{"\x00", `b"\x00"`},
		{"héllo", `b"h\xc3\xa9llo"`},
	}
	for _, tt := range tests {
		if got := pyBytes(tt.in); got != tt.want {
			t.Errorf("pyBytes(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

// TestModuleStringAndIndex pins the string surface: a string literal is a bytes
// literal, an index into it reads a byte, and the length is len.
func TestModuleStringAndIndex(t *testing.T) {
	t.Parallel()
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{
				&ir.AssignStmt{Name: "s", Define: true, Value: &ir.StringLit{Value: "hi"}},
				&ir.AssignStmt{Name: "c", Define: true, Value: &ir.IndexExpr{X: &ir.Ident{Name: "s"}, Index: &ir.IntLit{Text: "0"}}},
				&ir.ExprStmt{X: &ir.Intrinsic{Name: "println", Args: []ir.Expr{&ir.CallExpr{Name: "len", Args: []ir.Expr{&ir.Ident{Name: "s"}}}}}},
			},
		}},
	}
	want := `import _hebirt


def main():
    s = b"hi"
    c = s[0]
    _hebirt.println(len(s))


if __name__ == "__main__":
    main()
`
	got, err := Module(m)
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestModuleRangeString pins the while form a range over a string lowers to: the
// cursor starts at zero, each step decodes the rune at the cursor and exposes
// the byte index and rune, and the cursor advances by the rune's byte width.
func TestModuleRangeString(t *testing.T) {
	t.Parallel()
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{&ir.RangeString{
				Key:    "i",
				Value:  "r",
				Cursor: "_i",
				Width:  "_w",
				Source: &ir.Ident{Name: "s"},
				Body: []ir.Stmt{&ir.ExprStmt{X: &ir.Intrinsic{Name: "println", Args: []ir.Expr{
					&ir.Ident{Name: "i"}, &ir.Ident{Name: "r"},
				}}}},
			}},
		}},
	}
	want := `import _hebirt


def main():
    _i = 0
    while _i < len(s):
        r, _w = _hebirt._decode_rune(s, _i)
        i = _i
        _hebirt.println(i, r)
        _i = _i + _w


if __name__ == "__main__":
    main()
`
	got, err := Module(m)
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestModuleRangeStringBlankValue covers the rune target dropping to the
// throwaway name and the index assignment vanishing when the clause is blank.
func TestModuleRangeStringBlankValue(t *testing.T) {
	t.Parallel()
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{&ir.RangeString{
				Value:  "",
				Cursor: "_i",
				Width:  "_w",
				Source: &ir.Ident{Name: "s"},
				Body:   nil,
			}},
		}},
	}
	want := `import _hebirt


def main():
    _i = 0
    while _i < len(s):
        _, _w = _hebirt._decode_rune(s, _i)
        _i = _i + _w


if __name__ == "__main__":
    main()
`
	got, err := Module(m)
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestEmittedRuns is the small end-to-end check: emit hello, drop it beside the
// shim, run it under CPython, and confirm it prints the Go answer. It skips
// where python3 is not on the path.
func TestEmittedRuns(t *testing.T) {
	t.Parallel()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	// A module that always prints, so the check does not depend on the branch
	// in hello (where 3 < 3 is false and nothing is emitted).
	m := &ir.Module{
		Package: "main",
		Funcs: []*ir.Func{{
			Name: "main",
			Body: []ir.Stmt{&ir.ExprStmt{X: &ir.Intrinsic{Name: "println", Args: []ir.Expr{
				&ir.BinaryExpr{Op: "+", X: &ir.IntLit{Text: "1"}, Y: &ir.IntLit{Text: "2"}},
			}}}},
		}},
	}
	src, err := Module(m)
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, shim.Name+".py"), []byte(shim.Source()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), py, "main.py")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run emitted module: %v\n%s", err, out)
	}
	if got, want := string(out), "3\n"; got != want {
		t.Errorf("emitted output = %q, want %q", got, want)
	}
}
