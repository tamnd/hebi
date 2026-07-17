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

func TestPyString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"", `""`},
		{"hi", `"hi"`},
		{`a"b`, `"a\"b"`},
		{"a\nb", `"a\nb"`},
		{"a\tb", `"a\tb"`},
		{`c:\x`, `"c:\\x"`},
		{"\x00", `"\x00"`},
	}
	for _, tt := range tests {
		if got := pyString(tt.in); got != tt.want {
			t.Errorf("pyString(%q) = %s, want %s", tt.in, got, tt.want)
		}
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
