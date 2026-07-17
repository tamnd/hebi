package build

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
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
	l := &lowerer{pkg: pkg}
	m := &ir.Module{Package: pkg.Name}
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

type lowerer struct {
	pkg *frontend.Package
	// rangeSeq numbers the internal names a range over a string allocates, so a
	// nested or repeated range gets distinct cursor and width names. It counts
	// across the whole module in source order, which keeps the emit deterministic.
	rangeSeq int
	// switchSeq numbers the tag temporary an expression switch spills to, so a
	// nested switch does not reuse an outer switch's tag name.
	switchSeq int
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
	if fd.Type.Params != nil && len(fd.Type.Params.List) > 0 {
		return nil, l.errf(fd.Pos(), "function parameters are not supported yet")
	}
	if fd.Type.Results != nil && len(fd.Type.Results.List) > 0 {
		return nil, l.errf(fd.Pos(), "function results are not supported yet")
	}
	if fd.Body == nil {
		return nil, l.errf(fd.Pos(), "function %s has no body", fd.Name.Name)
	}
	body, err := l.lowerBlock(fd.Body)
	if err != nil {
		return nil, err
	}
	return &ir.Func{Name: fd.Name.Name, Body: body}, nil
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
	case *ast.IfStmt:
		return l.lowerIf(s)
	case *ast.ForStmt:
		return l.lowerFor(s)
	case *ast.RangeStmt:
		return l.lowerRange(s)
	case *ast.SwitchStmt:
		return l.lowerSwitch(s)
	case *ast.ExprStmt:
		x, err := l.lowerExpr(s.X)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{&ir.ExprStmt{X: x}}, nil
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
			out = append(out, &ir.AssignStmt{Name: name.Name, Value: value, Define: true})
		}
	}
	return out, nil
}

// zeroValue produces the Go zero value for a declared name's type.
func (l *lowerer) zeroValue(name *ast.Ident) (ir.Expr, error) {
	basic, ok := l.pkg.Info.TypeOf(name).Underlying().(*types.Basic)
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
	return nil, l.errf(name.Pos(), "zero value of %s is not supported yet", l.pkg.Info.TypeOf(name))
}

func (l *lowerer) lowerAssign(s *ast.AssignStmt) ([]ir.Stmt, error) {
	if s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
		return nil, l.errf(s.Pos(), "compound assignment %s is not supported yet", s.Tok)
	}
	if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
		return nil, l.errf(s.Pos(), "multiple assignment is not supported yet")
	}
	name, ok := s.Lhs[0].(*ast.Ident)
	if !ok {
		return nil, l.errf(s.Lhs[0].Pos(), "assignment target %T is not supported yet", s.Lhs[0])
	}
	value, err := l.lowerExpr(s.Rhs[0])
	if err != nil {
		return nil, err
	}
	return []ir.Stmt{&ir.AssignStmt{Name: name.Name, Value: value, Define: s.Tok == token.DEFINE}}, nil
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
	var clauses []clause
	for _, item := range s.Body.List {
		cc, ok := item.(*ast.CaseClause)
		if !ok {
			return nil, l.errf(item.Pos(), "switch body item %T is not supported yet", item)
		}
		var conds []ir.Expr
		for _, e := range cc.List {
			ce, err := l.lowerExpr(e)
			if err != nil {
				return nil, err
			}
			conds = append(conds, ce)
		}
		body, fall, err := l.lowerCaseBody(cc.Body)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, clause{conds: conds, body: body, fall: fall})
	}

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
// of a non-final case, so it is only ever the trailing statement here.
func (l *lowerer) lowerCaseBody(list []ast.Stmt) ([]ir.Stmt, bool, error) {
	fall := false
	if n := len(list); n > 0 {
		if br, ok := list[n-1].(*ast.BranchStmt); ok && br.Tok == token.FALLTHROUGH {
			fall = true
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

func (l *lowerer) lowerFor(s *ast.ForStmt) ([]ir.Stmt, error) {
	if s.Init != nil || s.Post != nil {
		return nil, l.errf(s.Pos(), "for with an init or post statement is not supported yet")
	}
	var cond ir.Expr
	if s.Cond != nil {
		c, err := l.lowerExpr(s.Cond)
		if err != nil {
			return nil, err
		}
		cond = c
	}
	body, err := l.lowerBlock(s.Body)
	if err != nil {
		return nil, err
	}
	return []ir.Stmt{&ir.ForStmt{Cond: cond, Body: body}}, nil
}

// lowerRange lowers a for range statement. Only range over a string is supported
// in this slice, which iterates runes and yields the byte index of each rune and
// the decoded rune; range over the other kinds arrives in a later slice. The
// source is bound to a fresh name first so it is evaluated once, then a cursor
// walks it rune by rune through the shim decoder.
func (l *lowerer) lowerRange(s *ast.RangeStmt) ([]ir.Stmt, error) {
	if s.Tok != token.DEFINE && s.Tok != token.ASSIGN && s.Tok != token.ILLEGAL {
		return nil, l.errf(s.Pos(), "range with %s is not supported yet", s.Tok)
	}
	srcType := l.pkg.Info.TypeOf(s.X)
	basic, ok := srcType.Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsString == 0 {
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
	body, err := l.lowerBlock(s.Body)
	if err != nil {
		return nil, err
	}
	rs := &ir.RangeString{
		Key:    rangeIdent(s.Key),
		Value:  rangeIdent(s.Value),
		Cursor: seqName("_i", n),
		Width:  seqName("_w", n),
		Source: source,
		Body:   body,
	}
	return append(pre, rs), nil
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
	case *ast.CallExpr:
		return l.lowerCall(e)
	default:
		return nil, l.errf(e.Pos(), "expression %T is not supported yet", e)
	}
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

// lowerIndex lowers an index expression. Only indexing a string is supported in
// this slice, which yields a byte as an int 0-255 because a Go string is Python
// bytes; indexing slices, arrays, and maps arrives with those types. Generic
// instantiation shares the IndexExpr syntax but never has a string operand, so
// the type guard keeps the two apart.
func (l *lowerer) lowerIndex(e *ast.IndexExpr) (ir.Expr, error) {
	xType := l.pkg.Info.TypeOf(e.X)
	basic, ok := xType.Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsString == 0 {
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
		args, err := l.lowerArgs(e.Args)
		if err != nil {
			return nil, err
		}
		return &ir.CallExpr{Name: fun.Name, Args: args}, nil
	default:
		return nil, l.errf(e.Pos(), "call target %T is not supported yet", e.Fun)
	}
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

func (l *lowerer) lowerArgs(exprs []ast.Expr) ([]ir.Expr, error) {
	out := make([]ir.Expr, len(exprs))
	for i, a := range exprs {
		lowered, err := l.lowerExpr(a)
		if err != nil {
			return nil, err
		}
		out[i] = lowered
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
