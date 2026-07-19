package build

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"go/version"
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
			if fd.Recv != nil {
				owner, method, err := l.lowerMethod(fd)
				if err != nil {
					return nil, err
				}
				owner.Methods = append(owner.Methods, method)
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
	if _, ok := t.Underlying().(*types.Pointer); ok {
		// A pointer field can hold a nil zero, but pointing it at anything needs the
		// address of a struct or a heap value, which waits on the struct-pointer
		// slice, so a struct with a pointer field is deferred whole rather than half
		// working.
		return ir.StructField{}, l.errf(pos, "a pointer field %s is not supported yet", name)
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
	// errorsAsSeq numbers the value and ok temporaries an errors.As call spills to,
	// so two calls in one function do not reuse a name.
	errorsAsSeq int
	// scopes is the stack of enclosing loops and switches, innermost last, which
	// tells a break whether it leaves a loop or a switch and lets a labeled break
	// find the loop it names.
	scopes []scope
	// resultNames holds the named result variables of the function being lowered,
	// in declaration order, or nil when the function has unnamed results. A bare
	// return in a function with named results returns these, so the lowerer needs
	// them in hand while it walks the body.
	resultNames []string
	// resultTypes holds the declared type of each result of the function being
	// lowered, one per result value position, so a bare nil returned into a result
	// slot can read its sentinel from the destination type the way a compared nil
	// reads it from the other operand. It is set for named and unnamed results
	// alike and cleared when the function is done.
	resultTypes []types.Type
	// deferReshape is set while lowering a deferring function whose deferred calls
	// read or change a named result or call recover, so its body needs the try and
	// except reshape rather than the plain finally. A return in such a body lowers to
	// a DeferReturn that runs the deferred calls before it hands the results back.
	deferReshape bool
	// deferResults holds the result variable names a reshaped deferring function
	// returns, in declaration order, empty for a void function. A DeferReturn reads
	// these to build its handoff, and the reshape block returns them on the recovered
	// panic path.
	deferResults []string
	// pendingDefs holds the nested defs a closure lowering hoisted, waiting to be
	// flushed just above the statement whose expression created them. A multi
	// statement function literal cannot be an expression in Python, so it lowers to
	// a def placed before its use; lowerBlock drains this before each statement.
	pendingDefs []ir.Stmt
	// funcSeq numbers the internal names hoisted closures take, _func, _func1, and
	// so on, across the whole module in source order so the emit stays determinate.
	funcSeq int
	// snapshot is the set of Go 1.22 per-iteration loop variables in scope, the
	// ones a closure in the loop body must freeze at creation with a default
	// argument to keep the value the iteration held. It is empty under the pre-1.22
	// shared-variable semantics, where a closure captures the one shared variable.
	snapshot map[types.Object]bool
	// boxed is the set of scalar locals in the function being lowered whose address
	// is taken, so each is stored in a Cell and every read and write goes through
	// the cell's get and set. Taking the address of such a local is then just naming
	// its cell, which is how a pointer to a plain local reads on Python.
	boxed map[types.Object]bool
	// recv is the receiver variable of the method being lowered, or nil when the
	// current function is not a method. Every read of the receiver lowers to self,
	// whatever the Go source named it, so the method body reads as ordinary Python.
	recv types.Object
	// aliased is the set of struct and array locals in the function being lowered
	// whose address is taken. A struct or array is already a reference object in
	// Python, so its address is the instance itself with no cell, and a pointer to
	// it and the local share that one instance. Rebinding such a local wholesale
	// would strand the pointer on the old instance, so that one write is diagnosed.
	aliased map[types.Object]bool
	// instancePtr is the set of pointer locals bound to the address of a struct or
	// array local, so the pointer is that instance rather than a cell. A field or
	// element pointer is a cell that carries a set, so a write through it works, but
	// a whole write through an instance pointer would need a field-by-field copy
	// into the shared instance, so that one write is diagnosed rather than emitted.
	instancePtr map[types.Object]bool
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
	prelude, err := l.beginResults(fd.Type.Results)
	if err != nil {
		return nil, err
	}
	defer func() { l.resultNames = nil; l.resultTypes = nil }()
	if fd.Body == nil {
		return nil, l.errf(fd.Pos(), "function %s has no body", fd.Name.Name)
	}
	if err := l.beginDefer(fd); err != nil {
		return nil, err
	}
	defer func() { l.deferReshape = false; l.deferResults = nil }()
	l.boxed = l.computeBoxed(fd)
	l.aliased = l.computeAliased(fd)
	l.instancePtr = l.computeInstancePtrs(fd, l.aliased)
	defer func() { l.boxed = nil; l.aliased = nil; l.instancePtr = nil }()
	boxInit := l.boxParamInits(fd.Type.Params)
	body, err := l.lowerBlock(fd.Body)
	if err != nil {
		return nil, err
	}
	body = resolveLabeledJumps(body)
	body, err = l.wrapDefers(fd, body)
	if err != nil {
		return nil, err
	}
	body = append(append(boxInit, prelude...), body...)
	return &ir.Func{Name: fd.Name.Name, Params: params, Body: body}, nil
}

// lowerMethod lowers a method declaration to a Python instance method attached to
// the receiver type's class, and returns that class along with the method. The
// receiver becomes self, so the receiver name is recorded for the body lowering
// and dropped from the parameter list. A value receiver and a pointer receiver
// lower the same way here, an instance method over self, because the copy a value
// receiver needs is passed by the caller, not taken inside the method. A method on
// a non-struct named type, which has no class, waits on its own slice.
func (l *lowerer) lowerMethod(fd *ast.FuncDecl) (*ir.StructDef, *ir.Method, error) {
	if fd.Type.TypeParams != nil && len(fd.Type.TypeParams.List) > 0 {
		return nil, nil, l.errf(fd.Pos(), "type parameters are not supported yet")
	}
	if fd.Body == nil {
		return nil, nil, l.errf(fd.Pos(), "method %s has no body", fd.Name.Name)
	}
	recvField := fd.Recv.List[0]
	typeName, err := l.receiverTypeName(recvField.Type)
	if err != nil {
		return nil, nil, err
	}
	owner, ok := l.structs[typeName]
	if !ok {
		return nil, nil, l.errf(fd.Pos(), "a method on non-struct type %s is not supported yet", typeName)
	}
	if len(recvField.Names) == 1 && recvField.Names[0].Name != "_" {
		l.recv = l.pkg.Info.Defs[recvField.Names[0]]
	}
	defer func() { l.recv = nil }()
	params, err := l.lowerParams(fd.Type.Params)
	if err != nil {
		return nil, nil, err
	}
	prelude, err := l.beginResults(fd.Type.Results)
	if err != nil {
		return nil, nil, err
	}
	defer func() { l.resultNames = nil; l.resultTypes = nil }()
	if err := l.beginDefer(fd); err != nil {
		return nil, nil, err
	}
	defer func() { l.deferReshape = false; l.deferResults = nil }()
	l.boxed = l.computeBoxed(fd)
	l.aliased = l.computeAliased(fd)
	l.instancePtr = l.computeInstancePtrs(fd, l.aliased)
	defer func() { l.boxed = nil; l.aliased = nil; l.instancePtr = nil }()
	boxInit := l.boxParamInits(fd.Type.Params)
	body, err := l.lowerBlock(fd.Body)
	if err != nil {
		return nil, nil, err
	}
	body = resolveLabeledJumps(body)
	body, err = l.wrapDefers(fd, body)
	if err != nil {
		return nil, nil, err
	}
	body = append(append(boxInit, prelude...), body...)
	return owner, &ir.Method{Name: fd.Name.Name, Params: params, Body: body}, nil
}

// receiverTypeName returns the named type a method receiver binds to, seeing
// through a pointer receiver. A parenthesized or otherwise shaped receiver waits
// on its own slice.
func (l *lowerer) receiverTypeName(t ast.Expr) (string, error) {
	switch rt := t.(type) {
	case *ast.Ident:
		return rt.Name, nil
	case *ast.StarExpr:
		if id, ok := rt.X.(*ast.Ident); ok {
			return id.Name, nil
		}
	}
	return "", l.errf(t.Pos(), "receiver type %T is not supported yet", t)
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

// beginResults records the function's named result variables on the lowerer and
// returns the prelude that binds each to its zero value at the top of the body,
// so a named result reads as its zero before the body writes it and a bare return
// returns the current values. A function with unnamed results clears resultNames
// and returns no prelude, so its returns flow through the explicit values. A
// result named with the blank identifier is rejected, since it names a slot the
// body cannot write yet a bare return would still have to return.
func (l *lowerer) beginResults(fields *ast.FieldList) ([]ir.Stmt, error) {
	l.resultNames = nil
	l.resultTypes = nil
	if fields == nil {
		return nil, nil
	}
	// Record the type of each result position, named or not, so a returned bare nil
	// can resolve its sentinel from the destination type.
	for _, field := range fields.List {
		t := l.pkg.Info.TypeOf(field.Type)
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			l.resultTypes = append(l.resultTypes, t)
		}
	}
	named := false
	for _, field := range fields.List {
		if len(field.Names) > 0 {
			named = true
			break
		}
	}
	if !named {
		return nil, nil
	}
	var prelude []ir.Stmt
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			return nil, l.errf(field.Pos(), "a function mixing named and unnamed results is not supported yet")
		}
		zero, err := l.zeroValueOfType(l.pkg.Info.TypeOf(field.Type), field.Pos())
		if err != nil {
			return nil, err
		}
		for _, name := range field.Names {
			if name.Name == "_" {
				return nil, l.errf(name.Pos(), "a blank named result is not supported yet")
			}
			l.resultNames = append(l.resultNames, name.Name)
			prelude = append(prelude, &ir.AssignStmt{Name: name.Name, Value: zero, Define: true})
		}
	}
	return prelude, nil
}

// lowerReturn lowers a return statement. A bare return in a function with unnamed
// results carries no value; in a function with named results it returns the
// current named values, one or several. A single returned value flows through
// ReturnStmt, and several returned values become a tuple the caller unpacks. Every
// returned value that reads a struct or array out of a copyable location is cloned,
// since the caller receives an independent value, which is the copy-on-return site
// of the value-semantics rule.
func (l *lowerer) lowerReturn(s *ast.ReturnStmt) ([]ir.Stmt, error) {
	if l.deferReshape {
		return l.lowerReshapedReturn(s)
	}
	if len(s.Results) == 0 {
		if len(l.resultNames) == 0 {
			return []ir.Stmt{&ir.ReturnStmt{}}, nil
		}
		if len(l.resultNames) == 1 {
			return []ir.Stmt{&ir.ReturnStmt{Value: &ir.Ident{Name: l.resultNames[0]}}}, nil
		}
		elems := make([]ir.Expr, len(l.resultNames))
		for i, name := range l.resultNames {
			elems[i] = &ir.Ident{Name: name}
		}
		return []ir.Stmt{&ir.ReturnStmt{Value: &ir.Tuple{Elems: elems}}}, nil
	}
	if len(s.Results) == 1 {
		value, err := l.lowerTyped(s.Results[0], l.resultTypeAt(0))
		if err != nil {
			return nil, err
		}
		value = l.copyIfValueRead(s.Results[0], value)
		return []ir.Stmt{&ir.ReturnStmt{Value: value}}, nil
	}
	elems := make([]ir.Expr, len(s.Results))
	for i, r := range s.Results {
		value, err := l.lowerTyped(r, l.resultTypeAt(i))
		if err != nil {
			return nil, err
		}
		elems[i] = l.copyIfValueRead(r, value)
	}
	return []ir.Stmt{&ir.ReturnStmt{Value: &ir.Tuple{Elems: elems}}}, nil
}

