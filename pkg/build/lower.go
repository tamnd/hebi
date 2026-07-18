package build

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strconv"
	"strings"

	"github.com/tamnd/hebi/pkg/frontend"
	"github.com/tamnd/hebi/pkg/ir"
)

// lower walks a loaded, type-checked package and builds the IR module the
// emitter consumes. It covers the supported subset and returns a positioned
// error for any surface it does not yet handle, so an unsupported construct
// fails loudly rather than emitting something wrong.
func lower(pkg *frontend.Package) (*ir.Module, error) {
	l := &lowerer{pkg: pkg, structs: map[string]*ir.StructDef{}}
	m := &ir.Module{Package: pkg.Name}
	// Collect struct type declarations first so the emitter has every class
	// before the functions that construct or read one, and so a value-struct
	// field can name a sibling struct regardless of source order.
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				sd, err := l.lowerTypeSpec(spec.(*ast.TypeSpec))
				if err != nil {
					return nil, err
				}
				if sd != nil {
					m.Structs = append(m.Structs, sd)
					l.structs[sd.Name] = sd
				}
			}
		}
	}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				// Imports and package-level declarations carry no function body
				// to lower here; the type checker already read them.
				continue
			}
			fn, err := l.lowerFunc(fd)
			if err != nil {
				return nil, err
			}
			m.Funcs = append(m.Funcs, fn)
		}
	}
	return m, nil
}

// lowerTypeSpec lowers a single type declaration. A struct type becomes an IR
// StructDef, the class the emitter generates. A defined non-struct type, such as
// type Celsius float64, needs no class because its values already lower through
// their underlying type, so it returns nil. Embedded fields and reference-typed
// fields wait on later slices and fail loudly here.
func (l *lowerer) lowerTypeSpec(ts *ast.TypeSpec) (*ir.StructDef, error) {
	obj, ok := l.pkg.Info.Defs[ts.Name].(*types.TypeName)
	if !ok {
		return nil, l.errf(ts.Pos(), "type %s is not supported yet", ts.Name.Name)
	}
	st, ok := obj.Type().Underlying().(*types.Struct)
	if !ok {
		return nil, nil
	}
	sd := &ir.StructDef{Name: ts.Name.Name, Comparable: types.Comparable(obj.Type())}
	for i := range st.NumFields() {
		fv := st.Field(i)
		// An embedded field is a field whose name is its type's name, so it lowers
		// to a slot named for the embedded type; its members are promoted at the
		// selector by go/types, resolved into an explicit access path there. A
		// value-struct embed is a value field like any other, deep-copied in copy.
		field, err := l.structField(fv.Name(), fv.Type(), ts.Pos())
		if err != nil {
			return nil, err
		}
		sd.Fields = append(sd.Fields, field)
	}
	return sd, nil
}

// structField classifies one struct field by its type. A value-struct field
// carries its type name, so the emitter can build its zero instance and recurse
// in copy; a scalar field carries its zero literal. A field of any other type,
// a pointer, slice, map, or the like, waits on a later slice and fails loudly
// through zeroValueOfType.
func (l *lowerer) structField(name string, t types.Type, pos token.Pos) (ir.StructField, error) {
	if _, ok := t.Underlying().(*types.Struct); ok {
		named, ok := t.(*types.Named)
		if !ok {
			return ir.StructField{}, l.errf(pos, "anonymous struct field %s is not supported yet", name)
		}
		return ir.StructField{Name: name, Kind: ir.FieldStruct, Struct: named.Obj().Name()}, nil
	}
	if arr, ok := t.Underlying().(*types.Array); ok {
		// An array field is a value like a struct field, so it carries its fresh
		// zero array and copies element-wise; the emitter builds it in the body and
		// clones it in copy, never sharing the list.
		zero, err := l.arrayZero(arr, pos)
		if err != nil {
			return ir.StructField{}, err
		}
		return ir.StructField{Name: name, Kind: ir.FieldArray, Zero: zero}, nil
	}
	zero, err := l.zeroValueOfType(t, pos)
	if err != nil {
		return ir.StructField{}, err
	}
	return ir.StructField{Name: name, Kind: ir.FieldScalar, Zero: zero}, nil
}

type lowerer struct {
	pkg *frontend.Package
	// structs indexes the lowered struct definitions by name, so a keyed composite
	// literal can look up the target struct to translate a field key into the
	// constructor parameter name, which differs for an embedded value struct.
	structs map[string]*ir.StructDef
	// rangeSeq numbers the internal names a range over a string allocates, so a
	// nested or repeated range gets distinct cursor and width names. It counts
	// across the whole module in source order, which keeps the emit deterministic.
	rangeSeq int
	// switchSeq numbers the tag temporary an expression switch spills to, so a
	// nested switch does not reuse an outer switch's tag name.
	switchSeq int
	// scopes is the stack of enclosing loops and switches, innermost last, which
	// tells a break whether it leaves a loop or a switch and lets a labeled break
	// find the loop it names.
	scopes []scope
}

// scope is one enclosing breakable construct: a loop or a switch, carrying the Go
// label the source attached to it, if any. Go's unlabeled break leaves whichever
// is innermost, while a labeled break names an outer loop that the lowering finds
// by its label.
type scope struct {
	kind  scopeKind
	label string
}

// scopeKind marks an enclosing breakable construct as a loop or a switch.
type scopeKind int

const (
	scopeLoop scopeKind = iota
	scopeSwitch
)

func (l *lowerer) pushScope(s scope) { l.scopes = append(l.scopes, s) }
func (l *lowerer) popScope()         { l.scopes = l.scopes[:len(l.scopes)-1] }

// innermostIsSwitch reports whether the closest enclosing breakable construct is
// a switch, in which case an unlabeled break leaves the switch rather than a
// loop.
func (l *lowerer) innermostIsSwitch() bool {
	n := len(l.scopes)
	return n > 0 && l.scopes[n-1].kind == scopeSwitch
}

// labelIsLoop reports whether the given label names an enclosing loop, so a
// labeled break to it can be expressed with the flag machinery.
func (l *lowerer) labelIsLoop(label string) bool {
	for _, s := range l.scopes {
		if s.label == label {
			return s.kind == scopeLoop
		}
	}
	return false
}

// lowerLoopBlock lowers a loop body with a loop scope on the stack, carrying the
// loop's label so a labeled break inside it can resolve to this loop.
func (l *lowerer) lowerLoopBlock(block *ast.BlockStmt, label string) ([]ir.Stmt, error) {
	l.pushScope(scope{kind: scopeLoop, label: label})
	body, err := l.lowerBlock(block)
	l.popScope()
	return body, err
}

// seqName builds an internal name from a base and a sequence number, leaving the
// first one bare for readability and suffixing the rest, so a lone range reads
// as _i and _w while a second range does not collide with it.
func seqName(base string, n int) string {
	if n == 0 {
		return base
	}
	return base + strconv.Itoa(n)
}

// rangeIdent returns the name of a range clause variable, or the empty string
// when the clause is absent or the blank identifier, in which case the lowering
// drops the corresponding assignment.
func rangeIdent(e ast.Expr) string {
	id, ok := e.(*ast.Ident)
	if !ok || id.Name == "_" {
		return ""
	}
	return id.Name
}

func (l *lowerer) errf(pos token.Pos, format string, args ...any) error {
	return fmt.Errorf("%s: "+format, append([]any{l.pkg.Fset.Position(pos)}, args...)...)
}

func (l *lowerer) lowerFunc(fd *ast.FuncDecl) (*ir.Func, error) {
	if fd.Recv != nil {
		return nil, l.errf(fd.Pos(), "methods are not supported yet")
	}
	if fd.Type.TypeParams != nil && len(fd.Type.TypeParams.List) > 0 {
		return nil, l.errf(fd.Pos(), "type parameters are not supported yet")
	}
	params, err := l.lowerParams(fd.Type.Params)
	if err != nil {
		return nil, err
	}
	if err := l.checkResults(fd.Type.Results); err != nil {
		return nil, err
	}
	if fd.Body == nil {
		return nil, l.errf(fd.Pos(), "function %s has no body", fd.Name.Name)
	}
	body, err := l.lowerBlock(fd.Body)
	if err != nil {
		return nil, err
	}
	body = resolveLabeledJumps(body)
	return &ir.Func{Name: fd.Name.Name, Params: params, Body: body}, nil
}

// lowerParams flattens a function's parameter list to the ordered names the
// Python signature binds. A field of several names, func(a, b int), expands to one
// name each, and a blank or unnamed parameter is given a synthetic _pN so the
// signature stays well formed and no two blanks collide; the body never refers to
// such a name, matching Go. A variadic parameter waits on its own slice.
func (l *lowerer) lowerParams(fields *ast.FieldList) ([]string, error) {
	if fields == nil {
		return nil, nil
	}
	var params []string
	for _, field := range fields.List {
		if _, ok := field.Type.(*ast.Ellipsis); ok {
			return nil, l.errf(field.Pos(), "variadic parameters are not supported yet")
		}
		if len(field.Names) == 0 {
			params = append(params, fmt.Sprintf("_p%d", len(params)))
			continue
		}
		for _, name := range field.Names {
			if name.Name == "_" {
				params = append(params, fmt.Sprintf("_p%d", len(params)))
				continue
			}
			params = append(params, name.Name)
		}
	}
	return params, nil
}

// checkResults rejects the return shapes this milestone does not carry. A single
// unnamed result is fine and flows through ReturnStmt; multiple results and named
// results are the readable-tuple and defer-visible-binding work of M3, so they
// fail loudly here rather than lowering to something wrong.
func (l *lowerer) checkResults(fields *ast.FieldList) error {
	if fields == nil {
		return nil
	}
	if len(fields.List) > 1 {
		return l.errf(fields.Pos(), "multiple return values are not supported yet")
	}
	for _, field := range fields.List {
		if len(field.Names) > 0 {
			return l.errf(field.Pos(), "named return values are not supported yet")
		}
	}
	return nil
}

