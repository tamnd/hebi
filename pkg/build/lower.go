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
// emitter consumes. It covers the M0 hello-scale subset and returns a positioned
// error for any surface it does not yet handle, so an unsupported construct
// fails loudly rather than emitting something wrong.
func lower(pkg *frontend.Package) (*ir.Module, error) {
	l := &lowerer{pkg: pkg}
	m := &ir.Module{Package: pkg.Name}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				// Imports and other declarations carry no runtime behavior at
				// M0 and are skipped; the type checker already read them.
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
		out = append(out, lowered)
	}
	return out, nil
}

func (l *lowerer) lowerStmt(s ast.Stmt) (ir.Stmt, error) {
	switch s := s.(type) {
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
		return &ir.ExprStmt{X: x}, nil
	default:
		return nil, l.errf(s.Pos(), "statement %T is not supported yet", s)
	}
}

func (l *lowerer) lowerAssign(s *ast.AssignStmt) (ir.Stmt, error) {
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
	return &ir.AssignStmt{Name: name.Name, Value: value, Define: s.Tok == token.DEFINE}, nil
}

func (l *lowerer) lowerIf(s *ast.IfStmt) (ir.Stmt, error) {
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
		// An else-if chain: lower the nested if as the single statement of the
		// else block, which reads the same in Python.
		nested, err := l.lowerIf(e)
		if err != nil {
			return nil, err
		}
		out.Else = []ir.Stmt{nested}
	default:
		return nil, l.errf(s.Else.Pos(), "else branch %T is not supported yet", s.Else)
	}
	return out, nil
}

func (l *lowerer) lowerFor(s *ast.ForStmt) (ir.Stmt, error) {
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
	return &ir.ForStmt{Cond: cond, Body: body}, nil
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
	case *ast.BinaryExpr:
		x, err := l.lowerExpr(e.X)
		if err != nil {
			return nil, err
		}
		y, err := l.lowerExpr(e.Y)
		if err != nil {
			return nil, err
		}
		return &ir.BinaryExpr{Op: e.Op.String(), X: x, Y: y}, nil
	case *ast.CallExpr:
		return l.lowerCall(e)
	default:
		return nil, l.errf(e.Pos(), "expression %T is not supported yet", e)
	}
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

func exprName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return "?"
}