// lowerReshapedReturn lowers a return inside a reshaped deferring function. The
// deferred calls run after the results are set and may still mutate a named
// result, so a return cannot hand its values straight back. Each explicit return
// value is first assigned into its named result variable, then an ir.DeferReturn
// drains the defer stack and returns the result variables. A bare return skips
// the assignment, since the named results already hold what a bare return means.
func (l *lowerer) lowerReshapedReturn(s *ast.ReturnStmt) ([]ir.Stmt, error) {
	var stmts []ir.Stmt
	if len(s.Results) > 0 {
		if len(s.Results) != len(l.deferResults) {
			return nil, l.errf(s.Pos(), "return with %d values in a deferring function with %d results is not supported yet", len(s.Results), len(l.deferResults))
		}
		for i, r := range s.Results {
			value, err := l.lowerTyped(r, l.resultTypeAt(i))
			if err != nil {
				return nil, err
			}
			value = l.copyIfValueRead(r, value)
			stmts = append(stmts, &ir.AssignStmt{Name: l.deferResults[i], Value: value})
		}
	}
	stmts = append(stmts, &ir.DeferReturn{Results: l.deferResults})
	return stmts, nil
}

// lowerFuncLit lowers a Go function literal to a closure. A literal whose body is
// a single return of one expression becomes a Python lambda, the readable form,
// unless the caller forces a def because it will immediately call the closure,
// which a lambda cannot carry as a statement. Every other literal, and every one
// with named results or more than one statement, becomes a def hoisted just above
// its use. A closure that reads a Go 1.22 per-iteration loop variable freezes it
// with a default argument, and a closure that writes an enclosing local declares
// that local nonlocal, so both match Go's capture by reference.
func (l *lowerer) lowerFuncLit(e *ast.FuncLit, forceDef bool) (ir.Expr, error) {
	params, err := l.lowerParams(e.Type.Params)
	if err != nil {
		return nil, err
	}
	captures, err := l.closureCaptures(e)
	if err != nil {
		return nil, err
	}
	if ret := singleReturnExpr(e); !forceDef && ret != nil && !containsFuncLit(ret) {
		body, err := l.lowerExpr(ret)
		if err != nil {
			return nil, err
		}
		body = l.copyIfValueRead(ret, body)
		return &ir.Lambda{Params: params, Captures: captures, Body: body}, nil
	}
	return l.lowerFuncLitDef(e, params, captures)
}

// lowerFuncLitDef lowers a function literal to a hoisted nested def, queued in
// pendingDefs and referred to by the name it is given. It lowers the body under a
// saved and restored result-name context, since the closure has its own results,
// and computes the nonlocal declarations the body's writes to enclosing locals
// need.
func (l *lowerer) lowerFuncLitDef(e *ast.FuncLit, params []string, captures []ir.Capture) (ir.Expr, error) {
	nonlocals, err := l.closureNonlocals(e)
	if err != nil {
		return nil, err
	}
	saved := l.resultNames
	savedReshape, savedResults := l.deferReshape, l.deferResults
	l.deferReshape, l.deferResults = false, nil
	restore := func() {
		l.resultNames = saved
		l.deferReshape, l.deferResults = savedReshape, savedResults
	}
	prelude, err := l.beginResults(e.Type.Results)
	if err != nil {
		restore()
		return nil, err
	}
	body, err := l.lowerBlock(e.Body)
	restore()
	if err != nil {
		return nil, err
	}
	body = append(prelude, body...)
	body = resolveLabeledJumps(body)
	name := seqName("_func", l.funcSeq)
	l.funcSeq++
	l.pendingDefs = append(l.pendingDefs, &ir.FuncDef{
		Name:      name,
		Params:    params,
		Captures:  captures,
		Nonlocals: nonlocals,
		Body:      body,
	})
	return &ir.Ident{Name: name}, nil
}

// singleReturnExpr returns the single returned expression of a function literal
// whose body is exactly one return of one value and whose results are unnamed, or
// nil when the literal is not that shape, in which case it lowers to a def rather
// than a lambda.
func singleReturnExpr(e *ast.FuncLit) ast.Expr {
	if hasNamedResults(e.Type.Results) {
		return nil
	}
	if len(e.Body.List) != 1 {
		return nil
	}
	ret, ok := e.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil
	}
	return ret.Results[0]
}

// hasNamedResults reports whether a result list names any of its results, which a
// lambda cannot express, so such a literal lowers to a def.
func hasNamedResults(results *ast.FieldList) bool {
	if results == nil {
		return false
	}
	for _, field := range results.List {
		if len(field.Names) > 0 {
			return true
		}
	}
	return false
}

// containsFuncLit reports whether an expression holds a nested function literal,
// which a lambda body cannot hoist, so a return expression that does lowers its
// closure to a def instead.
func containsFuncLit(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			found = true
			return false
		}
		return !found
	})
	return found
}

// closureCaptures builds the default-argument snapshots a closure needs, one for
// each in-scope per-iteration loop variable it reads and does not itself write. A
// read inside a nested closure counts too, since the value must be frozen where
// this closure is made. A variable the closure writes is left to the nonlocal
// path instead, so a name is never both a parameter and a nonlocal.
func (l *lowerer) closureCaptures(e *ast.FuncLit) ([]ir.Capture, error) {
	if len(l.snapshot) == 0 {
		return nil, nil
	}
	writes := l.closureWrites(e)
	seen := map[types.Object]bool{}
	var caps []ir.Capture
	ast.Inspect(e.Body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj := l.pkg.Info.ObjectOf(id)
		if obj == nil || !l.snapshot[obj] || writes[obj] || seen[obj] {
			return true
		}
		seen[obj] = true
		caps = append(caps, ir.Capture{Param: obj.Name(), Value: &ir.Ident{Name: obj.Name()}})
		return true
	})
	sort.Slice(caps, func(i, j int) bool { return caps[i].Param < caps[j].Param })
	return caps, nil
}

// closureNonlocals returns the enclosing locals a closure body assigns, which
// Python needs declared nonlocal so the write reaches the outer binding rather
// than making a fresh local, reproducing Go's capture by reference. It looks at
// this closure's own writes, not a nested closure's, since Python resolves a
// nested nonlocal to the nearest enclosing binding on its own. A write to a
// package-level variable is a module global, which this slice does not handle, so
// it fails loudly.
func (l *lowerer) closureNonlocals(e *ast.FuncLit) ([]string, error) {
	seen := map[types.Object]bool{}
	var names []string
	var bad error
	for obj := range l.closureWrites(e) {
		if obj.Pos() >= e.Pos() && obj.Pos() < e.End() {
			// Declared inside the closure, so the write binds the closure's own local.
			continue
		}
		if l.boxed[obj] {
			// A write to an address-taken local goes through its cell's set, not a
			// rebind of the name, so the closure needs no nonlocal to reach it.
			continue
		}
		if obj.Parent() != nil && obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope() {
			bad = l.errf(e.Pos(), "assignment to package-level variable %s from a closure is not supported yet", obj.Name())
			continue
		}
		if !seen[obj] {
			seen[obj] = true
			names = append(names, obj.Name())
		}
	}
	if bad != nil {
		return nil, bad
	}
	sort.Strings(names)
	return names, nil
}

// closureWrites returns the objects a closure body assigns directly, through =,
// a compound assignment, or ++ and --, not descending into a nested closure,
// which accounts for its own writes. A := target is a fresh local of the closure,
// so it is not a write to an enclosing variable and is skipped.
func (l *lowerer) closureWrites(e *ast.FuncLit) map[types.Object]bool {
	w := map[types.Object]bool{}
	record := func(target ast.Expr) {
		id, ok := target.(*ast.Ident)
		if !ok || id.Name == "_" {
			return
		}
		if obj := l.pkg.Info.ObjectOf(id); obj != nil {
			w[obj] = true
		}
	}
	first := true
	ast.Inspect(e.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			if first {
				first = false
				return true
			}
			return false
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE {
				for _, lhs := range n.Lhs {
					record(lhs)
				}
			}
		case *ast.IncDecStmt:
			record(n.X)
		}
		return true
	})
	return w
}

// forLoopVars returns the loop variables a three-clause for declares with := in
// its init, the ones Go 1.22 makes fresh each iteration, or nil when the init is
// not a short declaration.
func forLoopVars(s *ast.ForStmt) []*ast.Ident {
	init, ok := s.Init.(*ast.AssignStmt)
	if !ok || init.Tok != token.DEFINE {
		return nil
	}
	var ids []*ast.Ident
	for _, lhs := range init.Lhs {
		if id, ok := lhs.(*ast.Ident); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// identOf returns e as an identifier, or nil when it is absent or not a bare
// name, so a range clause's key or value position resolves to the variable it
// declares or to nothing.
func identOf(e ast.Expr) *ast.Ident {
	id, _ := e.(*ast.Ident)
	return id
}

// computeBoxed finds the scalar locals and parameters of a function whose address
// is taken with &x, the ones that must live in a Cell so a pointer to the local
// names one shared slot. It looks only at &ident in this function's own body, not
// inside a nested function literal, which boxes its own locals in its own frame. A
// loop variable, a named result, a package-level variable, and a non-scalar are
// left out, so taking the address of one of those still reaches the diagnosis in
// lowerAddr rather than silently boxing a case this slice does not model.
func (l *lowerer) computeBoxed(fd *ast.FuncDecl) map[types.Object]bool {
	loopVars := l.loopVarObjects(fd.Body)
	results := l.resultObjects(fd.Type.Results)
	boxed := map[types.Object]bool{}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		u, ok := n.(*ast.UnaryExpr)
		if !ok || u.Op != token.AND {
			return true
		}
		id, ok := astUnparen(u.X).(*ast.Ident)
		if !ok {
			return true
		}
		v, ok := l.pkg.Info.ObjectOf(id).(*types.Var)
		if !ok || v.IsField() {
			return true
		}
		if loopVars[v] || results[v] || l.isPackageScope(v) {
			return true
		}
		if _, ok := v.Type().Underlying().(*types.Basic); !ok {
			return true
		}
		boxed[v] = true
		return true
	})
	if len(boxed) == 0 {
		return nil
	}
	return boxed
}

// computeAliased finds the struct and array locals of a function whose address is
// taken with &x. Unlike a scalar, a struct or array is a reference object in
// Python, so its address is the instance itself and no cell is needed; the pointer
// and the local share the one instance, and a field or element write through
// either is seen by both. The same exclusions as boxing apply, so a loop variable,
// a named result, a package-level variable, or a field still reaches the diagnosis
// in lowerAddr rather than being modeled here.
func (l *lowerer) computeAliased(fd *ast.FuncDecl) map[types.Object]bool {
	loopVars := l.loopVarObjects(fd.Body)
	results := l.resultObjects(fd.Type.Results)
	aliased := map[types.Object]bool{}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		u, ok := n.(*ast.UnaryExpr)
		if !ok || u.Op != token.AND {
			return true
		}
		id, ok := astUnparen(u.X).(*ast.Ident)
		if !ok {
			return true
		}
		v, ok := l.pkg.Info.ObjectOf(id).(*types.Var)
		if !ok || v.IsField() {
			return true
		}
		if loopVars[v] || results[v] || l.isPackageScope(v) {
			return true
		}
		if !isStructValue(v.Type()) && !isArrayValue(v.Type()) {
			return true
		}
		aliased[v] = true
		return true
	})
	if len(aliased) == 0 {
		return nil
	}
	return aliased
}