// lowerReturn lowers a return statement. A bare return carries no value. A single
// returned value that reads a struct value out of a copyable location is cloned,
// since the caller receives an independent value, which is the copy-on-return site
// of the value-semantics rule. Multiple returns wait on M3 and are rejected at the
// function signature before a body reaches here.
func (l *lowerer) lowerReturn(s *ast.ReturnStmt) ([]ir.Stmt, error) {
	if len(s.Results) == 0 {
		return []ir.Stmt{&ir.ReturnStmt{}}, nil
	}
	if len(s.Results) > 1 {
		return nil, l.errf(s.Pos(), "multiple return values are not supported yet")
	}
	value, err := l.lowerExpr(s.Results[0])
	if err != nil {
		return nil, err
	}
	value = l.copyIfValueRead(s.Results[0], value)
	return []ir.Stmt{&ir.ReturnStmt{Value: value}}, nil
}

func (l *lowerer) lowerBlock(block *ast.BlockStmt) ([]ir.Stmt, error) {
	var out []ir.Stmt
	for _, s := range block.List {
		lowered, err := l.lowerStmt(s)
		if err != nil {
			return nil, err
		}
		out = append(out, lowered...)
	}
	return out, nil
}

// lowerStmt returns the statements a single Go statement lowers to. Most Go
// statements are one IR statement, but a var declaration of several names is
// several, so the return is a slice.
func (l *lowerer) lowerStmt(s ast.Stmt) ([]ir.Stmt, error) {
	switch s := s.(type) {
	case *ast.DeclStmt:
		return l.lowerDecl(s)
	case *ast.AssignStmt:
		return l.lowerAssign(s)
	case *ast.IncDecStmt:
		return l.lowerIncDec(s)
	case *ast.IfStmt:
		return l.lowerIf(s)
	case *ast.ForStmt:
		return l.lowerFor(s, "")
	case *ast.RangeStmt:
		return l.lowerRange(s, "")
	case *ast.SwitchStmt:
		return l.lowerSwitch(s)
	case *ast.BranchStmt:
		return l.lowerBranch(s)
	case *ast.LabeledStmt:
		return l.lowerLabeled(s)
	case *ast.ExprStmt:
		x, err := l.lowerExpr(s.X)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{&ir.ExprStmt{X: x}}, nil
	case *ast.ReturnStmt:
		return l.lowerReturn(s)
	default:
		return nil, l.errf(s.Pos(), "statement %T is not supported yet", s)
	}
}

func (l *lowerer) lowerDecl(s *ast.DeclStmt) ([]ir.Stmt, error) {
	gen, ok := s.Decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return nil, l.errf(s.Pos(), "declaration is not supported yet")
	}
	var out []ir.Stmt
	for _, spec := range gen.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			return nil, l.errf(spec.Pos(), "declaration spec %T is not supported yet", spec)
		}
		if len(vs.Values) == 0 {
			for _, name := range vs.Names {
				zero, err := l.zeroValue(name)
				if err != nil {
					return nil, err
				}
				out = append(out, &ir.AssignStmt{Name: name.Name, Value: zero, Define: true})
			}
			continue
		}
		if len(vs.Names) != len(vs.Values) {
			return nil, l.errf(vs.Pos(), "multiple assignment is not supported yet")
		}
		for i, name := range vs.Names {
			value, err := l.lowerExpr(vs.Values[i])
			if err != nil {
				return nil, err
			}
			if name.Name != "_" {
				value = l.copyIfValueRead(vs.Values[i], value)
			}
			out = append(out, &ir.AssignStmt{Name: name.Name, Value: value, Define: true})
		}
	}
	return out, nil
}

// zeroValue produces the Go zero value for a declared name's type.
func (l *lowerer) zeroValue(name *ast.Ident) (ir.Expr, error) {
	return l.zeroValueOfType(l.pkg.Info.TypeOf(name), name.Pos())
}

// zeroValueOfType produces the Go zero value of a type. A struct's zero value is
// its constructor called with no arguments, which the class fills with each
// field's own zero. A scalar's zero is its literal, with a float32 renarrowed
// through the single-precision helper. Any other type waits on a later slice and
// fails loudly, which is also how an unsupported struct field type is caught.
func (l *lowerer) zeroValueOfType(t types.Type, pos token.Pos) (ir.Expr, error) {
	if _, ok := t.Underlying().(*types.Struct); ok {
		named, ok := t.(*types.Named)
		if !ok {
			return nil, l.errf(pos, "zero value of an anonymous struct is not supported yet")
		}
		return &ir.StructLit{Type: named.Obj().Name()}, nil
	}
	if arr, ok := t.Underlying().(*types.Array); ok {
		return l.arrayZero(arr, pos)
	}
	if _, ok := t.Underlying().(*types.Slice); ok {
		// A slice is a reference, so its zero value is the nil slice sentinel, which
		// makes a slice-typed struct field a scalar field that shares the sentinel on
		// copy, exactly the shallow-sharing a nil slice value wants.
		return &ir.NilSlice{}, nil
	}
	basic, ok := t.Underlying().(*types.Basic)
	if ok {
		switch info := basic.Info(); {
		case info&types.IsInteger != 0:
			return &ir.IntLit{Text: "0"}, nil
		case info&types.IsFloat != 0:
			if basic.Kind() == types.Float32 {
				return &ir.Intrinsic{Name: "_f32", Args: []ir.Expr{&ir.FloatLit{Text: "0.0"}}}, nil
			}
			return &ir.FloatLit{Text: "0.0"}, nil
		case info&types.IsBoolean != 0:
			return &ir.BoolLit{Value: false}, nil
		case info&types.IsString != 0:
			return &ir.StringLit{Value: ""}, nil
		}
	}
	return nil, l.errf(pos, "zero value of %s is not supported yet", t)
}

// arrayZero builds the zero value of an array type as an ArrayZero, recursing so
// a nested array's element is itself an ArrayZero and an array of structs carries
// a StructLit element. The element's mutability decides which emitted form keeps
// each slot independent, so a scalar array repeats one immutable value while a
// struct or nested-array element is built fresh per position.
func (l *lowerer) arrayZero(arr *types.Array, pos token.Pos) (ir.Expr, error) {
	elem, err := l.zeroValueOfType(arr.Elem(), pos)
	if err != nil {
		return nil, err
	}
	return &ir.ArrayZero{Len: int(arr.Len()), Elem: elem, ElemMutable: isMutableType(arr.Elem())}, nil
}

// isMutableType reports whether a value of the type is mutable in the Python
// representation, so a zero array of it must build each element fresh rather than
// repeat one. A struct is a mutable object and an array is a mutable list; the
// scalar kinds are immutable, so an array of them may repeat one value safely.
func isMutableType(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Struct, *types.Array:
		return true
	default:
		return false
	}
}

// isArrayValue reports whether a type is a Go array, which is a value that copies
// element-wise at every copy site, distinct from a slice, which shares a backing.
func isArrayValue(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Array)
	return ok
}

// isSliceValue reports whether a type is a Go slice, which is a reference to a
// shared backing that aliases rather than copies, so it is indexed and sliced
// through the Slice header and never cloned at a value read.
func isSliceValue(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Slice)
	return ok
}

// compoundOps maps a Go compound-assignment token to the plain binary operator
// and the token narrow reads to decide masking. Only the operators whose binary
// form hebi already lowers are here; division, remainder, and the bitwise family
// wait on their own slices, so an unlisted compound assignment fails loudly.
var compoundOps = map[token.Token]struct {
	op  string
	tok token.Token
}{
	token.ADD_ASSIGN: {"+", token.ADD},
	token.SUB_ASSIGN: {"-", token.SUB},
	token.MUL_ASSIGN: {"*", token.MUL},
	token.SHL_ASSIGN: {"<<", token.SHL},
	token.SHR_ASSIGN: {">>", token.SHR},
}

func (l *lowerer) lowerAssign(s *ast.AssignStmt) ([]ir.Stmt, error) {
	if s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
		return l.lowerCompoundAssign(s)
	}
	if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
		return nil, l.errf(s.Pos(), "multiple assignment is not supported yet")
	}
	value, err := l.lowerExpr(s.Rhs[0])
	if err != nil {
		return nil, err
	}
	// A struct value assigned to a location copies, so the reference-semantic
	// Python instance is cloned where Go would make an independent value. A blank
	// assignment discards the value and creates no independent copy, so it does
	// not clone.
	switch lhs := s.Lhs[0].(type) {
	case *ast.Ident:
		if lhs.Name != "_" {
			value = l.copyIfValueRead(s.Rhs[0], value)
		}
		return []ir.Stmt{&ir.AssignStmt{Name: lhs.Name, Value: value, Define: s.Tok == token.DEFINE}}, nil
	case *ast.SelectorExpr:
		obj, err := l.lowerFieldTarget(lhs)
		if err != nil {
			return nil, err
		}
		value = l.copyIfValueRead(s.Rhs[0], value)
		return []ir.Stmt{&ir.SetField{Object: obj, Name: lhs.Sel.Name, Value: value}}, nil
	case *ast.IndexExpr:
		// An array element assignment writes through the backing list, and a slice
		// element assignment writes through the Slice header's __setitem__, which
		// reaches the shared backing so an aliasing slice sees the write. A map index
		// target waits on its own type.
		xt := l.pkg.Info.TypeOf(lhs.X)
		if !isArrayValue(xt) && !isSliceValue(xt) {
			return nil, l.errf(lhs.Pos(), "index assignment to %s is not supported yet", xt)
		}
		obj, err := l.lowerExpr(lhs.X)
		if err != nil {
			return nil, err
		}
		index, err := l.lowerExpr(lhs.Index)
		if err != nil {
			return nil, err
		}
		value = l.copyIfValueRead(s.Rhs[0], value)
		return []ir.Stmt{&ir.SetIndex{Object: obj, Index: index, Value: value}}, nil
	default:
		return nil, l.errf(s.Lhs[0].Pos(), "assignment target %T is not supported yet", s.Lhs[0])
	}
}

