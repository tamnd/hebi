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

// TestVerifyAcceptsFuncSurface checks a function with parameters and a value
// return, plus a bare return, passes structural verification.
func TestVerifyAcceptsFuncSurface(t *testing.T) {
	t.Parallel()
	m := &Module{
		Package: "main",
		Funcs: []*Func{{
			Name:   "add",
			Params: []string{"a", "b"},
			Body: []Stmt{
				&ReturnStmt{Value: &BinaryExpr{Op: "+", X: &Ident{Name: "a"}, Y: &Ident{Name: "b"}}},
			},
		}, {
			Name: "noop",
			Body: []Stmt{&ReturnStmt{}},
		}},
	}
	if err := Verify(m); err != nil {
		t.Fatalf("Verify rejected a well-formed function surface: %v", err)
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
		{"leaked labeled break", func(m *Module) {
			m.Funcs[0].Body = append(m.Funcs[0].Body, &LabeledBreak{Label: "Outer"})
		}, "unresolved labeled break to \"Outer\""},
		{"leaked labeled continue", func(m *Module) {
			m.Funcs[0].Body = append(m.Funcs[0].Body, &LabeledContinue{Label: "Outer"})
		}, "unresolved labeled continue to \"Outer\""},
		{"empty param name", func(m *Module) { m.Funcs[0].Params = []string{""} }, "parameter 0 has no name"},
		{"bad return value", func(m *Module) {
			m.Funcs[0].Body = append(m.Funcs[0].Body, &ReturnStmt{Value: &BinaryExpr{Op: "", X: &IntLit{Text: "1"}, Y: &IntLit{Text: "2"}}})
		}, "no operator"},
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

// TestVerifyStructSurface accepts a well-formed struct definition alongside the
// nodes that use it, the field access, the constructor call, the clone, and the
// field assignment, and rejects a struct with no name, a duplicate struct, a
// scalar field with a nil zero, a value-struct field with no type, a keyed
// literal field with no name, and the nil operands of the new nodes.
func TestVerifyStructSurface(t *testing.T) {
	t.Parallel()
	good := &Module{
		Package: "main",
		Structs: []*StructDef{
			{Name: "Inner", Fields: []StructField{{Name: "N", Kind: FieldScalar, Zero: &IntLit{Text: "0"}}}},
			{Name: "Outer", Fields: []StructField{
				{Name: "V", Kind: FieldStruct, Struct: "Inner"},
				{Name: "K", Kind: FieldScalar, Zero: &IntLit{Text: "0"}},
			}},
		},
		Funcs: []*Func{{Name: "main", Body: []Stmt{
			&AssignStmt{Name: "a", Define: true, Value: &StructLit{Type: "Outer", Keyed: true, Fields: []StructArg{
				{Name: "K", Value: &IntLit{Text: "2"}},
			}}},
			&AssignStmt{Name: "b", Define: true, Value: &Clone{X: &Ident{Name: "a"}}},
			&SetField{Object: &Ident{Name: "b"}, Name: "K", Value: &IntLit{Text: "9"}},
			&ExprStmt{X: &FieldAccess{X: &Ident{Name: "a"}, Name: "K"}},
		}}},
	}
	if err := Verify(good); err != nil {
		t.Fatalf("Verify rejected a well-formed struct surface: %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(*Module)
		wantSub string
	}{
		{"nil struct", func(m *Module) { m.Structs[0] = nil }, "struct 0 is nil"},
		{"struct no name", func(m *Module) { m.Structs[0].Name = "" }, "struct 0 has no name"},
		{"duplicate struct", func(m *Module) { m.Structs[1].Name = "Inner" }, "defined more than once"},
		{"field no name", func(m *Module) { m.Structs[0].Fields[0].Name = "" }, "field 0 has no name"},
		{"scalar nil zero", func(m *Module) { m.Structs[0].Fields[0].Zero = nil }, "nil expression"},
		{"struct field no type", func(m *Module) { m.Structs[1].Fields[0].Struct = "" }, "struct field with no type"},
		{"keyed field no name", func(m *Module) {
			m.Funcs[0].Body[0].(*AssignStmt).Value.(*StructLit).Fields[0].Name = ""
		}, "keyed field 0 has no name"},
		{"struct lit no type", func(m *Module) {
			m.Funcs[0].Body[0].(*AssignStmt).Value.(*StructLit).Type = ""
		}, "struct with no type"},
		{"nil clone operand", func(m *Module) { m.Funcs[0].Body[1].(*AssignStmt).Value.(*Clone).X = nil }, "nil expression"},
		{"set field no name", func(m *Module) { m.Funcs[0].Body[2].(*SetField).Name = "" }, "empty field name"},
		{"nil set field object", func(m *Module) { m.Funcs[0].Body[2].(*SetField).Object = nil }, "nil expression"},
		{"field access no name", func(m *Module) {
			m.Funcs[0].Body[3].(*ExprStmt).X.(*FieldAccess).Name = ""
		}, "reads a field with no name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Rebuild from scratch so parallel subtests do not share mutations.
			m := &Module{
				Package: "main",
				Structs: []*StructDef{
					{Name: "Inner", Fields: []StructField{{Name: "N", Kind: FieldScalar, Zero: &IntLit{Text: "0"}}}},
					{Name: "Outer", Fields: []StructField{
						{Name: "V", Kind: FieldStruct, Struct: "Inner"},
						{Name: "K", Kind: FieldScalar, Zero: &IntLit{Text: "0"}},
					}},
				},
				Funcs: []*Func{{Name: "main", Body: []Stmt{
					&AssignStmt{Name: "a", Define: true, Value: &StructLit{Type: "Outer", Keyed: true, Fields: []StructArg{
						{Name: "K", Value: &IntLit{Text: "2"}},
					}}},
					&AssignStmt{Name: "b", Define: true, Value: &Clone{X: &Ident{Name: "a"}}},
					&SetField{Object: &Ident{Name: "b"}, Name: "K", Value: &IntLit{Text: "9"}},
					&ExprStmt{X: &FieldAccess{X: &Ident{Name: "a"}, Name: "K"}},
				}}},
			}
			tt.mutate(m)
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

// TestVerifyArraySurface accepts a well-formed array definition alongside the
// nodes that use it, the array zero, the array literal, the array clone, the
// index write, and an array-typed struct field, and rejects an array zero with a
// negative length or a nil element, an array literal with a nil element, an
// array clone with a nil operand, an index write with a nil operand, and an
// array-typed struct field with a nil zero.
func TestVerifyArraySurface(t *testing.T) {
	t.Parallel()
	build := func() *Module {
		return &Module{
			Package: "main",
			Structs: []*StructDef{
				{Name: "Grid", Fields: []StructField{
					{Name: "Cells", Kind: FieldArray, Zero: &ArrayZero{Len: 3, Elem: &IntLit{Text: "0"}}},
				}},
			},
			Funcs: []*Func{{Name: "main", Body: []Stmt{
				&AssignStmt{Name: "a", Define: true, Value: &ArrayLit{Elems: []Expr{&IntLit{Text: "1"}, &IntLit{Text: "0"}}}},
				&AssignStmt{Name: "b", Define: true, Value: &ArrayClone{X: &Ident{Name: "a"}}},
				&SetIndex{Object: &Ident{Name: "b"}, Index: &IntLit{Text: "0"}, Value: &IntLit{Text: "9"}},
				&AssignStmt{Name: "z", Define: true, Value: &ArrayZero{Len: 2, Elem: &IntLit{Text: "0"}}},
			}}},
		}
	}
	if err := Verify(build()); err != nil {
		t.Fatalf("Verify rejected a well-formed array surface: %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(*Module)
		wantSub string
	}{
		{"array field nil zero", func(m *Module) { m.Structs[0].Fields[0].Zero = nil }, "nil expression"},
		{"array zero negative length", func(m *Module) {
			m.Funcs[0].Body[3].(*AssignStmt).Value.(*ArrayZero).Len = -1
		}, "negative length"},
		{"array zero nil element", func(m *Module) {
			m.Funcs[0].Body[3].(*AssignStmt).Value.(*ArrayZero).Elem = nil
		}, "nil expression"},
		{"array lit nil element", func(m *Module) {
			m.Funcs[0].Body[0].(*AssignStmt).Value.(*ArrayLit).Elems[0] = nil
		}, "nil expression"},
		{"array clone nil operand", func(m *Module) {
			m.Funcs[0].Body[1].(*AssignStmt).Value.(*ArrayClone).X = nil
		}, "nil expression"},
		{"set index nil object", func(m *Module) { m.Funcs[0].Body[2].(*SetIndex).Object = nil }, "nil expression"},
		{"set index nil index", func(m *Module) { m.Funcs[0].Body[2].(*SetIndex).Index = nil }, "nil expression"},
		{"set index nil value", func(m *Module) { m.Funcs[0].Body[2].(*SetIndex).Value = nil }, "nil expression"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := build()
			tt.mutate(m)
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

// TestVerifySliceSurface accepts a well-formed slice surface, a slice literal, a
// make, a two-index slice expression, an index write, a cap read off the header,
// a slice-typed struct field defaulting to the nil sentinel, and the nil sentinel
// itself, then rejects each node with a nil operand: a slice-literal element, a
// make length, capacity, or element zero, a slice expression's operand, and a
// malformed high or max bound on a slice expression.
func TestVerifySliceSurface(t *testing.T) {
	t.Parallel()
	build := func() *Module {
		return &Module{
			Package: "main",
			Structs: []*StructDef{
				{Name: "Bag", Fields: []StructField{
					{Name: "Items", Kind: FieldScalar, Zero: &NilSlice{}},
				}},
			},
			Funcs: []*Func{{Name: "main", Body: []Stmt{
				&AssignStmt{Name: "s", Define: true, Value: &SliceLit{Elems: []Expr{&IntLit{Text: "1"}, &IntLit{Text: "2"}}}},
				&AssignStmt{Name: "m", Define: true, Value: &SliceMake{Len: &IntLit{Text: "2"}, Cap: &IntLit{Text: "5"}, Elem: &IntLit{Text: "0"}}},
				&AssignStmt{Name: "b", Define: true, Value: &SliceExpr{X: &Ident{Name: "s"}, High: &IntLit{Text: "1"}}},
				&SetIndex{Object: &Ident{Name: "b"}, Index: &IntLit{Text: "0"}, Value: &IntLit{Text: "9"}},
				&AssignStmt{Name: "c", Define: true, Value: &FieldAccess{X: &Ident{Name: "m"}, Name: "cap"}},
				&AssignStmt{Name: "z", Define: true, Value: &NilSlice{}},
			}}},
		}
	}
	if err := Verify(build()); err != nil {
		t.Fatalf("Verify rejected a well-formed slice surface: %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(*Module)
		wantSub string
	}{
		{"slice lit nil element", func(m *Module) {
			m.Funcs[0].Body[0].(*AssignStmt).Value.(*SliceLit).Elems[0] = nil
		}, "nil expression"},
		{"make nil length", func(m *Module) {
			m.Funcs[0].Body[1].(*AssignStmt).Value.(*SliceMake).Len = nil
		}, "nil expression"},
		{"make nil capacity", func(m *Module) {
			m.Funcs[0].Body[1].(*AssignStmt).Value.(*SliceMake).Cap = nil
		}, "nil expression"},
		{"make nil element zero", func(m *Module) {
			m.Funcs[0].Body[1].(*AssignStmt).Value.(*SliceMake).Elem = nil
		}, "nil expression"},
		{"slice expr nil operand", func(m *Module) {
			m.Funcs[0].Body[2].(*AssignStmt).Value.(*SliceExpr).X = nil
		}, "nil expression"},
		{"slice expr malformed high bound", func(m *Module) {
			m.Funcs[0].Body[2].(*AssignStmt).Value.(*SliceExpr).High = &IntLit{}
		}, "no text"},
		{"slice expr malformed max bound", func(m *Module) {
			m.Funcs[0].Body[2].(*AssignStmt).Value.(*SliceExpr).Max = &IntLit{}
		}, "no text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := build()
			tt.mutate(m)
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

// TestVerifyForRange accepts a well-formed for-in-range loop with each of its
// optional bounds present or absent, and rejects one without a stop bound and
// one whose bounds are nil expressions.
func TestVerifyForRange(t *testing.T) {
	t.Parallel()
	good := &Module{Package: "main", Funcs: []*Func{{Name: "main", Body: []Stmt{
		&ForRange{Var: "i", Stop: &Ident{Name: "n"}, Body: []Stmt{&Break{}, &Continue{}}},
		&ForRange{Var: "j", Start: &IntLit{Text: "1"}, Stop: &IntLit{Text: "9"}, Step: &IntLit{Text: "2"}, Body: nil},
	}}}}
	if err := Verify(good); err != nil {
		t.Fatalf("Verify rejected a well-formed for-in-range: %v", err)
	}
	tests := []struct {
		name    string
		stmt    Stmt
		wantSub string
	}{
		{"no stop", &ForRange{Var: "i", Stop: nil}, "without a stop bound"},
		{"nil start", &ForRange{Var: "i", Start: nil, Stop: &IntLit{Text: "1"}, Step: &Ident{Name: ""}}, "no name"},
		{"nil stop expression", &ForRange{Var: "i", Stop: &BinaryExpr{Op: "+", X: nil, Y: &IntLit{Text: "1"}}}, "nil expression"},
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