// computeInstancePtrs finds the pointer locals bound to the address of a struct or
// array local, meaning name := &alias where alias is in the aliased set. Such a
// pointer is the instance itself, not a cell, so a whole write through it, *name =
// v, cannot go through a cell set and is diagnosed in lowerAssign. A pointer to a
// field or an element is a cell instead, so it is not collected here.
func (l *lowerer) computeInstancePtrs(fd *ast.FuncDecl, aliased map[types.Object]bool) map[types.Object]bool {
	if len(aliased) == 0 {
		return nil
	}
	ptrs := map[types.Object]bool{}
	record := func(lhs ast.Expr, rhs ast.Expr) {
		u, ok := astUnparen(rhs).(*ast.UnaryExpr)
		if !ok || u.Op != token.AND {
			return
		}
		src, ok := astUnparen(u.X).(*ast.Ident)
		if !ok || !aliased[l.pkg.Info.ObjectOf(src)] {
			return
		}
		if name, ok := lhs.(*ast.Ident); ok && name.Name != "_" {
			if obj := l.pkg.Info.ObjectOf(name); obj != nil {
				ptrs[obj] = true
			}
		}
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		switch s := n.(type) {
		case *ast.AssignStmt:
			if len(s.Lhs) == len(s.Rhs) {
				for i := range s.Lhs {
					record(s.Lhs[i], s.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			if len(s.Names) == len(s.Values) {
				for i := range s.Names {
					record(s.Names[i], s.Values[i])
				}
			}
		}
		return true
	})
	if len(ptrs) == 0 {
		return nil
	}
	return ptrs
}

// loopVarObjects collects the variables a for or range loop declares in the body,
// which are excluded from boxing because a pointer to a loop variable has its own
// per-iteration lifetime this slice does not model yet.
func (l *lowerer) loopVarObjects(body *ast.BlockStmt) map[types.Object]bool {
	out := map[types.Object]bool{}
	add := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok && id.Name != "_" {
			if obj := l.pkg.Info.Defs[id]; obj != nil {
				out[obj] = true
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.ForStmt:
			for _, id := range forLoopVars(s) {
				add(id)
			}
		case *ast.RangeStmt:
			if s.Tok == token.DEFINE {
				add(s.Key)
				add(s.Value)
			}
		}
		return true
	})
	return out
}

// resultObjects collects the named result variables of a function, which are
// excluded from boxing because a bare return returns them by name and a cell would
// return the box rather than the value it holds.
func (l *lowerer) resultObjects(fields *ast.FieldList) map[types.Object]bool {
	out := map[types.Object]bool{}
	if fields == nil {
		return out
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			if obj := l.pkg.Info.Defs[name]; obj != nil {
				out[obj] = true
			}
		}
	}
	return out
}

// wrapDefers wraps a lowered function body in a DeferBlock when the source
// contains a defer, so the body runs under a try whose finally fires the deferred
// calls in reverse order. A body with no defer is left flat. A reshaping frame,
// one with named results or a deferred recover, carries its result names on the
// block, so the block's emitter can drain the defers and return the results after
// an unrecovered panic unwinds through them.
func (l *lowerer) wrapDefers(fd *ast.FuncDecl, body []ir.Stmt) ([]ir.Stmt, error) {
	if !bodyHasDefer(fd.Body) {
		return body, nil
	}
	return []ir.Stmt{&ir.DeferBlock{Body: body, Reshape: l.deferReshape, Results: l.deferResults}}, nil
}

// beginDefer records on the lowerer whether the function being lowered reshapes
// its defers, and if so the result variable names the reshaped body returns. A
// function with no defer never reshapes. A function whose deferred calls may run
// after a return and still change a named result, either because it has named
// results or because a deferred call recovers, reshapes: its returns route through
// the defer stack rather than hand a value straight back. A deferred recover in a
// function with unnamed non-void results has no result variable to return after
// the unwind, so it is diagnosed rather than lowered to a body that would drop the
// recovered result.
func (l *lowerer) beginDefer(fd *ast.FuncDecl) error {
	l.deferReshape = false
	l.deferResults = nil
	if !bodyHasDefer(fd.Body) {
		return nil
	}
	if len(l.resultNames) > 0 {
		l.deferReshape = true
		l.deferResults = append([]string(nil), l.resultNames...)
		return nil
	}
	if l.deferHasRecover(fd) {
		if !resultsAreVoid(fd.Type.Results) {
			return l.errf(fd.Pos(), "a deferred recover in a function with unnamed results is not supported yet")
		}
		l.deferReshape = true
	}
	return nil
}

// resultsAreVoid reports whether a result list carries no values, so a reshaped
// body returns nothing after its defers unwind.
func resultsAreVoid(fields *ast.FieldList) bool {
	return fields == nil || len(fields.List) == 0
}

// deferHasRecover reports whether a deferred call in the function's own frame calls
// the builtin recover, directly or inside the deferred function literal's body. A
// recover in a nested non-deferred closure belongs to that closure's frame, so the
// walk does not descend into one.
func (l *lowerer) deferHasRecover(fd *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		d, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		ast.Inspect(d.Call, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if b, ok := l.pkg.Info.Uses[id].(*types.Builtin); ok && b.Name() == "recover" {
				found = true
			}
			return true
		})
		return false
	})
	return found
}

// bodyHasDefer reports whether a function body contains a defer statement in its
// own frame. A defer inside a nested function literal belongs to that closure, so
// the walk does not descend into one.
func bodyHasDefer(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.DeferStmt:
			found = true
			return false
		}
		return true
	})
	return found
}

// isPackageScope reports whether an object is declared at package scope, so its
// address is a package-global pointer this slice does not model.
func (l *lowerer) isPackageScope(obj types.Object) bool {
	return obj.Parent() != nil && obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope()
}

// boxParamInits builds the statements that re-home each address-taken parameter
// into a cell at the top of the body, x = Cell(x), so a pointer to a parameter
// aims at one shared slot the same way a pointer to a local does.
func (l *lowerer) boxParamInits(fields *ast.FieldList) []ir.Stmt {
	if fields == nil || len(l.boxed) == 0 {
		return nil
	}
	var out []ir.Stmt
	for _, field := range fields.List {
		for _, name := range field.Names {
			if obj := l.pkg.Info.Defs[name]; obj != nil && l.boxed[obj] {
				out = append(out, &ir.AssignStmt{Name: name.Name, Value: l.cellOf(&ir.Ident{Name: name.Name})})
			}
		}
	}
	return out
}

// cellOf wraps a value in a Cell construction, the box a pointer to a local names.
func (l *lowerer) cellOf(v ir.Expr) ir.Expr {
	return &ir.Intrinsic{Name: "Cell", Args: []ir.Expr{v}}
}

// isBoxedIdent reports whether an identifier refers to an address-taken local that
// lives in a cell, so its reads and writes go through the cell.
func (l *lowerer) isBoxedIdent(id *ast.Ident) bool {
	if len(l.boxed) == 0 {
		return false
	}
	return l.boxed[l.pkg.Info.ObjectOf(id)]
}

// isReceiver reports whether an identifier names the receiver of the method being
// lowered, which reads as self.
func (l *lowerer) isReceiver(id *ast.Ident) bool {
	return l.recv != nil && l.pkg.Info.ObjectOf(id) == l.recv
}

// identRead lowers a read of an identifier, naming the receiver self, going
// through the cell with a deref when the local is boxed, and naming it directly
// otherwise.
func (l *lowerer) identRead(id *ast.Ident) ir.Expr {
	if l.isReceiver(id) {
		return &ir.Ident{Name: "self"}
	}
	if l.isBoxedIdent(id) {
		return &ir.Deref{X: &ir.Ident{Name: id.Name}}
	}
	return &ir.Ident{Name: id.Name}
}

// nilSentinel lowers a nil of a known type to the zero-value sentinel it names, a
// nil pointer, a nil slice, or a nil map. An interface, function, or channel nil
// waits on its own slice.
func (l *lowerer) nilSentinel(t types.Type, pos token.Pos) (ir.Expr, error) {
	switch t.Underlying().(type) {
	case *types.Pointer:
		return &ir.NilPtr{}, nil
	case *types.Slice:
		return &ir.NilSlice{}, nil
	case *types.Map:
		return &ir.NilMap{}, nil
	case *types.Interface:
		// A nil interface, an error among them, is None, so a nil check is identity
		// against None rather than the sentinel equality a pointer or slice uses.
		return &ir.NilInterface{}, nil
	default:
		return nil, l.errf(pos, "nil of type %s is not supported yet", t)
	}
}

// nilFor lowers the bare nil identifier where its type is not fixed by context.
// An untyped nil carries no type of its own, so a nil that is not compared against
// a typed operand, the one context this slice reads its type from, waits on the
// slice that threads a destination type into every position nil can appear.
func (l *lowerer) nilFor(id *ast.Ident) (ir.Expr, error) {
	t := l.pkg.Info.TypeOf(id)
	if t == nil || isUntypedNil(t) {
		return nil, l.errf(id.Pos(), "nil in this position is not supported yet")
	}
	return l.nilSentinel(t, id.Pos())
}

// isUntypedNil reports whether a type is the predeclared untyped nil, which is
// what go/types records for the nil identifier since nil never takes a default
// type of its own the way an untyped number does.
func isUntypedNil(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Kind() == types.UntypedNil
}

// isNilExpr reports whether an expression is the bare nil identifier, so a caller
// that knows the surrounding type can lower it to the right sentinel.
func (l *lowerer) isNilExpr(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok || id.Name != "nil" {
		return false
	}
	t := l.pkg.Info.TypeOf(id)
	return t != nil && isUntypedNil(t)
}

// lowerTyped lowers an expression that flows into a known destination type, so a
// bare nil in a return, an assignment, or a declaration reads its sentinel from
// that type the way a compared nil reads it from the other operand. Anything but
// a bare untyped nil, and a nil with no destination type in hand, lowers plainly.
func (l *lowerer) lowerTyped(e ast.Expr, want types.Type) (ir.Expr, error) {
	if want != nil && l.isNilExpr(e) {
		return l.nilSentinel(want, e.Pos())
	}
	return l.lowerExpr(e)
}

// resultTypeAt returns the declared type of the result at position i, or nil when
// the position is out of range, so a returned nil can find its destination type.
func (l *lowerer) resultTypeAt(i int) types.Type {
	if i < 0 || i >= len(l.resultTypes) {
		return nil
	}
	return l.resultTypes[i]
}

// snapshotLoopVars registers the given loop variables as per-iteration snapshots
// for the duration of a loop body, under Go 1.22 semantics only, and returns a
// function that removes them again. Under the older shared-variable semantics it
// registers nothing, so a closure captures the one shared variable. A blank or
// absent variable, or one the type checker did not record as a fresh declaration,
// is skipped.
func (l *lowerer) snapshotLoopVars(pos token.Pos, idents ...*ast.Ident) func() {
	if !l.newLoopSemantics(pos) {
		return func() {}
	}
	var added []types.Object
	for _, id := range idents {
		if id == nil || id.Name == "_" {
			continue
		}
		obj := l.pkg.Info.Defs[id]
		if obj == nil {
			continue
		}
		if l.snapshot == nil {
			l.snapshot = map[types.Object]bool{}
		}
		if !l.snapshot[obj] {
			l.snapshot[obj] = true
			added = append(added, obj)
		}
	}
	return func() {
		for _, obj := range added {
			delete(l.snapshot, obj)
		}
	}
}

// loopVarCaptured reports whether a closure anywhere in the loop body reads the
// loop variable by name, which is what makes the shared-versus-per-iteration
// distinction observable and so decides whether the count sugar is safe under the
// older semantics.
func loopVarCaptured(body *ast.BlockStmt, name string) bool {
	captured := false
	ast.Inspect(body, func(n ast.Node) bool {
		fl, ok := n.(*ast.FuncLit)
		if !ok {
			return !captured
		}
		ast.Inspect(fl.Body, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Name == name {
				captured = true
			}
			return !captured
		})
		return false
	})
	return captured
}