// lowerFieldTarget lowers the object of a field-assignment target, s.field = v,
// after checking the selector really is a field, not a package member or a
// promoted field, which wait on later slices.
func (l *lowerer) lowerFieldTarget(sel *ast.SelectorExpr) (ir.Expr, error) {
	selection, ok := l.pkg.Info.Selections[sel]
	if !ok || selection.Kind() != types.FieldVal {
		return nil, l.errf(sel.Pos(), "assignment target %s is not supported yet", sel.Sel.Name)
	}
	base, err := l.lowerExpr(sel.X)
	if err != nil {
		return nil, err
	}
	// The object of the assignment is the access path down to but not including the
	// final field, which the caller names from the selector. For a direct field the
	// path is empty and the object is the base; for a promoted field it steps
	// through the embedded slots go/types resolved, so u.ID = 5 writes u.Base.ID.
	index := selection.Index()
	return l.fieldChain(base, l.pkg.Info.TypeOf(sel.X), index[:len(index)-1]), nil
}

// lowerCompoundAssign lowers x op= y to x = x op y, reusing the width mask of the
// plain binary operator so a growing compound assignment on a sized integer wraps
// two's-complement the way Go does. The target must be a plain name for now; the
// pointer, field, and index forms arrive with those types.
func (l *lowerer) lowerCompoundAssign(s *ast.AssignStmt) ([]ir.Stmt, error) {
	spec, ok := compoundOps[s.Tok]
	if !ok {
		return nil, l.errf(s.Pos(), "compound assignment %s is not supported yet", s.Tok)
	}
	if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
		return nil, l.errf(s.Pos(), "multiple assignment is not supported yet")
	}
	name, ok := s.Lhs[0].(*ast.Ident)
	if !ok {
		return nil, l.errf(s.Lhs[0].Pos(), "assignment target %T is not supported yet", s.Lhs[0])
	}
	y, err := l.lowerExpr(s.Rhs[0])
	if err != nil {
		return nil, err
	}
	inner := &ir.BinaryExpr{Op: spec.op, X: &ir.Ident{Name: name.Name}, Y: y}
	value := l.narrow(s.Lhs[0], spec.tok, inner)
	return []ir.Stmt{&ir.AssignStmt{Name: name.Name, Value: value}}, nil
}

// lowerIncDec lowers x++ and x-- to x = x + 1 and x = x - 1, masking the result
// to the target's width because an increment can overflow a sized integer and
// wrap the way Go does. The operand must be a plain name for now.
func (l *lowerer) lowerIncDec(s *ast.IncDecStmt) ([]ir.Stmt, error) {
	name, ok := s.X.(*ast.Ident)
	if !ok {
		return nil, l.errf(s.X.Pos(), "increment of %T is not supported yet", s.X)
	}
	op, tok := "+", token.ADD
	if s.Tok == token.DEC {
		op, tok = "-", token.SUB
	}
	inner := &ir.BinaryExpr{Op: op, X: &ir.Ident{Name: name.Name}, Y: &ir.IntLit{Text: "1"}}
	value := l.narrow(s.X, tok, inner)
	return []ir.Stmt{&ir.AssignStmt{Name: name.Name, Value: value}}, nil
}

// lowerLabeled lowers a statement that carries a Go label. Only a label on a loop
// is supported, where the label lets an inner break name this loop; the label
// flows into the loop lowering and out to the labeled-break pass. A label on a
// switch, a select, or a bare statement waits on a later slice, since those need
// their own jump machinery.
func (l *lowerer) lowerLabeled(s *ast.LabeledStmt) ([]ir.Stmt, error) {
	switch inner := s.Stmt.(type) {
	case *ast.ForStmt:
		return l.lowerFor(inner, s.Label.Name)
	case *ast.RangeStmt:
		return l.lowerRange(inner, s.Label.Name)
	default:
		return nil, l.errf(s.Pos(), "a label on %T is not supported yet", inner)
	}
}

// lowerBranch lowers a break or continue. An unlabeled break leaves the innermost
// loop and becomes a Python break; when the innermost breakable construct is a
// switch instead, the break leaves the switch, which the case lowering handles as
// a dropped trailing statement, so any break that reaches here from inside a
// switch is one hebi cannot yet place faithfully. A labeled break that names an
// enclosing loop becomes a LabeledBreak marker the later pass turns into flags. An
// unlabeled continue advances the innermost loop; the step a while-form loop owes
// it is threaded in by the loop lowering, not here. A labeled continue that names
// an enclosing loop becomes a LabeledContinue marker the later pass turns into
// flags, the same way a labeled break does. A goto still waits on its own slice.
func (l *lowerer) lowerBranch(s *ast.BranchStmt) ([]ir.Stmt, error) {
	switch s.Tok {
	case token.BREAK:
		if s.Label != nil {
			if !l.labelIsLoop(s.Label.Name) {
				return nil, l.errf(s.Pos(), "labeled break to %s is not supported yet", s.Label.Name)
			}
			return []ir.Stmt{&ir.LabeledBreak{Label: s.Label.Name}}, nil
		}
		if l.innermostIsSwitch() {
			return nil, l.errf(s.Pos(), "break inside a switch is only supported as the last statement of a case for now")
		}
		return []ir.Stmt{&ir.Break{}}, nil
	case token.CONTINUE:
		if s.Label != nil {
			if !l.labelIsLoop(s.Label.Name) {
				return nil, l.errf(s.Pos(), "labeled continue to %s is not supported yet", s.Label.Name)
			}
			return []ir.Stmt{&ir.LabeledContinue{Label: s.Label.Name}}, nil
		}
		return []ir.Stmt{&ir.Continue{}}, nil
	case token.GOTO:
		return nil, l.errf(s.Pos(), "goto is not supported yet")
	default:
		return nil, l.errf(s.Pos(), "fallthrough is only supported at the end of a switch case")
	}
}

// jumpKind tells a labeled break from a labeled continue in the resolve pass. The
// two share every step of the unwinding except the action taken at the loop they
// name: a break leaves that loop, a continue advances it.
type jumpKind int

const (
	jumpBreak jumpKind = iota
	jumpContinue
)

// jump is a labeled break or continue to a named loop. It is the key the resolve
// pass tracks, so a break flag and a continue flag to the same loop stay distinct
// and never collide.
type jump struct {
	label string
	kind  jumpKind
}

// jumpFlag names the boolean a jump sets, distinct per label and per kind so a
// break and a continue to the same loop do not share a flag.
func jumpFlag(j jump) string {
	if j.kind == jumpContinue {
		return "_cnt_" + j.label
	}
	return "_brk_" + j.label
}

// resolveLabeledJumps rewrites the LabeledBreak and LabeledContinue markers in a
// block into the flag machinery that reproduces a labeled jump in Python, which
// has no loop labels. A jump naming the loop it sits directly in is a plain break
// or a plain continue. A jump naming an outer loop sets a per-jump flag and breaks
// the innermost loop; then after each nested loop the flag is checked and the
// enclosing loop breaks too, so control unwinds one loop at a time until it
// reaches the named loop, where a break leaves it and a continue advances it. The
// flag is declared just before the named loop. innerLabel and innerStep are the
// label and continue step of the nearest enclosing loop, and used records which
// flags were set so a declaration is emitted only where a flag is really needed.
// The returned set is the jumps that still escape this block, for the caller to
// keep unwinding.
func resolveLabeledJumps(stmts []ir.Stmt) []ir.Stmt {
	out, _ := rewriteJumps(stmts, "", nil, map[jump]bool{})
	return out
}

func rewriteJumps(stmts []ir.Stmt, innerLabel string, innerStep []ir.Stmt, used map[jump]bool) ([]ir.Stmt, map[jump]bool) {
	escapes := map[jump]bool{}
	var out []ir.Stmt
	for _, s := range stmts {
		switch s := s.(type) {
		case *ir.LabeledBreak:
			j := jump{label: s.Label, kind: jumpBreak}
			if s.Label == innerLabel {
				// The break names the loop it sits directly in, so a plain break
				// leaves it, exactly as an unlabeled break would.
				out = append(out, &ir.Break{})
			} else {
				used[j] = true
				out = append(out, &ir.AssignStmt{Name: jumpFlag(j), Value: &ir.BoolLit{Value: true}})
				out = append(out, &ir.Break{})
				escapes[j] = true
			}
		case *ir.LabeledContinue:
			j := jump{label: s.Label, kind: jumpContinue}
			if s.Label == innerLabel {
				// The continue names the loop it sits directly in, so it runs that
				// loop's step and continues it, exactly as an unlabeled continue
				// would; a bare Python continue would skip the step.
				out = append(out, innerStep...)
				out = append(out, &ir.Continue{})
			} else {
				used[j] = true
				out = append(out, &ir.AssignStmt{Name: jumpFlag(j), Value: &ir.BoolLit{Value: true}})
				out = append(out, &ir.Break{})
				escapes[j] = true
			}
		case *ir.IfStmt:
			// An if is transparent to a jump, so its branches unwind to the same
			// enclosing loop and it never carries a post-loop check of its own.
			then, eThen := rewriteJumps(s.Then, innerLabel, innerStep, used)
			els, eElse := rewriteJumps(s.Else, innerLabel, innerStep, used)
			s.Then, s.Else = then, els
			mergeJumps(escapes, eThen)
			mergeJumps(escapes, eElse)
			out = append(out, s)
		case *ir.ForStmt:
			out = unwindLoop(out, s, &s.Body, s.Label, s.ContinueStep, innerLabel, innerStep, escapes, used)
		case *ir.ForRange:
			// A for-in-range advances on its own, so a continue to it owes no step.
			out = unwindLoop(out, s, &s.Body, s.Label, nil, innerLabel, innerStep, escapes, used)
		case *ir.RangeString:
			out = unwindLoop(out, s, &s.Body, s.Label, s.ContinueStep, innerLabel, innerStep, escapes, used)
		default:
			out = append(out, s)
		}
	}
	return out, escapes
}

