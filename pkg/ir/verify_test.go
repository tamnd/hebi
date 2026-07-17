package ir

import (
	"strings"
	"testing"
)

// hello is a well-formed module: func main() { x := 1 + 2; if x < 3 { println(x) } }
func hello() *Module {
	return &Module{
		Package: "main",
		Funcs: []*Func{{
			Name: "main",
			Body: []Stmt{
				&AssignStmt{Name: "x", Define: true, Value: &BinaryExpr{Op: "+", X: &IntLit{Text: "1"}, Y: &IntLit{Text: "2"}}},
				&IfStmt{
					Cond: &BinaryExpr{Op: "<", X: &Ident{Name: "x"}, Y: &IntLit{Text: "3"}},
					Then: []Stmt{&ExprStmt{X: &Intrinsic{Name: "println", Args: []Expr{&Ident{Name: "x"}}}}},
				},
			},
		}},
	}
}

func TestVerifyAcceptsWellFormed(t *testing.T) {
	t.Parallel()
	if err := Verify(hello()); err != nil {
		t.Fatalf("Verify rejected a well-formed module: %v", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*Module)
		wantSub string
	}{
		{"nil module", nil, "module is nil"},
		{"no package", func(m *Module) { m.Package = "" }, "no package name"},
		{"nil func", func(m *Module) { m.Funcs[0] = nil }, "func 0 is nil"},
		{"empty func name", func(m *Module) { m.Funcs[0].Name = "" }, "has no name"},
		{"duplicate func", func(m *Module) { m.Funcs = append(m.Funcs, &Func{Name: "main"}) }, "defined more than once"},
		{"nil statement", func(m *Module) { m.Funcs[0].Body[0] = nil }, "statement 0 is nil"},
		{"empty assign name", func(m *Module) { m.Funcs[0].Body[0].(*AssignStmt).Name = "" }, "empty name"},
		{"nil assign value", func(m *Module) { m.Funcs[0].Body[0].(*AssignStmt).Value = nil }, "nil expression"},
		{"nil if cond", func(m *Module) { m.Funcs[0].Body[1].(*IfStmt).Cond = nil }, "if condition is a nil expression"},
		{"empty binary op", func(m *Module) { m.Funcs[0].Body[0].(*AssignStmt).Value.(*BinaryExpr).Op = "" }, "no operator"},
		{"empty int text", func(m *Module) {
			m.Funcs[0].Body[0].(*AssignStmt).Value.(*BinaryExpr).X.(*IntLit).Text = ""
		}, "no text"},
		{"empty intrinsic name", func(m *Module) {
			m.Funcs[0].Body[1].(*IfStmt).Then[0].(*ExprStmt).X.(*Intrinsic).Name = ""
		}, "intrinsic with no name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var m *Module
			if tt.mutate != nil {
				m = hello()
				tt.mutate(m)
			}
			err := Verify(m)
			if err == nil {
				t.Fatal("Verify accepted a malformed module")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantSub)
			}
		})
	}
}

func TestVerifyUnaryAndMask(t *testing.T) {
	t.Parallel()
	good := &Module{Package: "main", Funcs: []*Func{{Name: "main", Body: []Stmt{
		&AssignStmt{Name: "x", Define: true, Value: &Mask{Bits: 8, Signed: true, X: &UnaryExpr{Op: "-", X: &Ident{Name: "y"}}}},
	}}}}
	if err := Verify(good); err != nil {
		t.Fatalf("Verify rejected a well-formed unary and mask: %v", err)
	}
	tests := []struct {
		name    string
		value   Expr
		wantSub string
	}{
		{"empty unary op", &UnaryExpr{Op: "", X: &Ident{Name: "y"}}, "unary expression with no operator"},
		{"nil unary operand", &UnaryExpr{Op: "-", X: nil}, "nil expression"},
		{"bad mask width", &Mask{Bits: 7, Signed: false, X: &Ident{Name: "y"}}, "invalid width 7"},
		{"nil mask operand", &Mask{Bits: 8, Signed: false, X: nil}, "nil expression"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &Module{Package: "main", Funcs: []*Func{{Name: "main", Body: []Stmt{
				&AssignStmt{Name: "x", Define: true, Value: tt.value},
			}}}}
			err := Verify(m)
			if err == nil {
				t.Fatal("Verify accepted a malformed module")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantSub)
			}
		})
	}
}

func TestVerifyFloatAndConvert(t *testing.T) {
	t.Parallel()
	good := &Module{Package: "main", Funcs: []*Func{{Name: "main", Body: []Stmt{
		&AssignStmt{Name: "x", Define: true, Value: &Convert{To: "int", X: &FloatLit{Text: "3.9"}}},
	}}}}
	if err := Verify(good); err != nil {
		t.Fatalf("Verify rejected a well-formed float and convert: %v", err)
	}
	tests := []struct {
		name    string
		value   Expr
		wantSub string
	}{
		{"empty float text", &FloatLit{Text: ""}, "float literal with no text"},
		{"unknown convert builtin", &Convert{To: "str", X: &Ident{Name: "y"}}, "unknown builtin"},
		{"nil convert operand", &Convert{To: "int", X: nil}, "nil expression"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &Module{Package: "main", Funcs: []*Func{{Name: "main", Body: []Stmt{
				&AssignStmt{Name: "x", Define: true, Value: tt.value},
			}}}}
			err := Verify(m)
			if err == nil {
				t.Fatal("Verify accepted a malformed module")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantSub)
			}
		})
	}
}

func TestVerifyStringSurface(t *testing.T) {
	t.Parallel()
	good := &Module{Package: "main", Funcs: []*Func{{Name: "main", Body: []Stmt{
		&RangeString{Value: "r", Cursor: "_i", Width: "_w", Source: &Ident{Name: "s"}, Body: []Stmt{
			&AssignStmt{Name: "c", Define: true, Value: &IndexExpr{X: &Ident{Name: "s"}, Index: &IntLit{Text: "0"}}},
		}},
	}}}}
	if err := Verify(good); err != nil {
		t.Fatalf("Verify rejected a well-formed string surface: %v", err)
	}
	tests := []struct {
		name    string
		stmt    Stmt
		wantSub string
	}{
		{"range without cursor", &RangeString{Cursor: "", Width: "_w", Source: &Ident{Name: "s"}}, "without a cursor or width name"},
		{"range without width", &RangeString{Cursor: "_i", Width: "", Source: &Ident{Name: "s"}}, "without a cursor or width name"},
		{"range nil source", &RangeString{Cursor: "_i", Width: "_w", Source: nil}, "nil expression"},
		{"nil indexed operand", &AssignStmt{Name: "c", Value: &IndexExpr{X: nil, Index: &IntLit{Text: "0"}}}, "nil expression"},
		{"nil index", &AssignStmt{Name: "c", Value: &IndexExpr{X: &Ident{Name: "s"}, Index: nil}}, "nil expression"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &Module{Package: "main", Funcs: []*Func{{Name: "main", Body: []Stmt{tt.stmt}}}}
			err := Verify(m)
			if err == nil {
				t.Fatal("Verify accepted a malformed module")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantSub)
			}
		})
	}
}

// TestVerifyDeterministic checks the reported error does not depend on run
// order: the same malformed tree gives the same message every time.
func TestVerifyDeterministic(t *testing.T) {
	t.Parallel()
	m := hello()
	m.Funcs[0].Body[1].(*IfStmt).Cond = nil
	first := Verify(m).Error()
	for range 20 {
		if got := Verify(m).Error(); got != first {
			t.Fatalf("Verify message drifted: %q vs %q", got, first)
		}
	}
}