// newLoopSemantics reports whether the file at pos compiles under Go 1.22 or
// later, where a loop declares a fresh variable each iteration. An unrecorded
// version is treated as current, since the loaded module sets the language
// version and only a deliberately older file opts out.
func (l *lowerer) newLoopSemantics(pos token.Pos) bool {
	v := l.fileVersion(pos)
	if v == "" {
		return true
	}
	return version.Compare(v, "go1.22") >= 0
}

// fileVersion returns the Go language version go/types resolved for the file that
// contains pos, or the empty string when none was recorded.
func (l *lowerer) fileVersion(pos token.Pos) string {
	if l.pkg.Info.FileVersions == nil {
		return ""
	}
	for _, f := range l.pkg.Files {
		if pos >= f.FileStart && pos < f.FileEnd {
			return l.pkg.Info.FileVersions[f]
		}
	}
	return ""
}

func (l *lowerer) lowerBlock(block *ast.BlockStmt) ([]ir.Stmt, error) {
	var out []ir.Stmt
	for _, s := range block.List {
		lowered, err := l.lowerStmt(s)
		if err != nil {
			return nil, err
		}
		// A closure the statement created hoists to a def just above the statement
		// that uses it, so its name is bound before the use.
		out = append(out, l.flushPendingDefs()...)
		out = append(out, lowered...)
	}
	return out, nil
}

// flushPendingDefs returns the closures hoisted so far and clears the queue, so
// the caller can place them just above the statement that uses them.
func (l *lowerer) flushPendingDefs() []ir.Stmt {
	if len(l.pendingDefs) == 0 {
		return nil
	}
	defs := l.pendingDefs
	l.pendingDefs = nil
	return defs
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
		if panicCall := l.asPanicCall(s.X); panicCall != nil {
			return l.lowerPanic(panicCall)
		}
		// A bare errors.As discards its ok but keeps its target side effect, so it
		// spills to a temporary ok the same way the condition form does.
		if call, ok := l.errorsAsCall(s.X); ok {
			okName := seqName("_ok", l.errorsAsSeq)
			return l.errorsAsPre(call, okName, true)
		}
		x, err := l.lowerExpr(s.X)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{&ir.ExprStmt{X: x}}, nil
	case *ast.ReturnStmt:
		return l.lowerReturn(s)
	case *ast.DeferStmt:
		return l.lowerDefer(s)
	default:
		return nil, l.errf(s.Pos(), "statement %T is not supported yet", s)
	}
}

// lowerDefer lowers a defer statement to a DeferPush that records the callable and
// its arguments at the point the defer runs. Go evaluates the deferred call's
// function value and its arguments when the defer statement is reached, not when
// the call finally fires, so the receiver of a method value and every argument are
// snapshotted here, and a value receiver takes its copy at this point exactly as a
// plain method value does.
func (l *lowerer) lowerDefer(s *ast.DeferStmt) ([]ir.Stmt, error) {
	if s.Call.Ellipsis != token.NoPos {
		return nil, l.errf(s.Pos(), "a deferred call with a spread argument is not supported yet")
	}
	fn, args, err := l.lowerDeferCallee(s.Call)
	if err != nil {
		return nil, err
	}
	return []ir.Stmt{&ir.DeferPush{Func: fn, Args: args}}, nil
}

// asPanicCall returns the call expression when x is a call to the builtin panic,
// or nil otherwise, so lowerStmt can route it to the raise form rather than treat
// it as a plain call expression. A user function named panic would resolve to a
// non-builtin object and not match.
func (l *lowerer) asPanicCall(x ast.Expr) *ast.CallExpr {
	call, ok := x.(*ast.CallExpr)
	if !ok {
		return nil
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil
	}
	if b, ok := l.pkg.Info.Uses[id].(*types.Builtin); ok && b.Name() == "panic" {
		return call
	}
	return nil
}

// lowerPanic lowers a panic call to an ir.Panic that raises a GoPanic carrying the
// argument. A panic with a nil argument raises the runtime's PanicNilError from Go
// 1.21 on, matching the language change that made panic(nil) a run-time error; an
// older file version has no such error, so it is diagnosed rather than lowered to a
// panic with a nil value the surface would render wrong.
func (l *lowerer) lowerPanic(call *ast.CallExpr) ([]ir.Stmt, error) {
	arg := call.Args[0]
	if l.pkg.Info.Types[arg].IsNil() {
		if version.Compare(l.fileVersion(call.Pos()), "go1.21") >= 0 {
			return []ir.Stmt{&ir.Panic{Value: &ir.Intrinsic{Name: "PanicNilError"}}}, nil
		}
		return nil, l.errf(call.Pos(), "panic with a nil argument before go1.21 is not supported yet")
	}
	value, err := l.lowerExpr(arg)
	if err != nil {
		return nil, err
	}
	value = l.copyIfValueRead(arg, value)
	return []ir.Stmt{&ir.Panic{Value: value}}, nil
}

