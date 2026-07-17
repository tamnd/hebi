package build

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

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
	return l.wrapGrowing(e, e.Op, inner), nil
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
		return l.wrapGrowing(e, e.Op, inner), nil
	default:
		return nil, l.errf(e.Pos(), "unary %s is not supported yet", e.Op)
	}
}

// wrapGrowing wraps inner in the width mask for e's result type when the
// operator can grow the value past the type's range. Constant expressions are
// exact and already in range, so they are never masked, which keeps ordinary
// constant arithmetic readable.
//
// Left shift is a growing op and masks like the others: Python computes the
// full-precision result and the mask truncates it, which also gives Go's
// count-at-least-width-yields-zero rule for free. Right shift is not here on
// purpose. A signed right shift is arithmetic in both languages once the value
// holds its true signed form, which the mask-then-sign-extend discipline
// guarantees, and an unsigned right shift is logical because the value is a
// non-negative Python int, so neither needs a helper.
func (l *lowerer) wrapGrowing(e ast.Expr, op token.Token, inner ir.Expr) ir.Expr {
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

func (l *lowerer) lowerBasicLit(e *ast.BasicLit) (ir.Expr, error) {
	switch e.Kind {
	case token.INT:
		return &ir.IntLit{Text: e.Value}, nil
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
	args, err := l.lowerArgs(e.Args)
	if err != nil {
		return nil, err
	}
	switch fun := e.Fun.(type) {
	case *ast.SelectorExpr:
		if l.isFmtPrintln(fun) {
			return &ir.Intrinsic{Name: "println", Args: args}, nil
		}
		return nil, l.errf(e.Pos(), "call to %s.%s is not supported yet", exprName(fun.X), fun.Sel.Name)
	case *ast.Ident:
		return &ir.CallExpr{Name: fun.Name, Args: args}, nil
	default:
		return nil, l.errf(e.Pos(), "call target %T is not supported yet", e.Fun)
	}
}

// isConversion reports whether a call is a type conversion T(x) rather than a
// function call, read off the type information.
func (l *lowerer) isConversion(e *ast.CallExpr) bool {
	tv, ok := l.pkg.Info.Types[e.Fun]
	return ok && tv.IsType()
}

// lowerConversion lowers a T(x) conversion. A conversion to a fixed-width
// integer is exactly the destination's mask helper, except when the whole
// conversion is a constant, which the type checker has already proven in range.
func (l *lowerer) lowerConversion(e *ast.CallExpr) (ir.Expr, error) {
	if len(e.Args) != 1 {
		return nil, l.errf(e.Pos(), "conversion takes one argument")
	}
	dest := l.pkg.Info.TypeOf(e.Fun)
	bits, signed, ok := intWidth(dest)
	if !ok {
		return nil, l.errf(e.Pos(), "conversion to %s is not supported yet", dest)
	}
	x, err := l.lowerExpr(e.Args[0])
	if err != nil {
		return nil, err
	}
	if tv := l.pkg.Info.Types[e]; tv.Value != nil {
		return x, nil
	}
	return &ir.Mask{Bits: bits, Signed: signed, X: x}, nil
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

func exprName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return "?"
}