// unwindLoop rewrites one nested loop's body, then places the loop into the
// surrounding block with the flag declarations it needs and the post-loop checks
// that keep a labeled jump unwinding. ownLabel and ownStep are the loop's own
// label and continue step, innerLabel and innerStep the enclosing loop's. Each
// jump the body still escapes to gets a check after the loop: a jump that has not
// reached its target breaks the enclosing loop and keeps escaping, while a jump
// that names the enclosing loop ends its unwinding there, a break leaving that
// loop and a continue running its step and advancing it.
func unwindLoop(out []ir.Stmt, loop ir.Stmt, body *[]ir.Stmt, ownLabel string, ownStep []ir.Stmt, innerLabel string, innerStep []ir.Stmt, escapes, used map[jump]bool) []ir.Stmt {
	newBody, childEsc := rewriteJumps(*body, ownLabel, ownStep, used)
	*body = newBody
	if ownLabel != "" {
		// A jump inside this loop names it, so the flag it sets must exist before
		// the loop runs, both for the first iteration and for a break's check after
		// the loop.
		for _, kind := range []jumpKind{jumpBreak, jumpContinue} {
			j := jump{label: ownLabel, kind: kind}
			if used[j] {
				out = append(out, &ir.AssignStmt{Name: jumpFlag(j), Value: &ir.BoolLit{Value: false}})
			}
		}
	}
	out = append(out, loop)
	for _, j := range sortedJumps(childEsc) {
		if j.label == innerLabel {
			out = append(out, terminalCheck(j, innerStep))
		} else {
			out = append(out, &ir.IfStmt{Cond: &ir.Ident{Name: jumpFlag(j)}, Then: []ir.Stmt{&ir.Break{}}})
			escapes[j] = true
		}
	}
	return out
}

// terminalCheck builds the check that ends a jump's unwinding at the loop it
// names. A break simply leaves the loop. A continue clears its flag so the next
// iteration starts fresh, runs the loop's step, then continues, which reproduces
// Go's continue that advances the named loop while abandoning the inner ones.
func terminalCheck(j jump, step []ir.Stmt) *ir.IfStmt {
	if j.kind == jumpContinue {
		then := []ir.Stmt{&ir.AssignStmt{Name: jumpFlag(j), Value: &ir.BoolLit{Value: false}}}
		then = append(then, step...)
		then = append(then, &ir.Continue{})
		return &ir.IfStmt{Cond: &ir.Ident{Name: jumpFlag(j)}, Then: then}
	}
	return &ir.IfStmt{Cond: &ir.Ident{Name: jumpFlag(j)}, Then: []ir.Stmt{&ir.Break{}}}
}

func mergeJumps(dst, src map[jump]bool) {
	for k := range src {
		dst[k] = true
	}
}

// sortedJumps returns the jumps in a set in a stable order, by label then kind, so
// the emitted checks do not depend on Go's map iteration order.
func sortedJumps(set map[jump]bool) []jump {
	jumps := make([]jump, 0, len(set))
	for j := range set {
		jumps = append(jumps, j)
	}
	sort.Slice(jumps, func(a, b int) bool {
		if jumps[a].label != jumps[b].label {
			return jumps[a].label < jumps[b].label
		}
		return jumps[a].kind < jumps[b].kind
	})
	return jumps
}

// runBeforeContinue rewrites a loop body so a continue first runs the given step,
// which is how a while-form loop keeps its post statement or a range over a string
// keeps its cursor advance faithful: Python's bare continue would skip the step
// and spin forever. It descends into if branches, which is where a continue
// usually hides and where a switch's lowered chain lives, but it stops at a nested
// loop, whose own continue belongs to that loop and gets its own step.
func runBeforeContinue(body []ir.Stmt, step []ir.Stmt) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(body))
	for _, s := range body {
		switch s := s.(type) {
		case *ir.Continue:
			out = append(out, step...)
			out = append(out, s)
		case *ir.IfStmt:
			s.Then = runBeforeContinue(s.Then, step)
			s.Else = runBeforeContinue(s.Else, step)
			out = append(out, s)
		default:
			out = append(out, s)
		}
	}
	return out
}

func (l *lowerer) lowerIf(s *ast.IfStmt) ([]ir.Stmt, error) {
	if s.Init != nil {
		return nil, l.errf(s.Pos(), "if with an init statement is not supported yet")
	}
	cond, err := l.lowerExpr(s.Cond)
	if err != nil {
		return nil, err
	}
	then, err := l.lowerBlock(s.Body)
	if err != nil {
		return nil, err
	}
	out := &ir.IfStmt{Cond: cond, Then: then}
	switch e := s.Else.(type) {
	case nil:
		// No else branch.
	case *ast.BlockStmt:
		els, err := l.lowerBlock(e)
		if err != nil {
			return nil, err
		}
		out.Else = els
	case *ast.IfStmt:
		// An else-if chain: the nested if becomes the single statement of the
		// else block, which reads the same in Python.
		nested, err := l.lowerIf(e)
		if err != nil {
			return nil, err
		}
		out.Else = nested
	default:
		return nil, l.errf(s.Else.Pos(), "else branch %T is not supported yet", s.Else)
	}
	return []ir.Stmt{out}, nil
}

// lowerSwitch lowers a switch to an if/elif chain, which is faithful because Go
// cases do not fall through by default and each is an implicit break, so the
// first matching branch runs and the chain ends. An expression switch spills its
// tag to a temporary so the tag runs once, and a case's value list becomes an or
// chain of equality tests against the tag. A tagless switch tests each case's
// boolean directly with no tag. The default clause, wherever it appears, becomes
// the final else, since Go takes it only when nothing else matches.
func (l *lowerer) lowerSwitch(s *ast.SwitchStmt) ([]ir.Stmt, error) {
	if s.Init != nil {
		return nil, l.errf(s.Pos(), "switch with an init statement is not supported yet")
	}
	var pre []ir.Stmt
	var tag ir.Expr
	if s.Tag != nil {
		t, err := l.lowerExpr(s.Tag)
		if err != nil {
			return nil, err
		}
		name := seqName("_tag", l.switchSeq)
		l.switchSeq++
		pre = append(pre, &ir.AssignStmt{Name: name, Value: t, Define: true})
		tag = &ir.Ident{Name: name}
	}

	type clause struct {
		conds []ir.Expr // nil marks the default clause
		body  []ir.Stmt
		fall  bool
	}
	// A switch is a breakable scope, so an unlabeled break inside a case leaves
	// the switch rather than an enclosing loop.
	l.pushScope(scope{kind: scopeSwitch})
	var clauses []clause
	for _, item := range s.Body.List {
		cc, ok := item.(*ast.CaseClause)
		if !ok {
			l.popScope()
			return nil, l.errf(item.Pos(), "switch body item %T is not supported yet", item)
		}
		var conds []ir.Expr
		for _, e := range cc.List {
			ce, err := l.lowerExpr(e)
			if err != nil {
				l.popScope()
				return nil, err
			}
			conds = append(conds, ce)
		}
		body, fall, err := l.lowerCaseBody(cc.Body)
		if err != nil {
			l.popScope()
			return nil, err
		}
		clauses = append(clauses, clause{conds: conds, body: body, fall: fall})
	}
	l.popScope()

	// resolve inlines a falling case's successor body, so a fallthrough runs the
	// next case unconditionally while that next case still runs on its own match.
	var resolve func(i int) []ir.Stmt
	resolve = func(i int) []ir.Stmt {
		out := append([]ir.Stmt(nil), clauses[i].body...)
		if clauses[i].fall && i+1 < len(clauses) {
			out = append(out, resolve(i+1)...)
		}
		return out
	}

	var branches []*ir.IfStmt
	var defaultBody []ir.Stmt
	hasDefault := false
	for i, c := range clauses {
		if c.conds == nil {
			defaultBody = resolve(i)
			hasDefault = true
			continue
		}
		branches = append(branches, &ir.IfStmt{Cond: caseCond(tag, c.conds), Then: resolve(i)})
	}

	if len(branches) == 0 {
		// Only a default, or an empty switch: no chain, just the default body.
		return append(pre, defaultBody...), nil
	}
	var elseBlock []ir.Stmt
	if hasDefault {
		elseBlock = defaultBody
	}
	for i := len(branches) - 1; i >= 0; i-- {
		branches[i].Else = elseBlock
		elseBlock = []ir.Stmt{branches[i]}
	}
	return append(pre, elseBlock...), nil
}

// lowerCaseBody lowers a case clause's statements, reporting whether the clause
// ends in a fallthrough. go/types guarantees a fallthrough is the last statement
// of a non-final case, so it is only ever the trailing statement here. A trailing
// unlabeled break is the redundant switch-exit Go programmers sometimes write for
// clarity, and since a case already ends without falling through, it is dropped;
// a break elsewhere in the case reaches the branch lowering, which reports that
// hebi cannot yet place it.
func (l *lowerer) lowerCaseBody(list []ast.Stmt) ([]ir.Stmt, bool, error) {
	fall := false
	if n := len(list); n > 0 {
		if br, ok := list[n-1].(*ast.BranchStmt); ok && br.Tok == token.FALLTHROUGH {
			fall = true
			list = list[:n-1]
		}
	}
	if n := len(list); n > 0 {
		if br, ok := list[n-1].(*ast.BranchStmt); ok && br.Tok == token.BREAK && br.Label == nil {
			list = list[:n-1]
		}
	}
	var out []ir.Stmt
	for _, s := range list {
		lowered, err := l.lowerStmt(s)
		if err != nil {
			return nil, false, err
		}
		out = append(out, lowered...)
	}
	return out, fall, nil
}