// lowerDeferCallee splits a deferred call into the callable value and the
// arguments the DeferPush records. A plain function is its name, a method value
// binds and copies its receiver like any method value, an immediately deferred
// function literal hoists to a nested def the push then names, and fmt.Println
// becomes the shim's println function reference so the finally can call it later.
// A deferred builtin or an otherwise shaped callee waits on its own slice.
func (l *lowerer) lowerDeferCallee(call *ast.CallExpr) (ir.Expr, []ir.Expr, error) {
	switch fun := astUnparen(call.Fun).(type) {
	case *ast.FuncLit:
		callee, err := l.lowerFuncLit(fun, true)
		if err != nil {
			return nil, nil, err
		}
		args, err := l.lowerCallArgs(call.Args)
		if err != nil {
			return nil, nil, err
		}
		return callee, args, nil
	case *ast.SelectorExpr:
		if l.isFmtPrintln(fun) {
			args, err := l.lowerPrintlnArgs(call.Args)
			if err != nil {
				return nil, nil, err
			}
			return &ir.ShimFunc{Name: "println"}, args, nil
		}
		if sel, ok := l.pkg.Info.Selections[fun]; ok && sel.Kind() == types.MethodVal {
			callee, err := l.lowerMethodValue(fun, sel)
			if err != nil {
				return nil, nil, err
			}
			args, err := l.lowerCallArgs(call.Args)
			if err != nil {
				return nil, nil, err
			}
			return callee, args, nil
		}
		return nil, nil, l.errf(call.Pos(), "a deferred call to %s.%s is not supported yet", exprName(fun.X), fun.Sel.Name)
	case *ast.Ident:
		if _, ok := l.pkg.Info.Uses[fun].(*types.Builtin); ok {
			return nil, nil, l.errf(call.Pos(), "a deferred builtin call %s is not supported yet", fun.Name)
		}
		args, err := l.lowerCallArgs(call.Args)
		if err != nil {
			return nil, nil, err
		}
		return &ir.Ident{Name: fun.Name}, args, nil
	default:
		return nil, nil, l.errf(call.Pos(), "a deferred call target %T is not supported yet", call.Fun)
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
				if l.isBoxedIdent(name) {
					zero = l.cellOf(zero)
				}
				out = append(out, &ir.AssignStmt{Name: name.Name, Value: zero, Define: true})
			}
			continue
		}
		if len(vs.Names) != len(vs.Values) {
			return nil, l.errf(vs.Pos(), "multiple assignment is not supported yet")
		}
		for i, name := range vs.Names {
			value, err := l.lowerTyped(vs.Values[i], l.pkg.Info.TypeOf(name))
			if err != nil {
				return nil, err
			}
			if name.Name != "_" {
				value = l.copyIfValueRead(vs.Values[i], value)
			}
			if l.isBoxedIdent(name) {
				value = l.cellOf(value)
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
	if _, ok := t.Underlying().(*types.Map); ok {
		// A map is a reference too, so its zero value is the nil map sentinel, which
		// reads as empty and panics on write exactly as a Go nil map does.
		return &ir.NilMap{}, nil
	}
	if _, ok := t.Underlying().(*types.Pointer); ok {
		// A pointer's zero value is nil, the sentinel that compares equal only to
		// itself and panics the Go way when a program reads through it.
		return &ir.NilPtr{}, nil
	}
	if _, ok := t.Underlying().(*types.Interface); ok {
		// An interface's zero value is the nil interface, None, so var err error
		// starts as None and reads equal to nil until the body writes it.
		return &ir.NilInterface{}, nil
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

// isMapValue reports whether a type is a Go map, which like a slice is a
// reference: it aliases rather than copies, so a map value read is never cloned
// and a nil map reads as empty yet panics on write.
func isMapValue(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Map)
	return ok
}

// isInterfaceValue reports whether a type's underlying type is an interface,
// such as the error interface, so a comparison against nil can pick the identity
// spelling rather than the sentinel equality a pointer or slice uses.
func isInterfaceValue(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

// checkMapKey rejects the map key types this slice does not carry. A basic key
// such as an int or a string is hashable in Python as it is, and a comparable
// struct key carries the generated hash and equality, so both pass. An array key
// is comparable in Go but lowers to a mutable, unhashable Python list, so it waits
// on the tuple-key form a later slice adds and fails loudly here.
func (l *lowerer) checkMapKey(key types.Type, pos token.Pos) error {
	switch key.Underlying().(type) {
	case *types.Basic, *types.Struct:
		return nil
	default:
		return l.errf(pos, "map with a %s key is not supported yet", key)
	}
}

// cloneForType wraps x in the value copy a struct or an array read owes, and
// returns nil for a reference or scalar type that shares or copies on its own.
// It is the clone the comma-ok read and the map range inject when they bind a
// value the caller may mutate independently of the map.
func (l *lowerer) cloneForType(t types.Type, x ir.Expr) ir.Expr {
	switch {
	case isStructValue(t):
		return &ir.Clone{X: x}
	case isArrayValue(t):
		return &ir.ArrayClone{X: x}
	default:
		return nil
	}
}

// astUnparen strips any enclosing parentheses from an expression, so a
// parenthesized comma-ok source such as (m[k]) is recognized as a map index.
func astUnparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
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
	if len(s.Lhs) > 1 {
		return l.lowerMultiAssign(s)
	}
	if len(s.Rhs) != 1 {
		return nil, l.errf(s.Pos(), "multiple assignment is not supported yet")
	}
	// ok := errors.As(err, &t) binds the ok flag and sets the target as a side
	// effect, so it lowers to the spill rather than a plain value assignment.
	if call, ok := l.errorsAsCall(s.Rhs[0]); ok {
		return l.lowerErrorsAsBind(s, call)
	}
	value, err := l.lowerTyped(s.Rhs[0], l.pkg.Info.TypeOf(s.Lhs[0]))
	if err != nil {
		return nil, err
	}
	// A struct value assigned to a location copies, so the reference-semantic
	// Python instance is cloned where Go would make an independent value. A blank
	// assignment discards the value and creates no independent copy, so it does
	// not clone.
	switch lhs := s.Lhs[0].(type) {
	case *ast.Ident:
		if s.Tok == token.ASSIGN && l.aliased[l.pkg.Info.ObjectOf(lhs)] {
			// The local's address is taken and a pointer shares its instance, so
			// rebinding it wholesale would leave the pointer on the old instance. A
			// field or element write keeps the instance, so only this whole-value
			// rebind is held back, for the slice that models pointer storage fully.
			return nil, l.errf(lhs.Pos(), "reassigning the address-taken value %s is not supported yet", lhs.Name)
		}
		if lhs.Name != "_" {
			value = l.copyIfValueRead(s.Rhs[0], value)
		}
		if l.isBoxedIdent(lhs) {
			// The local lives in a cell, so a define builds the cell around the value
			// and a plain assignment writes through it, keeping every pointer to the
			// local aimed at the one shared slot.
			if s.Tok == token.DEFINE {
				return []ir.Stmt{&ir.AssignStmt{Name: lhs.Name, Value: l.cellOf(value), Define: true}}, nil
			}
			return []ir.Stmt{&ir.DerefSet{Ptr: &ir.Ident{Name: lhs.Name}, Value: value}}, nil
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
		// assignment writes the entry through the dict, and a write to a nil map
		// raises through the sentinel's __setitem__, exactly as Go panics.
		xt := l.pkg.Info.TypeOf(lhs.X)
		if !isArrayValue(xt) && !isSliceValue(xt) && !isMapValue(xt) {
			return nil, l.errf(lhs.Pos(), "index assignment to %s is not supported yet", xt)
		}
		if mp, ok := xt.Underlying().(*types.Map); ok {
			if err := l.checkMapKey(mp.Key(), lhs.Pos()); err != nil {
				return nil, err
			}
		}
		obj, err := l.lowerExpr(lhs.X)
		if err != nil {
			return nil, err
		}
		index, err := l.lowerExpr(lhs.Index)
		if err != nil {
			return nil, err
		}
		if isMapValue(xt) {
			// Go copies the key into the map on insert, so a struct key is cloned; a
			// basic key is left alone by copyIfValueRead.
			index = l.copyIfValueRead(lhs.Index, index)
		}
		value = l.copyIfValueRead(s.Rhs[0], value)
		return []ir.Stmt{&ir.SetIndex{Object: obj, Index: index, Value: value}}, nil
	case *ast.StarExpr:
		// A write through a pointer, *p = v, reaches the pointed-at slot through the
		// pointer object's set, so a struct value is cloned first exactly as a write
		// to any other location copies. A field or element pointer is a cell that
		// carries that set, so the write works. A pointer bound to a struct or array
		// local is the instance itself, not a cell, so overwriting the whole value
		// would have to copy field by field into the shared instance; that one write
		// waits on the slice that models pointer storage fully.
		if id, ok := astUnparen(lhs.X).(*ast.Ident); ok && l.instancePtr[l.pkg.Info.ObjectOf(id)] {
			return nil, l.errf(lhs.Pos(), "writing through a pointer to a struct or array local is not supported yet")
		}
		ptr, err := l.lowerExpr(lhs.X)
		if err != nil {
			return nil, err
		}
		value = l.copyIfValueRead(s.Rhs[0], value)
		return []ir.Stmt{&ir.DerefSet{Ptr: ptr, Value: value}}, nil
	default:
		return nil, l.errf(s.Lhs[0].Pos(), "assignment target %T is not supported yet", s.Lhs[0])
	}
}

// lowerMultiAssign lowers an assignment with more than one target. Three forms
// reach it: the comma-ok map read v, ok := m[k], which binds a value and a
// present flag; the unpack of a call that returns several values, a, b := f(),
// which binds each returned value positionally; and the parallel assignment
// a, b = c, d, which Go evaluates in full before it binds any target, so a swap
// a, b = b, a is faithful to a Python tuple assignment. A field or an index
// target in a tuple assignment waits on its own slice and fails loudly.
func (l *lowerer) lowerMultiAssign(s *ast.AssignStmt) ([]ir.Stmt, error) {
	if len(s.Rhs) == 1 {
		if call, ok := astUnparen(s.Rhs[0]).(*ast.CallExpr); ok {
			return l.lowerUnpackCall(s, call)
		}
		if len(s.Lhs) == 2 {
			return l.lowerMapCommaOk(s)
		}
		return nil, l.errf(s.Pos(), "multiple assignment is not supported yet")
	}
	if len(s.Rhs) != len(s.Lhs) {
		return nil, l.errf(s.Pos(), "a parallel assignment needs one value per target")
	}
	names, err := l.multiAssignNames(s.Lhs)
	if err != nil {
		return nil, err
	}
	elems := make([]ir.Expr, len(s.Rhs))
	for i, r := range s.Rhs {
		value, err := l.lowerExpr(r)
		if err != nil {
			return nil, err
		}
		// A struct or an array value assigned to a target copies, and a blank target
		// discards its value and owes no copy, matching the single assignment.
		if names[i] != "_" {
			value = l.copyIfValueRead(r, value)
		}
		elems[i] = value
	}
	return []ir.Stmt{&ir.TupleAssign{Names: names, Value: &ir.Tuple{Elems: elems}, Define: s.Tok == token.DEFINE}}, nil
}

// lowerUnpackCall lowers a, b := f(), the unpack of a call that returns several
// values, into a tuple assignment from the call. Python unpacks the returned
// tuple positionally, and the function already cloned each struct or array value
// on the way out, so the unpack binds independent values and owes no copy here.
func (l *lowerer) lowerUnpackCall(s *ast.AssignStmt, call *ast.CallExpr) ([]ir.Stmt, error) {
	names, err := l.multiAssignNames(s.Lhs)
	if err != nil {
		return nil, err
	}
	value, err := l.lowerExpr(call)
	if err != nil {
		return nil, err
	}
	return []ir.Stmt{&ir.TupleAssign{Names: names, Value: value, Define: s.Tok == token.DEFINE}}, nil
}

// multiAssignNames returns the plain-name targets of a tuple assignment. A
// selector or an index target in a multiple assignment waits on its own slice,
// so anything but an identifier, including the blank _, fails loudly here unless
// it is a name.
func (l *lowerer) multiAssignNames(lhs []ast.Expr) ([]string, error) {
	names := make([]string, len(lhs))
	for i, t := range lhs {
		id, ok := t.(*ast.Ident)
		if !ok {
			return nil, l.errf(t.Pos(), "a multiple assignment target must be a name for now")
		}
		if l.isBoxedIdent(id) {
			return nil, l.errf(id.Pos(), "assignment to the address-taken variable %s in a multiple assignment is not supported yet", id.Name)
		}
		names[i] = id.Name
	}
	return names, nil
}

// lowerMapCommaOk lowers the two-value map read v, ok := m[k], which binds the
// value and a boolean that is true when the key was present. It becomes a tuple
// assignment from the _map_lookup helper, which returns the pair, and when the
// value type is a struct or an array the bound value is cloned afterward so it is
// an independent copy the body may mutate, matching the single-value read. A
// two-value assignment whose right side is not a map index, a type assertion or a
// channel receive, waits on its own slice, so it fails loudly.
func (l *lowerer) lowerMapCommaOk(s *ast.AssignStmt) ([]ir.Stmt, error) {
	idx, ok := astUnparen(s.Rhs[0]).(*ast.IndexExpr)
	if !ok || !isMapValue(l.pkg.Info.TypeOf(idx.X)) {
		return nil, l.errf(s.Pos(), "multiple assignment is not supported yet")
	}
	valName := tupleName(s.Lhs[0])
	okName := tupleName(s.Lhs[1])
	if valName == "" || okName == "" {
		return nil, l.errf(s.Pos(), "the comma-ok read binds two plain names")
	}
	for _, t := range s.Lhs {
		if id, ok := t.(*ast.Ident); ok && l.isBoxedIdent(id) {
			return nil, l.errf(id.Pos(), "assignment to the address-taken variable %s in a comma-ok read is not supported yet", id.Name)
		}
	}
	mp := l.pkg.Info.TypeOf(idx.X).Underlying().(*types.Map)
	if err := l.checkMapKey(mp.Key(), s.Pos()); err != nil {
		return nil, err
	}
	m, err := l.lowerExpr(idx.X)
	if err != nil {
		return nil, err
	}
	key, err := l.lowerExpr(idx.Index)
	if err != nil {
		return nil, err
	}
	zero, err := l.zeroValueOfType(mp.Elem(), s.Pos())
	if err != nil {
		return nil, err
	}
	out := []ir.Stmt{&ir.TupleAssign{
		Names:  []string{valName, okName},
		Value:  &ir.Intrinsic{Name: "_map_lookup", Args: []ir.Expr{m, key, zero}},
		Define: s.Tok == token.DEFINE,
	}}
	if valName != "_" {
		if clone := l.cloneForType(mp.Elem(), &ir.Ident{Name: valName}); clone != nil {
			out = append(out, &ir.AssignStmt{Name: valName, Value: clone})
		}
	}
	return out, nil
}

// tupleName returns the name of a comma-ok target, or the empty string when the
// target is not a plain identifier, which the caller rejects.
func tupleName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
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
	inner := &ir.BinaryExpr{Op: spec.op, X: l.identRead(name), Y: y}
	value := l.narrow(s.Lhs[0], spec.tok, inner)
	if l.isBoxedIdent(name) {
		return []ir.Stmt{&ir.DerefSet{Ptr: &ir.Ident{Name: name.Name}, Value: value}}, nil
	}
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
	inner := &ir.BinaryExpr{Op: op, X: l.identRead(name), Y: &ir.IntLit{Text: "1"}}
	value := l.narrow(s.X, tok, inner)
	if l.isBoxedIdent(name) {
		return []ir.Stmt{&ir.DerefSet{Ptr: &ir.Ident{Name: name.Name}, Value: value}}, nil
	}
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
		case *ir.RangeMap:
			// A range over a map advances on its own like a for-in-range, so a
			// continue to it owes no step.
			out = unwindLoop(out, s, &s.Body, s.Label, nil, innerLabel, innerStep, escapes, used)
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
	var cond ir.Expr
	var pre []ir.Stmt
	// errors.As is comma-ok with a side effect, so as the sole condition it spills
	// the value and ok temporaries above the if and the condition reads the ok.
	if call, ok := l.errorsAsCall(s.Cond); ok {
		p, okExpr, err := l.lowerErrorsAsCond(call)
		if err != nil {
			return nil, err
		}
		pre, cond = p, okExpr
	} else {
		c, err := l.lowerExpr(s.Cond)
		if err != nil {
			return nil, err
		}
		// A closure in the condition hoists above the whole if, not into a branch.
		cond, pre = c, l.flushPendingDefs()
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
	return append(pre, out), nil
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
		// A closure in the tag hoists above the switch, before the tag temporary.
		pre = append(pre, l.flushPendingDefs()...)
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
		out = append(out, l.flushPendingDefs()...)
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
	// A three-clause loop declares its variable with the init, so under Go 1.22 a
	// closure in the body captures a fresh per-iteration copy; register it so the
	// closure lowering freezes it. The while and sugar paths lower the body under
	// the same registration.
	undo := l.snapshotLoopVars(s.Pos(), forLoopVars(s)...)
	defer undo()
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
		pre = append(pre, l.flushPendingDefs()...)
		pre = append(pre, init...)
	}
	var cond ir.Expr
	if s.Cond != nil {
		c, err := l.lowerExpr(s.Cond)
		if err != nil {
			return nil, err
		}
		// A closure in the condition hoists above the loop, not into its body.
		pre = append(pre, l.flushPendingDefs()...)
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
	// Under the pre-1.22 shared-variable semantics a closure that captures the loop
	// variable sees the value it holds after the loop, which the while form leaves
	// one past the bound; a for-in-range would instead stop at the bound, so fall
	// back to the while form to keep the shared final value faithful.
	if !l.newLoopSemantics(s.Pos()) && loopVarCaptured(s.Body, v.Name) {
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
	// A range that declares its key and value with := gives each iteration a fresh
	// pair under Go 1.22, so a closure in the body freezes them; register them for
	// the body lowering the sub-paths share.
	if s.Tok == token.DEFINE {
		undo := l.snapshotLoopVars(s.Pos(), identOf(s.Key), identOf(s.Value))
		defer undo()
	}
	srcType := l.pkg.Info.TypeOf(s.X)
	if mp, ok := srcType.Underlying().(*types.Map); ok {
		return l.lowerRangeMap(s, mp, label)
	}
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

// lowerRangeMap lowers a range over a map to a RangeMap, which the emitter turns
// into a for over a snapshot of the map's keys or key-value pairs. Go visits the
// entries in a random order and lets the body delete an entry it has not reached,
// so the snapshot, taken by the shim helper before the loop, makes a delete during
// the range safe the way Python's live-dict iteration is not. A key-only range
// binds the key, a two-variable range binds the key and the value, and a blank in
// either position drops that binding. Each iteration binds a fresh copy of a
// struct or array key or value, so the body may mutate it without reaching back
// into the map, matching Go.
func (l *lowerer) lowerRangeMap(s *ast.RangeStmt, mp *types.Map, label string) ([]ir.Stmt, error) {
	if err := l.checkMapKey(mp.Key(), s.Pos()); err != nil {
		return nil, err
	}
	src, err := l.lowerExpr(s.X)
	if err != nil {
		return nil, err
	}
	keyName := rangeIdent(s.Key)
	valName := rangeIdent(s.Value)
	body, err := l.lowerLoopBlock(s.Body, label)
	if err != nil {
		return nil, err
	}
	// The per-iteration copies are prepended to the body, so a struct value the
	// body mutates is its own copy, not the one the map still holds.
	var pre []ir.Stmt
	if valName != "" {
		if clone := l.cloneForType(mp.Elem(), &ir.Ident{Name: valName}); clone != nil {
			pre = append(pre, &ir.AssignStmt{Name: valName, Value: clone})
		}
	}
	if keyName != "" {
		if clone := l.cloneForType(mp.Key(), &ir.Ident{Name: keyName}); clone != nil {
			pre = append(pre, &ir.AssignStmt{Name: keyName, Value: clone})
		}
	}
	body = append(pre, body...)
	return []ir.Stmt{&ir.RangeMap{Key: keyName, Value: valName, Source: src, Body: body, Label: label}}, nil
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
		case "nil":
			return l.nilFor(e)
		default:
			return l.identRead(e), nil
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
	case *ast.StarExpr:
		return l.lowerDeref(e)
	case *ast.CompositeLit:
		return l.lowerCompositeLit(e)
	case *ast.CallExpr:
		return l.lowerCall(e)
	case *ast.FuncLit:
		return l.lowerFuncLit(e, false)
	default:
		return nil, l.errf(e.Pos(), "expression %T is not supported yet", e)
	}
}

// lowerSelector lowers a selector used as a value. A field read s.field becomes a
// Python attribute access, a method value recv.M binds the receiver, and a method
// expression T.M yields the unbound class function. A promoted field or method
// waits on the embedding slice, and a package member such as a constant waits on
// its own slice. The copy a struct-valued field read owes is injected by the
// caller through copyIfValueRead, not here, so a field read that is only addressed
// or compared is not needlessly cloned.
func (l *lowerer) lowerSelector(e *ast.SelectorExpr) (ir.Expr, error) {
	selection, ok := l.pkg.Info.Selections[e]
	if !ok {
		return nil, l.errf(e.Pos(), "selector %s is not supported yet", e.Sel.Name)
	}
	switch selection.Kind() {
	case types.FieldVal:
		x, err := l.lowerExpr(e.X)
		if err != nil {
			return nil, err
		}
		// A direct field is a single-step path; a promoted field steps through the
		// embedded slots go/types resolved, so u.ID reads u.Base.ID at emit time with
		// no runtime delegation, matching the type checker's selection.
		return l.fieldChain(x, l.pkg.Info.TypeOf(e.X), selection.Index()), nil
	case types.MethodVal:
		return l.lowerMethodValue(e, selection)
	case types.MethodExpr:
		return l.lowerMethodExpr(e, selection)
	default:
		return nil, l.errf(e.Pos(), "selector %s is not supported yet", e.Sel.Name)
	}
}

// lowerMethodValue lowers a method value recv.M to a bound method. A value receiver
// is snapshotted with a copy at the point the value is taken, matching Go's copy of
// the receiver into the value, so a later mutation of the original is not observed;
// a pointer receiver keeps the shared instance. A promoted method value through an
// embedded field waits on the embedding slice.
func (l *lowerer) lowerMethodValue(e *ast.SelectorExpr, selection *types.Selection) (ir.Expr, error) {
	if len(selection.Index()) > 1 {
		return nil, l.errf(e.Pos(), "a promoted method value %s is not supported yet", e.Sel.Name)
	}
	sig, ok := methodSig(selection)
	if !ok {
		return nil, l.errf(e.Pos(), "method value %s is not supported yet", e.Sel.Name)
	}
	recv, err := l.lowerExpr(e.X)
	if err != nil {
		return nil, err
	}
	_, ptr := sig.Recv().Type().(*types.Pointer)
	return &ir.MethodValue{Recv: recv, Name: e.Sel.Name, Copy: !ptr}, nil
}

// lowerMethodExpr lowers a method expression T.M to the unbound class function
// whose first argument is the receiver. A value receiver carries a copy obligation
// on that first argument, so the emitter wraps the call in a lambda that snapshots
// it; a pointer receiver takes the instance directly. Only a method on a struct is
// modeled, so a method expression on another named type is diagnosed.
func (l *lowerer) lowerMethodExpr(e *ast.SelectorExpr, selection *types.Selection) (ir.Expr, error) {
	if len(selection.Index()) > 1 {
		return nil, l.errf(e.Pos(), "a promoted method expression %s is not supported yet", e.Sel.Name)
	}
	sig, ok := methodSig(selection)
	if !ok {
		return nil, l.errf(e.Pos(), "method expression %s is not supported yet", e.Sel.Name)
	}
	name, ok := receiverClassName(sig)
	if !ok {
		return nil, l.errf(e.Pos(), "a method expression on non-struct type %s is not supported yet", e.Sel.Name)
	}
	_, ptr := sig.Recv().Type().(*types.Pointer)
	return &ir.MethodExpr{Recv: name, Name: e.Sel.Name, ValueCopy: !ptr}, nil
}

// methodSig returns the signature of the method a selection resolves to, with its
// receiver, or false when the selection is not a method with a receiver.
func methodSig(selection *types.Selection) (*types.Signature, bool) {
	method, ok := selection.Obj().(*types.Func)
	if !ok {
		return nil, false
	}
	sig, ok := method.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return nil, false
	}
	return sig, true
}

// receiverClassName returns the generated class name for a method's receiver, which
// is the struct type's name seen through a pointer receiver, or false when the
// receiver is not a struct and so has no generated class.
func receiverClassName(sig *types.Signature) (string, bool) {
	t := sig.Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return "", false
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return "", false
	}
	return named.Obj().Name(), true
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
	if mp, ok := t.Underlying().(*types.Map); ok {
		return l.lowerMapLit(e, mp)
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
// of its elements. Positional, index-keyed, and mixed forms are all supported
// through lowerIndexedElements. A partial literal is padded with zero-value
// elements to the array length, matching Go, and a struct or array element that
// reads an existing value is cloned so the literal stores an independent copy.
// Each padded zero is built separately, so two zero elements never alias.
func (l *lowerer) lowerArrayLit(e *ast.CompositeLit, arr *types.Array) (ir.Expr, error) {
	elems, err := l.lowerIndexedElements(e.Elts, arr.Elem(), e.Pos())
	if err != nil {
		return nil, err
	}
	for i := len(elems); i < int(arr.Len()); i++ {
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
// slice literal's length is exactly the span its elements reach, and a value
// element that reads an existing value is cloned so the backing owns an
// independent copy. Positional, index-keyed, and mixed forms are all supported.
func (l *lowerer) lowerSliceLit(e *ast.CompositeLit) (ir.Expr, error) {
	elem := l.pkg.Info.TypeOf(e).Underlying().(*types.Slice).Elem()
	elems, err := l.lowerIndexedElements(e.Elts, elem, e.Pos())
	if err != nil {
		return nil, err
	}
	return &ir.SliceLit{Elems: elems}, nil
}

// lowerIndexedElements places the elements of an array or slice composite
// literal into a dense list, honoring Go's index rule: a keyed element sets the
// running index to its constant key, a positional element takes the running
// index, and the index advances by one after each element. A gap left between
// two indices is filled with a freshly built zero value so two zero elements
// never alias, and a struct or array value that reads an existing value is
// cloned so the backing owns an independent copy. The span returned reaches the
// highest index written plus one, which for a slice is its length.
func (l *lowerer) lowerIndexedElements(elts []ast.Expr, elem types.Type, pos token.Pos) ([]ir.Expr, error) {
	placed := map[int]ir.Expr{}
	maxIndex := -1
	idx := 0
	for _, elt := range elts {
		valueExpr := elt
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			n, err := l.constIndex(kv.Key)
			if err != nil {
				return nil, err
			}
			idx = n
			valueExpr = kv.Value
		}
		value, err := l.lowerExpr(valueExpr)
		if err != nil {
			return nil, err
		}
		placed[idx] = l.copyIfValueRead(valueExpr, value)
		if idx > maxIndex {
			maxIndex = idx
		}
		idx++
	}
	elems := make([]ir.Expr, 0, maxIndex+1)
	for i := 0; i <= maxIndex; i++ {
		if v, ok := placed[i]; ok {
			elems = append(elems, v)
			continue
		}
		zero, err := l.zeroValueOfType(elem, pos)
		if err != nil {
			return nil, err
		}
		elems = append(elems, zero)
	}
	return elems, nil
}

// constIndex reads the constant integer value of a composite-literal element
// key. Go requires the key of an array or slice literal element to be a constant
// index, so a non-constant key is diagnosed rather than emitted.
func (l *lowerer) constIndex(key ast.Expr) (int, error) {
	tv, ok := l.pkg.Info.Types[key]
	if !ok || tv.Value == nil {
		return 0, l.errf(key.Pos(), "composite literal index must be a constant")
	}
	n, ok := constant.Int64Val(constant.ToInt(tv.Value))
	if !ok {
		return 0, l.errf(key.Pos(), "composite literal index is out of range")
	}
	return int(n), nil
}

// lowerMapLit lowers a map composite literal to a MapLit, a Python dict of its
// entries. A map literal has only the keyed form, so every element is a key-value
// pair. A struct key or a struct or array value that reads an existing value is
// cloned, so the dict owns independent copies the way Go stores independent keys
// and values.
func (l *lowerer) lowerMapLit(e *ast.CompositeLit, mp *types.Map) (ir.Expr, error) {
	if err := l.checkMapKey(mp.Key(), e.Pos()); err != nil {
		return nil, err
	}
	entries := make([]ir.MapEntry, 0, len(e.Elts))
	for _, elt := range e.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, l.errf(elt.Pos(), "a map literal element must be a key-value pair")
		}
		key, err := l.lowerExpr(kv.Key)
		if err != nil {
			return nil, err
		}
		value, err := l.lowerExpr(kv.Value)
		if err != nil {
			return nil, err
		}
		entries = append(entries, ir.MapEntry{
			Key:   l.copyIfValueRead(kv.Key, key),
			Value: l.copyIfValueRead(kv.Value, value),
		})
	}
	return &ir.MapLit{Entries: entries}, nil
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
	x, err := l.lowerOperand(e.X, e.Y)
	if err != nil {
		return nil, err
	}
	y, err := l.lowerOperand(e.Y, e.X)
	if err != nil {
		return nil, err
	}
	op := e.Op.String()
	if l.isInterfaceNilCompare(e) {
		// An interface value is None when it is the nil interface, so the check is
		// identity against None, is or is not, not the sentinel equality a pointer or
		// slice nil uses.
		if e.Op == token.EQL {
			op = "is"
		} else {
			op = "is not"
		}
	}
	inner := &ir.BinaryExpr{Op: op, X: x, Y: y}
	return l.narrow(e, e.Op, inner), nil
}

// isInterfaceNilCompare reports whether a binary expression compares an interface
// value against nil with == or !=, the check that lowers to an identity test
// against None. One operand is the bare nil and the other has an interface type,
// which is how an error compared to nil reaches the is and is not spelling.
func (l *lowerer) isInterfaceNilCompare(e *ast.BinaryExpr) bool {
	if e.Op != token.EQL && e.Op != token.NEQ {
		return false
	}
	switch {
	case l.isNilExpr(e.X):
		return isInterfaceValue(l.pkg.Info.TypeOf(e.Y))
	case l.isNilExpr(e.Y):
		return isInterfaceValue(l.pkg.Info.TypeOf(e.X))
	default:
		return false
	}
}

// lowerOperand lowers one side of a binary expression, resolving a bare nil to the
// sentinel of the other operand's type. A comparison p == nil is the one place nil
// takes its type from context in this slice, since the untyped nil has none of its
// own and the typed operand supplies it.
func (l *lowerer) lowerOperand(e, other ast.Expr) (ir.Expr, error) {
	if l.isNilExpr(e) {
		if t := l.pkg.Info.TypeOf(other); t != nil && !isUntypedNil(t) {
			return l.nilSentinel(t, e.Pos())
		}
	}
	return l.lowerExpr(e)
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
	case *types.Map:
		return l.lowerMapIndex(e, u)
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

// lowerMapIndex lowers a single-value map read m[k] to the _map_index intrinsic,
// which returns the stored value or, on a missing key, the value type's zero, so
// the read never fails the way a Go map read does not. A struct or array value the
// read binds is cloned by the caller through copyIfValueRead, exactly as a slice
// element read is, so it is not cloned here. The two-value comma-ok form is a
// different lowering the assignment handles, so it never reaches here.
func (l *lowerer) lowerMapIndex(e *ast.IndexExpr, mp *types.Map) (ir.Expr, error) {
	if err := l.checkMapKey(mp.Key(), e.Pos()); err != nil {
		return nil, err
	}
	m, err := l.lowerExpr(e.X)
	if err != nil {
		return nil, err
	}
	key, err := l.lowerExpr(e.Index)
	if err != nil {
		return nil, err
	}
	zero, err := l.zeroValueOfType(mp.Elem(), e.Pos())
	if err != nil {
		return nil, err
	}
	return &ir.Intrinsic{Name: "_map_index", Args: []ir.Expr{m, key, zero}}, nil
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
	case token.AND:
		return l.lowerAddr(e)
	default:
		return nil, l.errf(e.Pos(), "unary %s is not supported yet", e.Op)
	}
}

// lowerAddr lowers the address-of operator, &s.field and &a[i], into a pointer
// object that names the location it points at. A field address becomes a FieldPtr
// carrying the container object and the field name, and an element address becomes
// an IndexPtr carrying the sequence and the index, so a later deref reads and a
// deref-assign writes straight through to the same slot. The address of a struct
// field steps through the same embedded-field path a field read follows, so
// &u.ID resolves to &u.Base.ID when ID is promoted. Taking the address of a
// composite literal or a plain local scalar is deferred to the pointer-as-cell
// slice, so it is diagnosed rather than emitted.
func (l *lowerer) lowerAddr(e *ast.UnaryExpr) (ir.Expr, error) {
	switch operand := astUnparen(e.X).(type) {
	case *ast.SelectorExpr:
		selection, ok := l.pkg.Info.Selections[operand]
		if !ok || selection.Kind() != types.FieldVal {
			return nil, l.errf(e.Pos(), "taking the address of %s is not supported yet", operand.Sel.Name)
		}
		base, err := l.lowerExpr(operand.X)
		if err != nil {
			return nil, err
		}
		index := selection.Index()
		container := l.fieldChain(base, l.pkg.Info.TypeOf(operand.X), index[:len(index)-1])
		return &ir.AddrField{Container: container, Name: operand.Sel.Name}, nil
	case *ast.IndexExpr:
		xt := l.pkg.Info.TypeOf(operand.X)
		if !isArrayValue(xt) && !isSliceValue(xt) {
			return nil, l.errf(e.Pos(), "taking the address of an element of %s is not supported yet", xt)
		}
		seq, err := l.lowerExpr(operand.X)
		if err != nil {
			return nil, err
		}
		index, err := l.lowerExpr(operand.Index)
		if err != nil {
			return nil, err
		}
		return &ir.AddrIndex{Seq: seq, Index: index}, nil
	case *ast.CompositeLit:
		// A struct composite literal is already a reference object, so the address of
		// &T{...} is that fresh instance itself; a deref reads and writes reach it the
		// same way they would through a struct local the code took the address of.
		t := l.pkg.Info.TypeOf(operand)
		if !isStructValue(t) {
			return nil, l.errf(e.Pos(), "taking the address of a composite literal of %s is not supported yet", t)
		}
		return l.lowerCompositeLit(operand)
	case *ast.Ident:
		if l.isBoxedIdent(operand) {
			// The local is boxed into a cell, so its address is just the cell, named
			// by the same identifier every read and write of the local goes through.
			return &ir.Ident{Name: operand.Name}, nil
		}
		if l.aliased[l.pkg.Info.ObjectOf(operand)] {
			// A struct or array local is already a reference object, so its address is
			// the instance itself; a field or element write through the pointer and
			// through the local both reach that one instance.
			return l.identRead(operand), nil
		}
		return nil, l.errf(e.Pos(), "taking the address of %s is not supported yet", operand.Name)
	default:
		return nil, l.errf(e.Pos(), "taking the address of %T is not supported yet", operand)
	}
}

// lowerDeref lowers a pointer dereference read, *p, into a Deref that reads
// through the pointer object the address-of operator produced. A deref used as an
// assignment target is handled by lowerAssign, which builds a DerefSet instead so
// the write reaches the pointed-at slot.
func (l *lowerer) lowerDeref(e *ast.StarExpr) (ir.Expr, error) {
	x, err := l.lowerExpr(e.X)
	if err != nil {
		return nil, err
	}
	// A pointer to a struct or an array is the instance itself, so dereferencing it
	// yields that instance directly; only a pointer to a scalar lives in a cell and
	// reads through its get.
	if p, ok := l.pkg.Info.TypeOf(e.X).Underlying().(*types.Pointer); ok {
		if isStructValue(p.Elem()) || isArrayValue(p.Elem()) {
			return x, nil
		}
	}
	return &ir.Deref{X: x}, nil
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
	switch fun := astUnparen(e.Fun).(type) {
	case *ast.FuncLit:
		// An immediately invoked function literal has no name to bind, and a def
		// cannot be an expression, so it hoists to a def that the call then names.
		callee, err := l.lowerFuncLit(fun, true)
		if err != nil {
			return nil, err
		}
		args, err := l.lowerCallArgs(e.Args)
		if err != nil {
			return nil, err
		}
		return &ir.CallExpr{Name: callee.(*ir.Ident).Name, Args: args}, nil
	case *ast.SelectorExpr:
		if l.isFmtPrintln(fun) {
			args, err := l.lowerPrintlnArgs(e.Args)
			if err != nil {
				return nil, err
			}
			return &ir.Intrinsic{Name: "println", Args: args}, nil
		}
		if name, ok := l.pkgFunc(fun, "errors"); ok {
			return l.lowerErrorsFunc(e, name)
		}
		if name, ok := l.pkgFunc(fun, "fmt"); ok && name == "Errorf" {
			return l.lowerErrorf(e)
		}
		if sel, ok := l.pkg.Info.Selections[fun]; ok {
			switch sel.Kind() {
			case types.MethodVal:
				return l.lowerMethodCall(e, fun, sel)
			case types.MethodExpr:
				return l.lowerMethodExprCall(e, fun, sel)
			}
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
// lowerMethodCall lowers a method call recv.M(args) to a MethodCall on the lowered
// receiver. A value-receiver method operates on a copy of the receiver, so the
// receiver is cloned at the call: a value read of a struct clones through the same
// ident-or-field discriminator as any value copy, and a call through a pointer
// clones the pointed-to struct since Go copies it on the implicit dereference. A
// pointer-receiver method operates on the instance, so the receiver passes through
// directly, which is also how Go's automatic address-of a value receiver lowers,
// since the struct instance already is the reference the pointer would name. A
// promoted method reached through an embedded field waits on the embedding slice.
func (l *lowerer) lowerMethodCall(e *ast.CallExpr, sel *ast.SelectorExpr, selection *types.Selection) (ir.Expr, error) {
	if len(selection.Index()) > 1 {
		return nil, l.errf(e.Pos(), "a promoted method call %s is not supported yet", sel.Sel.Name)
	}
	recv, err := l.lowerExpr(sel.X)
	if err != nil {
		return nil, err
	}
	method, ok := selection.Obj().(*types.Func)
	if !ok {
		return nil, l.errf(e.Pos(), "method %s is not supported yet", sel.Sel.Name)
	}
	sig, ok := method.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return nil, l.errf(e.Pos(), "method %s is not supported yet", sel.Sel.Name)
	}
	if _, ptr := sig.Recv().Type().(*types.Pointer); !ptr {
		// A value receiver takes a copy. When the receiver expression is a pointer,
		// Go copies the pointed-to struct on the automatic dereference, so the
		// instance is cloned unconditionally; otherwise the shared value-copy
		// discriminator clones a named read and leaves a fresh value alone.
		if _, ptrRecv := l.pkg.Info.TypeOf(sel.X).(*types.Pointer); ptrRecv {
			recv = &ir.Clone{X: recv}
		} else {
			recv = l.copyIfValueRead(sel.X, recv)
		}
	}
	args, err := l.lowerCallArgs(e.Args)
	if err != nil {
		return nil, err
	}
	return &ir.MethodCall{Recv: recv, Name: sel.Sel.Name, Args: args}, nil
}

// lowerMethodExprCall lowers an immediately called method expression T.M(recv,
// args) to the same MethodCall as recv.M(args), since a method expression is a
// method call with the receiver passed as the first argument. A value receiver
// copies that first argument exactly as a value-receiver method call copies its
// receiver, and a pointer receiver passes the instance. A promoted method
// expression waits on the embedding slice.
func (l *lowerer) lowerMethodExprCall(e *ast.CallExpr, sel *ast.SelectorExpr, selection *types.Selection) (ir.Expr, error) {
	if len(selection.Index()) > 1 {
		return nil, l.errf(e.Pos(), "a promoted method expression %s is not supported yet", sel.Sel.Name)
	}
	sig, ok := methodSig(selection)
	if !ok || len(e.Args) == 0 {
		return nil, l.errf(e.Pos(), "method expression %s is not supported yet", sel.Sel.Name)
	}
	recv, err := l.lowerExpr(e.Args[0])
	if err != nil {
		return nil, err
	}
	if _, ptr := sig.Recv().Type().(*types.Pointer); !ptr {
		if _, ptrRecv := l.pkg.Info.TypeOf(e.Args[0]).(*types.Pointer); ptrRecv {
			recv = &ir.Clone{X: recv}
		} else {
			recv = l.copyIfValueRead(e.Args[0], recv)
		}
	}
	args, err := l.lowerCallArgs(e.Args[1:])
	if err != nil {
		return nil, err
	}
	return &ir.MethodCall{Recv: recv, Name: sel.Sel.Name, Args: args}, nil
}

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
	case "delete":
		return l.lowerDelete(e)
	case "clear":
		return l.lowerClear(e)
	case "recover":
		if len(e.Args) != 0 {
			return nil, l.errf(e.Pos(), "recover takes no arguments")
		}
		return &ir.Intrinsic{Name: "_recover"}, nil
	default:
		return nil, l.errf(e.Pos(), "builtin %s is not supported yet", fun.Name)
	}
}

// lowerDelete lowers delete(m, k) to the _map_delete intrinsic, which removes the
// key when present and does nothing otherwise, including on a nil map, matching
// Go's delete. The key is read, not stored, so it is not cloned.
func (l *lowerer) lowerDelete(e *ast.CallExpr) (ir.Expr, error) {
	if len(e.Args) != 2 {
		return nil, l.errf(e.Pos(), "delete takes a map and a key")
	}
	if !isMapValue(l.pkg.Info.TypeOf(e.Args[0])) {
		return nil, l.errf(e.Pos(), "delete from %s is not supported yet", l.pkg.Info.TypeOf(e.Args[0]))
	}
	m, err := l.lowerExpr(e.Args[0])
	if err != nil {
		return nil, err
	}
	key, err := l.lowerExpr(e.Args[1])
	if err != nil {
		return nil, err
	}
	return &ir.Intrinsic{Name: "_map_delete", Args: []ir.Expr{m, key}}, nil
}

// lowerClear lowers clear(m) on a map to the _map_clear intrinsic, which removes
// every entry and is a no-op on a nil map. clear of a slice zeroes its elements
// in place, a different operation that waits on its own lowering, so it fails
// loudly here.
func (l *lowerer) lowerClear(e *ast.CallExpr) (ir.Expr, error) {
	if len(e.Args) != 1 {
		return nil, l.errf(e.Pos(), "clear takes one argument")
	}
	if !isMapValue(l.pkg.Info.TypeOf(e.Args[0])) {
		return nil, l.errf(e.Pos(), "clear of %s is not supported yet", l.pkg.Info.TypeOf(e.Args[0]))
	}
	m, err := l.lowerExpr(e.Args[0])
	if err != nil {
		return nil, err
	}
	return &ir.Intrinsic{Name: "_map_clear", Args: []ir.Expr{m}}, nil
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
	if mp, ok := t.Underlying().(*types.Map); ok {
		// make of a map builds an empty dict; the optional size argument is only a
		// capacity hint, which a Python dict has no use for, so it is dropped.
		if err := l.checkMapKey(mp.Key(), e.Pos()); err != nil {
			return nil, err
		}
		return &ir.MapLit{}, nil
	}
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

// pkgFunc reports whether a selector names a function from the standard package
// at the given import path, checked through the type information rather than by
// name so a local value named like the package cannot masquerade as it, and
// returns the selected function name when it does.
func (l *lowerer) pkgFunc(sel *ast.SelectorExpr, path string) (string, bool) {
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	pkgName, ok := l.pkg.Info.Uses[ident].(*types.PkgName)
	if !ok {
		return "", false
	}
	if pkgName.Imported().Path() != path {
		return "", false
	}
	return sel.Sel.Name, true
}

// lowerErrorsFunc lowers a call to the standard errors package. New builds a
// string-backed value, Unwrap and Is walk the error chain, and Join wraps the
// non-nil arguments, each routing to the runtime intrinsic that carries the
// value-world semantics doc 10 fixes. As is comma-ok over a pointer target and
// waits on the next slice, and any other errors function fails loudly.
func (l *lowerer) lowerErrorsFunc(e *ast.CallExpr, name string) (ir.Expr, error) {
	switch name {
	case "New":
		return l.lowerErrorsCall(e, "errors_new")
	case "Unwrap":
		return l.lowerErrorsCall(e, "errors_unwrap")
	case "Is":
		return l.lowerErrorsCall(e, "errors_is")
	case "Join":
		if e.Ellipsis != token.NoPos {
			return nil, l.errf(e.Pos(), "errors.Join with a spread argument is not supported yet")
		}
		return l.lowerErrorsCall(e, "errors_join")
	case "As":
		// errors.As assigns its target, so it is not an ordinary value expression
		// and is handled where a statement can carry that side effect.
		return nil, l.errf(e.Pos(), "errors.As is only supported as an if condition, a boolean binding, or a statement")
	default:
		return nil, l.errf(e.Pos(), "errors.%s is not supported yet", name)
	}
}

// errorsAsCall reports whether an expression is a call to errors.As, and returns
// the call, so the statement contexts that can carry its target side effect
// recognize it before it reaches the value-expression path.
func (l *lowerer) errorsAsCall(e ast.Expr) (*ast.CallExpr, bool) {
	call, ok := astUnparen(e).(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	sel, ok := astUnparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	name, ok := l.pkgFunc(sel, "errors")
	if !ok || name != "As" {
		return nil, false
	}
	return call, true
}

// lowerErrorsAsCond lowers errors.As as the sole condition of an if or for, into
// the spill statements that run before the loop or branch and the ok temporary
// the condition then reads.
func (l *lowerer) lowerErrorsAsCond(call *ast.CallExpr) ([]ir.Stmt, ir.Expr, error) {
	okName := seqName("_ok", l.errorsAsSeq)
	pre, err := l.errorsAsPre(call, okName, true)
	if err != nil {
		return nil, nil, err
	}
	return pre, &ir.Ident{Name: okName}, nil
}

// lowerErrorsAsBind lowers ok := errors.As(err, &t) or ok = errors.As(err, &t),
// binding the ok flag to the named target while the spill sets t on a match.
func (l *lowerer) lowerErrorsAsBind(s *ast.AssignStmt, call *ast.CallExpr) ([]ir.Stmt, error) {
	okName, ok := s.Lhs[0].(*ast.Ident)
	if !ok {
		return nil, l.errf(s.Lhs[0].Pos(), "the errors.As result binds a plain name")
	}
	// A discarded ok, _ = errors.As(...), still needs a readable flag for the
	// guarded target write, so it spills to a temporary the way the bare form does.
	if okName.Name == "_" {
		return l.errorsAsPre(call, seqName("_ok", l.errorsAsSeq), true)
	}
	if l.isBoxedIdent(okName) {
		return nil, l.errf(okName.Pos(), "binding errors.As to the address-taken variable %s is not supported yet", okName.Name)
	}
	return l.errorsAsPre(call, okName.Name, s.Tok == token.DEFINE)
}

// errorsAsPre builds the statements an errors.As call spills to: a comma-ok read
// from the errors_as intrinsic that binds a value temporary and the ok flag, then
// a guarded write of the value into the target so the target changes only on a
// match, the way Go leaves it untouched when As returns false. The target must be
// the address of a plain variable whose type is a pointer to a named struct, the
// concrete error the runtime matches with isinstance, so an interface target or a
// non-addressable second argument fails loudly.
func (l *lowerer) errorsAsPre(call *ast.CallExpr, okName string, defineOk bool) ([]ir.Stmt, error) {
	if call.Ellipsis != token.NoPos || len(call.Args) != 2 {
		return nil, l.errf(call.Pos(), "errors.As takes an error and a pointer to a target")
	}
	addr, ok := astUnparen(call.Args[1]).(*ast.UnaryExpr)
	if !ok || addr.Op != token.AND {
		return nil, l.errf(call.Args[1].Pos(), "errors.As needs the address of a target variable")
	}
	tgt, ok := astUnparen(addr.X).(*ast.Ident)
	if !ok {
		return nil, l.errf(addr.X.Pos(), "errors.As needs the address of a target variable")
	}
	if l.isBoxedIdent(tgt) {
		return nil, l.errf(tgt.Pos(), "errors.As into the address-taken variable %s is not supported yet", tgt.Name)
	}
	className, err := l.errorTargetClass(tgt)
	if err != nil {
		return nil, err
	}
	errExpr, err := l.lowerExpr(call.Args[0])
	if err != nil {
		return nil, err
	}
	valName := seqName("_as", l.errorsAsSeq)
	l.errorsAsSeq++
	pre := l.flushPendingDefs()
	pre = append(pre,
		&ir.TupleAssign{
			Names:  []string{valName, okName},
			Value:  &ir.Intrinsic{Name: "errors_as", Args: []ir.Expr{errExpr, &ir.Ident{Name: className}}},
			Define: defineOk,
		},
		&ir.IfStmt{
			Cond: &ir.Ident{Name: okName},
			Then: []ir.Stmt{&ir.AssignStmt{Name: tgt.Name, Value: &ir.Ident{Name: valName}}},
		},
	)
	return pre, nil
}

// errorTargetClass returns the Python class name errors.As matches the chain
// against, taken from an errors.As target that is a pointer to a named struct.
// The compiled tier models a pointer to a struct as the struct instance itself,
// so the class is the named struct's own class. An interface target or a pointer
// to anything but a named struct waits on a later slice and fails loudly.
func (l *lowerer) errorTargetClass(tgt *ast.Ident) (string, error) {
	ptr, ok := l.pkg.Info.TypeOf(tgt).Underlying().(*types.Pointer)
	if !ok {
		return "", l.errf(tgt.Pos(), "errors.As target %s must be a pointer to a concrete error type", tgt.Name)
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		return "", l.errf(tgt.Pos(), "errors.As target %s must point to a named type", tgt.Name)
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return "", l.errf(tgt.Pos(), "errors.As target %s must point to a struct type", tgt.Name)
	}
	return named.Obj().Name(), nil
}

// lowerErrorsCall lowers an errors package call to its runtime intrinsic over the
// plainly lowered arguments. An error is a reference value, not a struct read, so
// no value copy is injected and the argument passes straight through.
func (l *lowerer) lowerErrorsCall(e *ast.CallExpr, intrinsic string) (ir.Expr, error) {
	args, err := l.lowerCallArgs(e.Args)
	if err != nil {
		return nil, err
	}
	return &ir.Intrinsic{Name: intrinsic, Args: args}, nil
}

// errorfVerbs is the set of format verbs fmt.Errorf may carry in the compiled
// tier. Every one but w and q renders exactly as go_str already does, w records
// its operand as wrapped, and q quotes its operand, so the runtime formatter
// needs no other verb. A verb outside this set, or a flag or width the set does
// not cover, fails loudly rather than formatting the Go way by luck.
const errorfVerbs = "vsdtwq"

// lowerErrorf lowers a call to fmt.Errorf. The format must be a constant string,
// checked here so the verb scan runs at compile time, and every verb must be one
// the runtime formatter admits. The lowered call carries the format as bytes and
// the plainly lowered operands, and the errorf intrinsic scans the format again
// at run time to build the message and record the %w operands.
func (l *lowerer) lowerErrorf(e *ast.CallExpr) (ir.Expr, error) {
	if e.Ellipsis != token.NoPos {
		return nil, l.errf(e.Pos(), "fmt.Errorf with a spread argument is not supported yet")
	}
	if len(e.Args) == 0 {
		return nil, l.errf(e.Pos(), "fmt.Errorf needs a format argument")
	}
	tv := l.pkg.Info.Types[e.Args[0]]
	if tv.Value == nil || tv.Value.Kind() != constant.String {
		return nil, l.errf(e.Args[0].Pos(), "fmt.Errorf needs a constant format string")
	}
	if err := l.checkErrorfFormat(e.Args[0].Pos(), constant.StringVal(tv.Value)); err != nil {
		return nil, err
	}
	args, err := l.lowerCallArgs(e.Args)
	if err != nil {
		return nil, err
	}
	return &ir.Intrinsic{Name: "errorf", Args: args}, nil
}

// checkErrorfFormat scans an Errorf format string and rejects any directive the
// runtime formatter does not carry. A doubled percent is a literal, a bare verb
// from the admitted set is fine, and anything else, a flag, a width, an unknown
// verb, or a trailing percent, fails loudly so an unsupported format never
// reaches the runtime to be mangled.
func (l *lowerer) checkErrorfFormat(pos token.Pos, format string) error {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i >= len(format) {
			return l.errf(pos, "fmt.Errorf format ends with a lone %%")
		}
		if format[i] == '%' {
			continue
		}
		if !strings.ContainsRune(errorfVerbs, rune(format[i])) {
			return l.errf(pos, "fmt.Errorf format directive %%%c is not supported yet", format[i])
		}
	}
	return nil
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