// caseCond builds a case's condition. With a tag it is an or chain of equality
// tests against the tag, one per case value; without a tag the case values are
// themselves booleans, so it is an or chain of them directly.
func caseCond(tag ir.Expr, conds []ir.Expr) ir.Expr {
	parts := conds
	if tag != nil {
		parts = make([]ir.Expr, len(conds))
		for i, c := range conds {
			parts[i] = &ir.BinaryExpr{Op: "==", X: tag, Y: c}
		}
	}
	expr := parts[0]
	for _, p := range parts[1:] {
		expr = &ir.BinaryExpr{Op: "||", X: expr, Y: p}
	}
	return expr
}

// lowerFor lowers a for statement without a range clause. A bare condition or an
// infinite loop is a while, unchanged from the earlier slice. A three-clause loop
// becomes a readable for-in-range when it is a plain forward or backward count,
// and otherwise a faithful while: the init runs before the loop, the condition
// gates it, and the post runs at the bottom of the body.
func (l *lowerer) lowerFor(s *ast.ForStmt, label string) ([]ir.Stmt, error) {
	if s.Init == nil && s.Post == nil {
		return l.lowerWhile(s, label)
	}
	if sugar, err := l.countSugar(s, label); err != nil {
		return nil, err
	} else if sugar != nil {
		return []ir.Stmt{sugar}, nil
	}
	var pre []ir.Stmt
	if s.Init != nil {
		init, err := l.lowerStmt(s.Init)
		if err != nil {
			return nil, err
		}
		pre = init
	}
	var cond ir.Expr
	if s.Cond != nil {
		c, err := l.lowerExpr(s.Cond)
		if err != nil {
			return nil, err
		}
		cond = c
	}
	body, err := l.lowerLoopBlock(s.Body, label)
	if err != nil {
		return nil, err
	}
	var contStep []ir.Stmt
	if s.Post != nil {
		// The post runs at the bottom of the loop body on a normal iteration, and
		// a continue must run it too, or the loop would skip the step and spin, so
		// the step is threaded in before each continue as well. The same step is
		// kept on the node so a labeled continue the pass injects here runs it too.
		post, err := l.lowerStmt(s.Post)
		if err != nil {
			return nil, err
		}
		contStep = post
		body = runBeforeContinue(body, post)
		body = append(body, post...)
	}
	return append(pre, &ir.ForStmt{Cond: cond, Body: body, Label: label, ContinueStep: contStep}), nil
}

// lowerWhile lowers a condition-only or infinite for loop to a while. A nil
// condition is Go's bare for and emits while True.
func (l *lowerer) lowerWhile(s *ast.ForStmt, label string) ([]ir.Stmt, error) {
	var cond ir.Expr
	if s.Cond != nil {
		c, err := l.lowerExpr(s.Cond)
		if err != nil {
			return nil, err
		}
		cond = c
	}
	body, err := l.lowerLoopBlock(s.Body, label)
	if err != nil {
		return nil, err
	}
	return []ir.Stmt{&ir.ForStmt{Cond: cond, Body: body, Label: label}}, nil
}

// countSugar recognizes a three-clause for that is a plain integer count and
// builds the for-in-range form for it, returning nil when the loop is not a
// simple count so the caller falls back to the while form. The pattern is a
// short-variable init of an integer, a condition that compares that same
// variable against a bound, a post that steps the variable by a constant in the
// matching direction, and a body that never reassigns the variable. Everything
// outside that shape is left to the always-correct while form.
func (l *lowerer) countSugar(s *ast.ForStmt, label string) (*ir.ForRange, error) {
	init, ok := s.Init.(*ast.AssignStmt)
	if !ok || init.Tok != token.DEFINE || len(init.Lhs) != 1 || len(init.Rhs) != 1 {
		return nil, nil
	}
	v, ok := init.Lhs[0].(*ast.Ident)
	if !ok || v.Name == "_" {
		return nil, nil
	}
	if _, _, isInt := intWidth(l.pkg.Info.TypeOf(v)); !isInt {
		return nil, nil
	}
	cond, ok := s.Cond.(*ast.BinaryExpr)
	if !ok {
		return nil, nil
	}
	if cv, ok := cond.X.(*ast.Ident); !ok || cv.Name != v.Name {
		return nil, nil
	}
	up, byK, ok := postStep(s.Post, v.Name)
	if !ok {
		return nil, nil
	}
	if assignsInBody(s.Body, v.Name) {
		return nil, nil
	}
	// Match the comparison to the step direction, and reject the inclusive bound
	// paired with a non-unit step, where turning the bound into a range stop
	// would risk an off-by-one.
	inclusive := false
	switch cond.Op {
	case token.LSS:
		if !up {
			return nil, nil
		}
	case token.LEQ:
		if !up || byK != "" {
			return nil, nil
		}
		inclusive = true
	case token.GTR:
		if up {
			return nil, nil
		}
	case token.GEQ:
		if up || byK != "" {
			return nil, nil
		}
		inclusive = true
	default:
		return nil, nil
	}

	start, err := l.lowerExpr(init.Rhs[0])
	if err != nil {
		return nil, err
	}
	if zeroLit(init.Rhs[0]) {
		start = nil
	}
	limit, err := l.lowerExpr(cond.Y)
	if err != nil {
		return nil, err
	}
	stop := limit
	if inclusive {
		// An inclusive bound with a unit step counts one further, so the range
		// stops one past the bound going up and one before it going down.
		adjust := "+"
		if !up {
			adjust = "-"
		}
		stop = &ir.BinaryExpr{Op: adjust, X: limit, Y: &ir.IntLit{Text: "1"}}
	}
	var step ir.Expr
	switch {
	case up && byK != "":
		step = &ir.IntLit{Text: byK}
	case !up && byK != "":
		step = &ir.IntLit{Text: "-" + byK}
	case !up:
		step = &ir.IntLit{Text: "-1"}
	}
	// A continue in a for-in-range needs no threaded step, since Python's for
	// advances the range on its own, exactly as Go's post would.
	body, err := l.lowerLoopBlock(s.Body, label)
	if err != nil {
		return nil, err
	}
	return &ir.ForRange{Var: v.Name, Start: start, Stop: stop, Step: step, Body: body, Label: label}, nil
}

// postStep reads a for loop's post statement, reporting whether it steps the loop
// variable up or down and by how much. A ++ or -- steps by one, reported with an
// empty magnitude; a += or -= steps by a positive integer literal, reported as
// its text. Anything else, including a data-dependent step, is not a countable
// stride and returns ok false so the caller uses the while form.
func postStep(post ast.Stmt, name string) (up bool, byK string, ok bool) {
	switch p := post.(type) {
	case *ast.IncDecStmt:
		id, isIdent := p.X.(*ast.Ident)
		if !isIdent || id.Name != name {
			return false, "", false
		}
		return p.Tok == token.INC, "", true
	case *ast.AssignStmt:
		if len(p.Lhs) != 1 || len(p.Rhs) != 1 {
			return false, "", false
		}
		id, isIdent := p.Lhs[0].(*ast.Ident)
		if !isIdent || id.Name != name {
			return false, "", false
		}
		if p.Tok != token.ADD_ASSIGN && p.Tok != token.SUB_ASSIGN {
			return false, "", false
		}
		lit, isLit := p.Rhs[0].(*ast.BasicLit)
		if !isLit || lit.Kind != token.INT || lit.Value == "0" {
			return false, "", false
		}
		return p.Tok == token.ADD_ASSIGN, lit.Value, true
	default:
		return false, "", false
	}
}

// zeroLit reports whether an expression is the integer literal 0, which the
// count sugar drops so a loop starting at zero reads as range(stop).
func zeroLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "0"
}

// assignsInBody reports whether a loop body reassigns the given name anywhere,
// including a shadowing short declaration, which disqualifies the count sugar
// because a Python range would not reflect the change.
func assignsInBody(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
					found = true
				}
			}
		case *ast.IncDecStmt:
			if id, ok := n.X.(*ast.Ident); ok && id.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

// lowerRange lowers a for range statement. Only range over a string is supported
// in this slice, which iterates runes and yields the byte index of each rune and
// the decoded rune; range over the other kinds arrives in a later slice. The
// source is bound to a fresh name first so it is evaluated once, then a cursor
// walks it rune by rune through the shim decoder.
func (l *lowerer) lowerRange(s *ast.RangeStmt, label string) ([]ir.Stmt, error) {
	if s.Tok != token.DEFINE && s.Tok != token.ASSIGN && s.Tok != token.ILLEGAL {
		return nil, l.errf(s.Pos(), "range with %s is not supported yet", s.Tok)
	}
	srcType := l.pkg.Info.TypeOf(s.X)
	basic, ok := srcType.Underlying().(*types.Basic)
	if !ok {
		return nil, l.errf(s.Pos(), "range over %s is not supported yet", srcType)
	}
	if basic.Info()&types.IsInteger != 0 {
		return l.lowerRangeInt(s, label)
	}
	if basic.Info()&types.IsString == 0 {
		return nil, l.errf(s.Pos(), "range over %s is not supported yet", srcType)
	}
	src, err := l.lowerExpr(s.X)
	if err != nil {
		return nil, err
	}
	var pre []ir.Stmt
	source := src
	if _, isIdent := src.(*ir.Ident); !isIdent {
		// Bind a non-trivial source once, since the emitted loop reads it in both
		// the length test and the decode, and Go evaluates the range source once.
		name := seqName("_s", l.rangeSeq)
		pre = append(pre, &ir.AssignStmt{Name: name, Value: src, Define: true})
		source = &ir.Ident{Name: name}
	}
	n := l.rangeSeq
	l.rangeSeq++
	cursor := seqName("_i", n)
	width := seqName("_w", n)
	body, err := l.lowerLoopBlock(s.Body, label)
	if err != nil {
		return nil, err
	}
	// The emitter advances the cursor at the bottom of the loop for a normal
	// iteration; a continue must advance it too, or the decode would repeat the
	// same rune forever, so the advance is threaded in before each continue.
	advance := []ir.Stmt{&ir.AssignStmt{Name: cursor, Value: &ir.BinaryExpr{Op: "+", X: &ir.Ident{Name: cursor}, Y: &ir.Ident{Name: width}}}}
	body = runBeforeContinue(body, advance)
	rs := &ir.RangeString{
		Key:          rangeIdent(s.Key),
		Value:        rangeIdent(s.Value),
		Cursor:       cursor,
		Width:        width,
		Source:       source,
		Body:         body,
		Label:        label,
		ContinueStep: advance,
	}
	return append(pre, rs), nil
}

// lowerRangeInt lowers a range over an integer, the Go 1.22 form for i := range n
// that iterates i from zero to n minus one. It is the readable for-in-range: n is
// the stop bound, the start is an implicit zero, and the step is one, so a
// non-positive n runs the body no times, matching Go. Only the index is bound,
// since Go's range over an integer has a single value.
func (l *lowerer) lowerRangeInt(s *ast.RangeStmt, label string) ([]ir.Stmt, error) {
	if s.Value != nil {
		return nil, l.errf(s.Pos(), "range over an integer binds a single value")
	}
	stop, err := l.lowerExpr(s.X)
	if err != nil {
		return nil, err
	}
	body, err := l.lowerLoopBlock(s.Body, label)
	if err != nil {
		return nil, err
	}
	return []ir.Stmt{&ir.ForRange{Var: rangeIdent(s.Key), Stop: stop, Body: body, Label: label}}, nil
}

func (l *lowerer) lowerExpr(e ast.Expr) (ir.Expr, error) {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return l.lowerExpr(e.X)
	case *ast.BasicLit:
		return l.lowerBasicLit(e)
	case *ast.Ident:
		switch e.Name {
		case "true":
			return &ir.BoolLit{Value: true}, nil
		case "false":
			return &ir.BoolLit{Value: false}, nil
		default:
			return &ir.Ident{Name: e.Name}, nil
		}
	case *ast.UnaryExpr:
		return l.lowerUnary(e)
	case *ast.BinaryExpr:
		return l.lowerBinary(e)
	case *ast.IndexExpr:
		return l.lowerIndex(e)
	case *ast.SliceExpr:
		return l.lowerSliceExpr(e)
	case *ast.SelectorExpr:
		return l.lowerSelector(e)
	case *ast.CompositeLit:
		return l.lowerCompositeLit(e)
	case *ast.CallExpr:
		return l.lowerCall(e)
	default:
		return nil, l.errf(e.Pos(), "expression %T is not supported yet", e)
	}
}

// lowerSelector lowers a selector used as a value. Only a field read is supported
// in this slice, s.field, which becomes a Python attribute access; a method value
// and a promoted field wait on the embedding slice, and a package member such as
// a constant waits on its own slice. The copy a struct-valued field read owes is
// injected by the caller through copyIfValueRead, not here, so a field read that
// is only addressed or compared is not needlessly cloned.
func (l *lowerer) lowerSelector(e *ast.SelectorExpr) (ir.Expr, error) {
	selection, ok := l.pkg.Info.Selections[e]
	if !ok {
		return nil, l.errf(e.Pos(), "selector %s is not supported yet", e.Sel.Name)
	}
	if selection.Kind() != types.FieldVal {
		return nil, l.errf(e.Pos(), "method value %s is not supported yet", e.Sel.Name)
	}
	x, err := l.lowerExpr(e.X)
	if err != nil {
		return nil, err
	}
	// A direct field is a single-step path; a promoted field steps through the
	// embedded slots go/types resolved, so u.ID reads u.Base.ID at emit time with
	// no runtime delegation, matching the type checker's selection.
	return l.fieldChain(x, l.pkg.Info.TypeOf(e.X), selection.Index()), nil
}

// fieldChain builds the nested field access a selection resolves to, following
// the embedded-field path go/types computed. Each index names a field of the
// current struct, and because an embedded field's slot is named for its type, the
// path reads the same names the type checker walked. A direct selection has a
// single index and yields one access; a promoted selection has several and yields
// the chain that reaches the promoted member.
func (l *lowerer) fieldChain(base ir.Expr, baseType types.Type, index []int) ir.Expr {
	expr := base
	t := structOf(baseType)
	for k, i := range index {
		f := t.Field(i)
		expr = &ir.FieldAccess{X: expr, Name: f.Name()}
		if k < len(index)-1 {
			t = structOf(f.Type())
		}
	}
	return expr
}

// structOf returns the struct type underlying a type, seeing through a pointer to
// a struct, or nil when the type is not a struct. The lowering only calls it while
// walking a field path go/types already validated, so the intermediate steps are
// always structs.
func structOf(t types.Type) *types.Struct {
	if t == nil {
		return nil
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	st, _ := t.Underlying().(*types.Struct)
	return st
}

// lowerCompositeLit lowers a struct composite literal to a constructor call.
// Positional and keyed forms are both supported, keyed emitting keyword
// arguments so an omitted field takes its zero default. A struct-valued element
// that reads an existing value copies, matching Go, since the literal stores an
// independent value in the field. Array, slice, and map literals wait on the
// aggregate slices.
func (l *lowerer) lowerCompositeLit(e *ast.CompositeLit) (ir.Expr, error) {
	t := l.pkg.Info.TypeOf(e)
	if arr, ok := t.Underlying().(*types.Array); ok {
		return l.lowerArrayLit(e, arr)
	}
	if _, ok := t.Underlying().(*types.Slice); ok {
		return l.lowerSliceLit(e)
	}
	named, ok := t.(*types.Named)
	if !ok {
		return nil, l.errf(e.Pos(), "composite literal of %s is not supported yet", t)
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return nil, l.errf(e.Pos(), "composite literal of %s is not supported yet", t)
	}
	lit := &ir.StructLit{Type: named.Obj().Name()}
	if len(e.Elts) == 0 {
		return lit, nil
	}
	_, lit.Keyed = e.Elts[0].(*ast.KeyValueExpr)
	for _, elt := range e.Elts {
		if lit.Keyed {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				return nil, l.errf(elt.Pos(), "mixed keyed and positional fields are not supported")
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				return nil, l.errf(kv.Key.Pos(), "struct field key %T is not supported yet", kv.Key)
			}
			value, err := l.lowerExpr(kv.Value)
			if err != nil {
				return nil, err
			}
			value = l.copyIfValueRead(kv.Value, value)
			// The keyword argument must be the constructor parameter name, which is
			// the field name except where an embedded value struct's field shadows a
			// class the constructor calls, so the two stay in step.
			lit.Fields = append(lit.Fields, ir.StructArg{Name: l.ctorKeyword(named.Obj().Name(), key.Name), Value: value})
			continue
		}
		value, err := l.lowerExpr(elt)
		if err != nil {
			return nil, err
		}
		value = l.copyIfValueRead(elt, value)
		lit.Fields = append(lit.Fields, ir.StructArg{Value: value})
	}
	return lit, nil
}

// lowerArrayLit lowers an array composite literal to an ArrayLit, a Python list
// of its elements. Only the positional form is supported in this slice; an
// index-keyed element waits on the composite-literal slice. A partial positional
// literal is padded with zero-value elements to the array length, matching Go,
// and a struct or array element that reads an existing value is cloned so the
// literal stores an independent copy. Each padded zero is built separately, so
// two zero elements never alias.
func (l *lowerer) lowerArrayLit(e *ast.CompositeLit, arr *types.Array) (ir.Expr, error) {
	n := int(arr.Len())
	elems := make([]ir.Expr, 0, n)
	for _, elt := range e.Elts {
		if _, ok := elt.(*ast.KeyValueExpr); ok {
			return nil, l.errf(elt.Pos(), "keyed array elements are not supported yet")
		}
		value, err := l.lowerExpr(elt)
		if err != nil {
			return nil, err
		}
		elems = append(elems, l.copyIfValueRead(elt, value))
	}
	for i := len(elems); i < n; i++ {
		zero, err := l.zeroValueOfType(arr.Elem(), e.Pos())
		if err != nil {
			return nil, err
		}
		elems = append(elems, zero)
	}
	return &ir.ArrayLit{Elems: elems}, nil
}

// lowerSliceLit lowers a slice composite literal to a SliceLit, a slice header
// over a fresh backing list. Unlike an array literal it is never padded, since a
// slice literal's length is exactly its element count, and a value element that
// reads an existing value is cloned so the backing owns an independent copy. The
// index-keyed form waits on the composite-literal slice, so it fails loudly.
func (l *lowerer) lowerSliceLit(e *ast.CompositeLit) (ir.Expr, error) {
	elems := make([]ir.Expr, 0, len(e.Elts))
	for _, elt := range e.Elts {
		if _, ok := elt.(*ast.KeyValueExpr); ok {
			return nil, l.errf(elt.Pos(), "keyed slice elements are not supported yet")
		}
		value, err := l.lowerExpr(elt)
		if err != nil {
			return nil, err
		}
		elems = append(elems, l.copyIfValueRead(elt, value))
	}
	return &ir.SliceLit{Elems: elems}, nil
}

// ctorKeyword returns the constructor keyword for a keyed field of a struct,
// applying the same shadow-avoiding rule the emitter uses so a keyed literal's
// keyword matches the parameter name. When the struct is not in the table, which
// does not happen for a lowered keyed literal, the field name is used as is.
func (l *lowerer) ctorKeyword(structName, fieldName string) string {
	if sd, ok := l.structs[structName]; ok {
		return sd.CtorParamName(fieldName)
	}
	return fieldName
}

// copyIfValueRead wraps a lowered expression in the value copy Go performs when
// the source reads a value type out of a location Go would copy: a plain
// variable, a field selector, or an array index. A struct value clones through
// its copy method and an array value clones element-wise through the array
// helper. A composite literal or a call already yields a fresh value, so it is
// left alone, matching the rule that the copy is injected only at a value read
// and never over an already-independent value.
func (l *lowerer) copyIfValueRead(src ast.Expr, lowered ir.Expr) ir.Expr {
	t := l.pkg.Info.TypeOf(src)
	isStruct := isStructValue(t)
	isArray := isArrayValue(t)
	if !isStruct && !isArray {
		return lowered
	}
	switch src := src.(type) {
	case *ast.ParenExpr:
		return l.copyIfValueRead(src.X, lowered)
	case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr:
		if isArray {
			return &ir.ArrayClone{X: lowered}
		}
		return &ir.Clone{X: lowered}
	default:
		return lowered
	}
}

// isStructValue reports whether a type is a struct value, not a pointer to one,
// so the copy rule fires on a value read but never on a pointer that Go shares.
func isStructValue(t types.Type) bool {
	if t == nil {
		return false
	}
	if _, ok := t.(*types.Pointer); ok {
		return false
	}
	_, ok := t.Underlying().(*types.Struct)
	return ok
}

func (l *lowerer) lowerBinary(e *ast.BinaryExpr) (ir.Expr, error) {
	x, err := l.lowerExpr(e.X)
	if err != nil {
		return nil, err
	}
	y, err := l.lowerExpr(e.Y)
	if err != nil {
		return nil, err
	}
	inner := &ir.BinaryExpr{Op: e.Op.String(), X: x, Y: y}
	return l.narrow(e, e.Op, inner), nil
}

// lowerIndex lowers an index expression. Indexing a string yields a byte as an
// int 0-255, because a Go string is Python bytes, and indexing an array reads the
// element out of the backing list; a struct or array element that yields a value
// is cloned by the caller through copyIfValueRead, not here. Indexing slices and
// maps arrives with those types. Generic instantiation shares the IndexExpr
// syntax but has neither a string nor an array operand, so the type guard keeps
// the two apart.
func (l *lowerer) lowerIndex(e *ast.IndexExpr) (ir.Expr, error) {
	xType := l.pkg.Info.TypeOf(e.X)
	switch u := xType.Underlying().(type) {
	case *types.Basic:
		if u.Info()&types.IsString == 0 {
			return nil, l.errf(e.Pos(), "indexing %s is not supported yet", xType)
		}
	case *types.Array:
		// An array index reads the element out of the Python list directly.
	case *types.Slice:
		// A slice index reads through the Slice header, which applies the offset and
		// the bounds check; a value element is cloned by the caller, not here.
	default:
		return nil, l.errf(e.Pos(), "indexing %s is not supported yet", xType)
	}
	x, err := l.lowerExpr(e.X)
	if err != nil {
		return nil, err
	}
	index, err := l.lowerExpr(e.Index)
	if err != nil {
		return nil, err
	}
	return &ir.IndexExpr{X: x, Index: index}, nil
}

// lowerSliceExpr lowers the slice expression s[low:high] or the full form
// s[low:high:max] to a SliceExpr, which builds a new slice header sharing the
// operand's backing, so the result aliases the operand the way a Go reslice does.
// The full form carries a max bound that caps the result's reserved capacity,
// which the emitter routes through the runtime since Python's slice syntax has no
// third bound. Only a slice operand is supported in this slice: a string reslice
// yields a string and an array reslice yields a slice sharing the array's backing,
// and both wait on their own slices, so they fail loudly here.
func (l *lowerer) lowerSliceExpr(e *ast.SliceExpr) (ir.Expr, error) {
	if !isSliceValue(l.pkg.Info.TypeOf(e.X)) {
		return nil, l.errf(e.Pos(), "slicing %s is not supported yet", l.pkg.Info.TypeOf(e.X))
	}
	x, err := l.lowerExpr(e.X)
	if err != nil {
		return nil, err
	}
	var low, high, max ir.Expr
	if e.Low != nil {
		if low, err = l.lowerExpr(e.Low); err != nil {
			return nil, err
		}
	}
	if e.High != nil {
		if high, err = l.lowerExpr(e.High); err != nil {
			return nil, err
		}
	}
	if e.Slice3 {
		if max, err = l.lowerExpr(e.Max); err != nil {
			return nil, err
		}
	}
	return &ir.SliceExpr{X: x, Low: low, High: high, Max: max}, nil
}

func (l *lowerer) lowerUnary(e *ast.UnaryExpr) (ir.Expr, error) {
	switch e.Op {
	case token.ADD, token.SUB:
		x, err := l.lowerExpr(e.X)
		if err != nil {
			return nil, err
		}
		inner := &ir.UnaryExpr{Op: e.Op.String(), X: x}
		// Negation can grow, MinInt has no positive counterpart, so it masks;
		// unary plus is a no-op and never does.
		return l.narrow(e, e.Op, inner), nil
	case token.NOT:
		// Logical not on a bool yields a bool and never grows, so no mask; the
		// operand is already a proven bool because Go has no truthiness.
		x, err := l.lowerExpr(e.X)
		if err != nil {
			return nil, err
		}
		return &ir.UnaryExpr{Op: e.Op.String(), X: x}, nil
	default:
		return nil, l.errf(e.Pos(), "unary %s is not supported yet", e.Op)
	}
}

// narrow renarrows inner to e's result type when the carrier is wider than the
// Go type. A float32 result round-trips through the single-precision helper,
// since Python's float is always 64-bit; a float64 result passes through
// because Python's float already is one. An integer result masks when the
// operator can grow the value past the type's range, and a constant integer is
// left bare because it is exact and the type checker has proven it in range,
// which keeps ordinary constant arithmetic readable.
//
// Left shift is a growing op and masks like the others: Python computes the
// full-precision result and the mask truncates it, which also gives Go's
// count-at-least-width-yields-zero rule for free. Right shift is not here on
// purpose. A signed right shift is arithmetic in both languages once the value
// holds its true signed form, which the mask-then-sign-extend discipline
// guarantees, and an unsigned right shift is logical because the value is a
// non-negative Python int, so neither needs a helper.
func (l *lowerer) narrow(e ast.Expr, op token.Token, inner ir.Expr) ir.Expr {
	if _, ok := floatWidth(l.pkg.Info.TypeOf(e)); ok {
		// A float32 result renarrows even when constant, because Go rounds each
		// step at single precision and Python does not; float64 passes through.
		return l.float32Wrap(e, inner)
	}
	switch op {
	case token.ADD, token.SUB, token.MUL, token.SHL:
	default:
		return inner
	}
	if tv := l.pkg.Info.Types[e]; tv.Value != nil {
		return inner
	}
	bits, signed, ok := intWidth(l.pkg.Info.TypeOf(e))
	if !ok {
		return inner
	}
	return &ir.Mask{Bits: bits, Signed: signed, X: inner}
}

// float32Wrap wraps inner in the single-precision helper when e's type is
// float32, and leaves it alone otherwise. Every producing float32 operation,
// including a literal and a conversion, is renarrowed this way.
func (l *lowerer) float32Wrap(e ast.Expr, inner ir.Expr) ir.Expr {
	if bits, ok := floatWidth(l.pkg.Info.TypeOf(e)); ok && bits == 32 {
		return &ir.Intrinsic{Name: "_f32", Args: []ir.Expr{inner}}
	}
	return inner
}

func (l *lowerer) lowerBasicLit(e *ast.BasicLit) (ir.Expr, error) {
	switch e.Kind {
	case token.INT:
		return &ir.IntLit{Text: e.Value}, nil
	case token.FLOAT:
		// A decimal float literal is already valid Python; a hexadecimal float
		// literal is not, and waits on a later slice.
		if strings.ContainsAny(e.Value, "xX") {
			return nil, l.errf(e.Pos(), "hexadecimal float literal is not supported yet")
		}
		return l.float32Wrap(e, &ir.FloatLit{Text: e.Value}), nil
	case token.STRING:
		value, err := strconv.Unquote(e.Value)
		if err != nil {
			return nil, l.errf(e.Pos(), "malformed string literal: %v", err)
		}
		return &ir.StringLit{Value: value}, nil
	default:
		return nil, l.errf(e.Pos(), "%s literal is not supported yet", e.Kind)
	}
}

func (l *lowerer) lowerCall(e *ast.CallExpr) (ir.Expr, error) {
	if l.isConversion(e) {
		return l.lowerConversion(e)
	}
	switch fun := e.Fun.(type) {
	case *ast.SelectorExpr:
		if l.isFmtPrintln(fun) {
			args, err := l.lowerPrintlnArgs(e.Args)
			if err != nil {
				return nil, err
			}
			return &ir.Intrinsic{Name: "println", Args: args}, nil
		}
		return nil, l.errf(e.Pos(), "call to %s.%s is not supported yet", exprName(fun.X), fun.Sel.Name)
	case *ast.Ident:
		if b, ok := l.pkg.Info.Uses[fun].(*types.Builtin); ok {
			return l.lowerBuiltin(e, fun, b)
		}
		args, err := l.lowerCallArgs(e.Args)
		if err != nil {
			return nil, err
		}
		return &ir.CallExpr{Name: fun.Name, Args: args}, nil
	default:
		return nil, l.errf(e.Pos(), "call target %T is not supported yet", e.Fun)
	}
}

// lowerBuiltin lowers a call to a Go builtin. len of an array, a string, or a
// slice lowers to Python's len over the plainly lowered argument, which reads the
// Slice header's length for a slice and the list or bytes length otherwise, with
// no value copy injected so len(x) reads as len(x). cap of an array equals its
// length and lowers the same way, but cap of a slice is the header's reserved
// capacity, which the length does not carry, so it reads the header's cap field
// directly. make builds a slice header over a freshly zeroed backing. The other
// builtins wait on the slices that bring their types, so an unhandled one fails
// loudly.
func (l *lowerer) lowerBuiltin(e *ast.CallExpr, fun *ast.Ident, _ *types.Builtin) (ir.Expr, error) {
	switch fun.Name {
	case "len", "cap":
		if len(e.Args) != 1 {
			return nil, l.errf(e.Pos(), "%s takes one argument", fun.Name)
		}
		arg, err := l.lowerExpr(e.Args[0])
		if err != nil {
			return nil, err
		}
		if fun.Name == "cap" && isSliceValue(l.pkg.Info.TypeOf(e.Args[0])) {
			return &ir.FieldAccess{X: arg, Name: "cap"}, nil
		}
		return &ir.CallExpr{Name: "len", Args: []ir.Expr{arg}}, nil
	case "make":
		return l.lowerMake(e)
	case "append":
		return l.lowerAppend(e)
	case "copy":
		return l.lowerCopy(e)
	default:
		return nil, l.errf(e.Pos(), "builtin %s is not supported yet", fun.Name)
	}
}

// lowerCopy lowers copy(dst, src) to the _slice_copy intrinsic, which moves the
// overlap of the two slices element by element and returns the number of elements
// moved, the smaller of the two lengths. Both arguments are slices lowered
// plainly, since copy reads the two headers and moves through the backing without
// copying either header. The runtime handles a source and destination that share
// a backing the way Go's memmove does, so overlapping regions move safely. copy
// from a string waits on its own lowering, so it fails loudly here.
func (l *lowerer) lowerCopy(e *ast.CallExpr) (ir.Expr, error) {
	if len(e.Args) != 2 {
		return nil, l.errf(e.Pos(), "copy takes a destination and a source")
	}
	if !isSliceValue(l.pkg.Info.TypeOf(e.Args[0])) {
		return nil, l.errf(e.Pos(), "copy into %s is not supported yet", l.pkg.Info.TypeOf(e.Args[0]))
	}
	if !isSliceValue(l.pkg.Info.TypeOf(e.Args[1])) {
		return nil, l.errf(e.Pos(), "copy from %s is not supported yet", l.pkg.Info.TypeOf(e.Args[1]))
	}
	dst, err := l.lowerExpr(e.Args[0])
	if err != nil {
		return nil, err
	}
	src, err := l.lowerExpr(e.Args[1])
	if err != nil {
		return nil, err
	}
	return &ir.Intrinsic{Name: "_slice_copy", Args: []ir.Expr{dst, src}}, nil
}

// lowerAppend lowers append(s, v1, v2, ...) to the _slice_append intrinsic, which
// writes into the shared backing while there is capacity and reallocates onto a
// fresh backing once it is full, matching Go's growth and its aliasing. The first
// argument is the slice, lowered plainly because append reads its header and does
// not copy it, and each appended value goes through the value copy Go performs
// when a value type is stored into the backing, so a struct or an array is cloned
// in. The spread form append(s, other...) waits on its own lowering, so it fails
// loudly.
func (l *lowerer) lowerAppend(e *ast.CallExpr) (ir.Expr, error) {
	if e.Ellipsis != token.NoPos {
		return nil, l.errf(e.Pos(), "append with a spread argument is not supported yet")
	}
	if len(e.Args) < 1 {
		return nil, l.errf(e.Pos(), "append needs a slice")
	}
	slice, err := l.lowerExpr(e.Args[0])
	if err != nil {
		return nil, err
	}
	args := make([]ir.Expr, 0, len(e.Args))
	args = append(args, slice)
	for _, a := range e.Args[1:] {
		v, err := l.lowerExpr(a)
		if err != nil {
			return nil, err
		}
		args = append(args, l.copyIfValueRead(a, v))
	}
	return &ir.Intrinsic{Name: "_slice_append", Args: args}, nil
}

// lowerMake lowers make([]T, len) and make([]T, len, cap) to a SliceMake, a
// slice header over a freshly zeroed backing whose length and capacity are the
// call's arguments, the capacity defaulting to the length when the source gave
// none. The element zero decides the backing form so a mutable element is built
// fresh per slot. make of a map or a channel waits on those types, so it fails
// loudly here.
func (l *lowerer) lowerMake(e *ast.CallExpr) (ir.Expr, error) {
	t := l.pkg.Info.TypeOf(e)
	slice, ok := t.Underlying().(*types.Slice)
	if !ok {
		return nil, l.errf(e.Pos(), "make of %s is not supported yet", t)
	}
	if len(e.Args) < 2 {
		return nil, l.errf(e.Pos(), "make of a slice needs a length")
	}
	length, err := l.lowerExpr(e.Args[1])
	if err != nil {
		return nil, err
	}
	capacity := length
	if len(e.Args) == 3 {
		if capacity, err = l.lowerExpr(e.Args[2]); err != nil {
			return nil, err
		}
	}
	elem, err := l.zeroValueOfType(slice.Elem(), e.Pos())
	if err != nil {
		return nil, err
	}
	return &ir.SliceMake{Len: length, Cap: capacity, Elem: elem, ElemMutable: isMutableType(slice.Elem())}, nil
}

// lowerPrintlnArgs lowers the arguments of fmt.Println, wrapping a float32 in
// the single-precision formatter. A float32 loses its type once it is a Python
// float, so go_str would otherwise print it with float64's shortest digits;
// the wrapper prints the digits Go prints for a float32.
func (l *lowerer) lowerPrintlnArgs(exprs []ast.Expr) ([]ir.Expr, error) {
	out := make([]ir.Expr, len(exprs))
	for i, a := range exprs {
		lowered, err := l.lowerExpr(a)
		if err != nil {
			return nil, err
		}
		if bits, ok := floatWidth(l.pkg.Info.TypeOf(a)); ok && bits == 32 {
			lowered = &ir.Intrinsic{Name: "_gofloat32", Args: []ir.Expr{lowered}}
		}
		out[i] = lowered
	}
	return out, nil
}

// isConversion reports whether a call is a type conversion T(x) rather than a
// function call, read off the type information.
func (l *lowerer) isConversion(e *ast.CallExpr) bool {
	tv, ok := l.pkg.Info.Types[e.Fun]
	return ok && tv.IsType()
}

// lowerConversion lowers a T(x) conversion between the number kinds. A
// conversion to float32 renarrows to single precision; to float64 it widens,
// which is exact and needs only a float() when the source is an integer. A
// conversion to a fixed-width integer is the destination's mask helper, with an
// int() truncation toward zero first when the source is a float, except when
// the source is itself an integer and the whole conversion is a constant the
// type checker has already proven in range.
func (l *lowerer) lowerConversion(e *ast.CallExpr) (ir.Expr, error) {
	if len(e.Args) != 1 {
		return nil, l.errf(e.Pos(), "conversion takes one argument")
	}
	dest := l.pkg.Info.TypeOf(e.Fun)
	x, err := l.lowerExpr(e.Args[0])
	if err != nil {
		return nil, err
	}
	_, srcIsFloat := floatWidth(l.pkg.Info.TypeOf(e.Args[0]))

	if bits, ok := floatWidth(dest); ok {
		if bits == 32 {
			return &ir.Intrinsic{Name: "_f32", Args: []ir.Expr{x}}, nil
		}
		if srcIsFloat {
			// float64 from a float is identity: the value already holds a Python
			// float, and widening from single precision is exact.
			return x, nil
		}
		return &ir.Convert{To: "float", X: x}, nil
	}

	if bits, signed, ok := intWidth(dest); ok {
		if srcIsFloat {
			return &ir.Mask{Bits: bits, Signed: signed, X: &ir.Convert{To: "int", X: x}}, nil
		}
		if tv := l.pkg.Info.Types[e]; tv.Value != nil {
			return x, nil
		}
		return &ir.Mask{Bits: bits, Signed: signed, X: x}, nil
	}

	return nil, l.errf(e.Pos(), "conversion to %s is not supported yet", dest)
}

// isFmtPrintln reports whether a selector is fmt.Println, checked through the
// type information rather than by name, so a local value named fmt does not
// masquerade as the package.
func (l *lowerer) isFmtPrintln(sel *ast.SelectorExpr) bool {
	if sel.Sel.Name != "Println" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	pkgName, ok := l.pkg.Info.Uses[ident].(*types.PkgName)
	if !ok {
		return false
	}
	return pkgName.Imported().Path() == "fmt"
}

// lowerCallArgs lowers the arguments of a call to a named function, cloning a
// struct value passed by value, which is the copy-on-call site of the
// value-semantics rule: the callee receives an independent value, so a mutation
// inside it must not touch the caller's. A non-struct argument and a fresh value
// such as a literal or another call are left alone by copyIfValueRead.
func (l *lowerer) lowerCallArgs(exprs []ast.Expr) ([]ir.Expr, error) {
	out := make([]ir.Expr, len(exprs))
	for i, a := range exprs {
		lowered, err := l.lowerExpr(a)
		if err != nil {
			return nil, err
		}
		out[i] = l.copyIfValueRead(a, lowered)
	}
	return out, nil
}

// intWidth returns the width in bits and signedness of a fixed-width integer
// type, and whether the type is one. int and uint are 64-bit by hebi's target
// contract, so output is deterministic across a 32-bit and 64-bit host.
func intWidth(t types.Type) (bits int, signed bool, ok bool) {
	basic, isBasic := t.Underlying().(*types.Basic)
	if !isBasic {
		return 0, false, false
	}
	switch basic.Kind() {
	case types.Int8:
		return 8, true, true
	case types.Int16:
		return 16, true, true
	case types.Int32:
		return 32, true, true
	case types.Int, types.Int64:
		return 64, true, true
	case types.Uint8:
		return 8, false, true
	case types.Uint16:
		return 16, false, true
	case types.Uint32:
		return 32, false, true
	case types.Uint, types.Uint64, types.Uintptr:
		return 64, false, true
	default:
		return 0, false, false
	}
}

// floatWidth returns the width in bits of a floating-point type, and whether
// the type is one. An untyped float constant defaults to 64-bit, matching Go's
// default type for an untyped float.
func floatWidth(t types.Type) (bits int, ok bool) {
	basic, isBasic := t.Underlying().(*types.Basic)
	if !isBasic {
		return 0, false
	}
	switch basic.Kind() {
	case types.Float32:
		return 32, true
	case types.Float64, types.UntypedFloat:
		return 64, true
	default:
		return 0, false
	}
}

func exprName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return "?"
}
