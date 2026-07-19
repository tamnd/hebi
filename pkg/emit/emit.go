// Package emit lowers a verified IR module to the source of one Python module.
//
// The emitter is a single deterministic pass: the same module always produces
// byte-identical source, with no map iteration or other unordered state leaking
// into the output. Emitted modules import the runtime shim only when they use
// it, so the result is ruff-clean with no unused import. At M0 the emitter
// covers the hello-scale IR: functions whose bodies assign, branch, loop, and
// call over integer, string, and boolean literals and binary operators.
package emit

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tamnd/hebi/pkg/ir"
	"github.com/tamnd/hebi/pkg/shim"
)

// binOps maps the Go operator text carried in the IR to its Python spelling.
// The arithmetic and comparison operators are shared, and the logical
// operators become the and and or keywords. Operators whose semantics differ
// between the languages, such as integer division, are left out on purpose so
// an unhandled one is a hard error rather than a silent mistranslation.
var binOps = map[string]string{
	"+":  "+",
	"-":  "-",
	"*":  "*",
	"<":  "<",
	">":  ">",
	"<=": "<=",
	">=": ">=",
	"==": "==",
	"!=": "!=",
	"&&": "and",
	"||": "or",
	"<<": "<<",
	">>": ">>",
	// Floor division and remainder, emitted only for an unsigned operand, whose
	// non-negative value makes Python's // and % agree with Go's truncation.
	"//": "//",
	"%":  "%",
	// Identity for the interface-to-nil check: an interface value is None when it
	// is the nil interface, so err == nil lowers to err is None and err != nil to
	// err is not None, the faithful analogue of Go's nil interface identity test.
	"is":     "is",
	"is not": "is not",
}

// unaryOps maps the Go unary operator text to its Python spelling. Negation and
// the no-op unary plus are shared; the growing case, negation, is masked by the
// lowering, not here.
var unaryOps = map[string]string{
	"-": "-",
	"+": "+",
	"!": "not",
}

// isWordOperator reports whether a Python operator is spelled as a keyword, such
// as not, and so needs a space before its operand where a symbol operator does
// not.
func isWordOperator(op string) bool {
	last := op[len(op)-1]
	return last >= 'a' && last <= 'z'
}

// maskHelper names the runtime helper for a width and signedness, matching the
// definitions in the shim.
func maskHelper(bits int, signed bool) string {
	if signed {
		return fmt.Sprintf("_i%d", bits)
	}
	return fmt.Sprintf("_u%d", bits)
}

// Module lowers a verified IR module to the source of one Python module. It
// assumes the module has already passed ir.Verify, so structural problems are
// not re-checked here; it returns an error only for surface the emitter does
// not yet support.
func Module(m *ir.Module) (string, error) {
	var b strings.Builder
	imports := false
	if len(m.Interfaces) > 0 {
		// A module with any interface declares Protocol classes, so it imports the
		// Protocol base and its runtime-checkable decorator from typing, a standard
		// library import that sorts before the first-party runtime shim.
		b.WriteString("from typing import Protocol, runtime_checkable\n")
		imports = true
	}
	if usesShim(m) {
		if imports {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "import %s\n", shim.Name)
		imports = true
	}
	if imports {
		b.WriteString("\n\n")
	}
	// Interfaces emit first as Protocol classes, then structs as classes, both in
	// source order, so a function or class that names one refers to a name already
	// bound.
	wrote := false
	for _, id := range m.Interfaces {
		if wrote {
			b.WriteString("\n\n")
		}
		emitInterface(&b, id)
		wrote = true
	}
	for _, sd := range m.Structs {
		if wrote {
			b.WriteString("\n\n")
		}
		if err := emitStruct(&b, m.Package, sd); err != nil {
			return "", err
		}
		wrote = true
	}
	hasMain := false
	for _, fn := range m.Funcs {
		if wrote {
			b.WriteString("\n\n")
		}
		if err := emitFunc(&b, fn); err != nil {
			return "", err
		}
		wrote = true
		if fn.Name == "main" {
			hasMain = true
		}
	}
	if hasMain {
		if moduleUnwinds(m) {
			// A panic that runs off the top of main crashes the Go way: the guard
			// catches the escaping GoPanic and prints Go's banner before exiting with
			// status 2, rather than letting Python print its own traceback. It is
			// emitted only for a module that panics or defers, since only those raise a
			// GoPanic that can reach here.
			b.WriteString("\n\nif __name__ == \"__main__\":\n")
			b.WriteString("    try:\n        main()\n")
			fmt.Fprintf(&b, "    except %s.GoPanic as _p:\n        %s._crash(_p)\n", shim.Name, shim.Name)
		} else {
			b.WriteString("\n\nif __name__ == \"__main__\":\n    main()\n")
		}
	}
	return b.String(), nil
}

// moduleUnwinds reports whether the module contains a panic or a defer anywhere,
// the constructs that raise or propagate a GoPanic, so the entry guard that turns
// an escaping panic into Go's crash surface is emitted only when one can arise.
func moduleUnwinds(m *ir.Module) bool {
	for _, sd := range m.Structs {
		for _, method := range sd.Methods {
			if blockUnwinds(method.Body) {
				return true
			}
		}
	}
	for _, fn := range m.Funcs {
		if blockUnwinds(fn.Body) {
			return true
		}
	}
	return false
}

func blockUnwinds(body []ir.Stmt) bool {
	return slices.ContainsFunc(body, stmtUnwinds)
}

// stmtUnwinds reports whether a statement can raise or propagate a GoPanic, either
// because it is a panic or a defer node, or because one of its expressions can
// panic while it is evaluated, which a one-result type assertion does.
func stmtUnwinds(s ir.Stmt) bool {
	switch s := s.(type) {
	case *ir.Panic, *ir.DeferBlock, *ir.DeferPush, *ir.DeferReturn:
		return true
	case *ir.ExprStmt:
		return exprUnwinds(s.X)
	case *ir.ReturnStmt:
		return exprUnwinds(s.Value)
	case *ir.AssignStmt:
		return exprUnwinds(s.Value)
	case *ir.TupleAssign:
		return exprUnwinds(s.Value)
	case *ir.SetField:
		return exprUnwinds(s.Object) || exprUnwinds(s.Value)
	case *ir.SetIndex:
		return exprUnwinds(s.Object) || exprUnwinds(s.Index) || exprUnwinds(s.Value)
	case *ir.DerefSet:
		return exprUnwinds(s.Ptr) || exprUnwinds(s.Value)
	case *ir.IfStmt:
		return exprUnwinds(s.Cond) || blockUnwinds(s.Then) || blockUnwinds(s.Else)
	case *ir.ForStmt:
		return exprUnwinds(s.Cond) || blockUnwinds(s.Body)
	case *ir.ForRange:
		return exprUnwinds(s.Start) || exprUnwinds(s.Stop) || exprUnwinds(s.Step) || blockUnwinds(s.Body)
	case *ir.RangeMap:
		return exprUnwinds(s.Source) || blockUnwinds(s.Body)
	case *ir.RangeString:
		return exprUnwinds(s.Source) || blockUnwinds(s.Body)
	case *ir.FuncDef:
		return blockUnwinds(s.Body)
	}
	return false
}

// panicIntrinsics names the runtime intrinsics that raise a GoPanic on their own,
// so an expression carrying one can crash the Go way even when its arguments cannot.
// The one-result type assertion panics when the interface does not hold the asserted
// type, and integer division and remainder panic on a zero divisor; the comma-ok
// _type_assert_ok and the float _fdiv never panic. A send on a closed channel and a
// close of a closed or nil channel panic, so chan_send and chan_close carry the
// crash the guard must account for; a receive never panics. Among the sync
// operations, driving a WaitGroup counter negative raises a recoverable panic and a
// once function may panic, so waitgroup_add, waitgroup_done, and once_do join the
// set; an unlock of an unheld lock is a Go fatal error, not a panic, so it exits on
// its own and needs no guard, and the pure lock, wait, and try operations never panic.
var panicIntrinsics = map[string]bool{
	"_type_assert":   true,
	"_idiv":          true,
	"_imod":          true,
	"_quo":           true,
	"chan_send":      true,
	"chan_close":     true,
	"select":         true,
	"waitgroup_add":  true,
	"waitgroup_done": true,
	"once_do":        true,
}

// exprUnwinds reports whether evaluating an expression can raise a GoPanic. A
// handful of runtime intrinsics panic on their own, listed in panicIntrinsics, and
// every other form only recurses into its sub-expressions to find one that can.
func exprUnwinds(e ir.Expr) bool {
	switch e := e.(type) {
	case *ir.Intrinsic:
		return panicIntrinsics[e.Name] || argsUnwind(e.Args)
	case *ir.BinaryExpr:
		return exprUnwinds(e.X) || exprUnwinds(e.Y)
	case *ir.UnaryExpr:
		return exprUnwinds(e.X)
	case *ir.Mask:
		return exprUnwinds(e.X)
	case *ir.Convert:
		return exprUnwinds(e.X)
	case *ir.IndexExpr:
		return exprUnwinds(e.X) || exprUnwinds(e.Index)
	case *ir.CallExpr:
		return argsUnwind(e.Args)
	case *ir.MethodCall:
		return exprUnwinds(e.Recv) || argsUnwind(e.Args)
	case *ir.MethodValue:
		return exprUnwinds(e.Recv)
	case *ir.FieldAccess:
		return exprUnwinds(e.X)
	case *ir.Deref:
		return exprUnwinds(e.X)
	case *ir.Tuple:
		return argsUnwind(e.Elems)
	case *ir.Lambda:
		return exprUnwinds(e.Body)
	case *ir.Clone:
		return exprUnwinds(e.X)
	case *ir.ArrayZero:
		return exprUnwinds(e.Elem)
	case *ir.ArrayLit:
		return argsUnwind(e.Elems)
	case *ir.ArrayClone:
		return exprUnwinds(e.X)
	case *ir.MapLit:
		for _, en := range e.Entries {
			if exprUnwinds(en.Key) || exprUnwinds(en.Value) {
				return true
			}
		}
	case *ir.SliceExpr:
		return exprUnwinds(e.X) || exprUnwinds(e.Low) || exprUnwinds(e.High) || exprUnwinds(e.Max)
	case *ir.StructLit:
		for _, f := range e.Fields {
			if exprUnwinds(f.Value) {
				return true
			}
		}
	}
	return false
}

func argsUnwind(args []ir.Expr) bool {
	return slices.ContainsFunc(args, exprUnwinds)
}

// usesShim reports whether the module contains any intrinsic, which is the only
// thing that needs the runtime import at M0.
func usesShim(m *ir.Module) bool {
	for _, sd := range m.Structs {
		for _, f := range sd.Fields {
			switch f.Kind {
			case ir.FieldScalar:
				if exprUsesShim(f.Zero) {
					return true
				}
			case ir.FieldArray:
				// An array field always emits the clone and array-key helpers in the
				// generated copy and hash methods, so it needs the runtime import.
				return true
			case ir.FieldSync:
				// A sync field builds its runtime object in the constructor, so the
				// module imports the shim.
				return true
			}
		}
		for _, method := range sd.Methods {
			if blockUsesShim(method.Body) {
				return true
			}
		}
	}
	for _, fn := range m.Funcs {
		if blockUsesShim(fn.Body) {
			return true
		}
	}
	return false
}

func blockUsesShim(body []ir.Stmt) bool {
	for _, s := range body {
		switch s := s.(type) {
		case *ir.ExprStmt:
			if exprUsesShim(s.X) {
				return true
			}
		case *ir.ReturnStmt:
			if exprUsesShim(s.Value) {
				return true
			}
		case *ir.AssignStmt:
			if exprUsesShim(s.Value) {
				return true
			}
		case *ir.TupleAssign:
			if exprUsesShim(s.Value) {
				return true
			}
		case *ir.RangeMap:
			// A range over a map always calls the snapshot helper in the shim.
			return true
		case *ir.SetField:
			if exprUsesShim(s.Object) || exprUsesShim(s.Value) {
				return true
			}
		case *ir.SetIndex:
			if exprUsesShim(s.Object) || exprUsesShim(s.Index) || exprUsesShim(s.Value) {
				return true
			}
		case *ir.DerefSet:
			if exprUsesShim(s.Ptr) || exprUsesShim(s.Value) {
				return true
			}
		case *ir.IfStmt:
			if exprUsesShim(s.Cond) || blockUsesShim(s.Then) || blockUsesShim(s.Else) {
				return true
			}
		case *ir.ForStmt:
			if exprUsesShim(s.Cond) || blockUsesShim(s.Body) {
				return true
			}
		case *ir.ForRange:
			if exprUsesShim(s.Start) || exprUsesShim(s.Stop) || exprUsesShim(s.Step) || blockUsesShim(s.Body) {
				return true
			}
		case *ir.RangeString:
			// A range over a string always calls the rune decoder in the shim.
			return true
		case *ir.FuncDef:
			if capturesUseShim(s.Captures) || blockUsesShim(s.Body) {
				return true
			}
		case *ir.DeferBlock:
			// A reshaped defer block names the runtime's GoPanic and _run_defers in
			// its except handler, so it needs the import regardless of its body.
			if s.Reshape || blockUsesShim(s.Body) {
				return true
			}
		case *ir.DeferPush:
			if exprUsesShim(s.Func) || argsUseShim(s.Args) {
				return true
			}
		case *ir.DeferReturn:
			// A defer return calls the runtime's _run_defers.
			return true
		case *ir.Panic:
			// A panic raises the runtime's GoPanic, and its value may reach the shim too.
			return true
		}
	}
	return false
}

func exprUsesShim(e ir.Expr) bool {
	switch e := e.(type) {
	case *ir.Intrinsic, *ir.Mask, *ir.ShimFunc:
		return true
	case *ir.BinaryExpr:
		return exprUsesShim(e.X) || exprUsesShim(e.Y)
	case *ir.UnaryExpr:
		return exprUsesShim(e.X)
	case *ir.Convert:
		return exprUsesShim(e.X)
	case *ir.IndexExpr:
		return exprUsesShim(e.X) || exprUsesShim(e.Index)
	case *ir.CallExpr:
		return argsUseShim(e.Args)
	case *ir.MethodCall:
		return exprUsesShim(e.Recv) || argsUseShim(e.Args)
	case *ir.MethodValue:
		return exprUsesShim(e.Recv)
	case *ir.MethodExpr:
		// The receiver is a class name and the copy is a method on the value, so no
		// runtime shim is reached.
		return false
	case *ir.FieldAccess:
		return exprUsesShim(e.X)
	case *ir.AddrField:
		// The field-pointer helper always comes from the runtime shim.
		return true
	case *ir.AddrIndex:
		// The index-pointer helper always comes from the runtime shim.
		return true
	case *ir.Deref:
		return exprUsesShim(e.X)
	case *ir.Tuple:
		return argsUseShim(e.Elems)
	case *ir.Lambda:
		return capturesUseShim(e.Captures) || exprUsesShim(e.Body)
	case *ir.Clone:
		return exprUsesShim(e.X)
	case *ir.ArrayClone:
		// The array clone helper always comes from the runtime shim.
		return true
	case *ir.ArrayZero:
		return exprUsesShim(e.Elem)
	case *ir.ArrayLit:
		return argsUseShim(e.Elems)
	case *ir.SliceLit, *ir.SliceMake, *ir.NilSlice:
		// A slice literal, a make, and the nil sentinel all name a runtime helper,
		// so each needs the shim import regardless of its operands.
		return true
	case *ir.NilMap:
		// The nil map sentinel names a runtime helper, so it needs the shim import.
		return true
	case *ir.NilPtr:
		// The nil pointer sentinel names a runtime helper, so it needs the shim import.
		return true
	case *ir.MapLit:
		for _, en := range e.Entries {
			if exprUsesShim(en.Key) || exprUsesShim(en.Value) {
				return true
			}
		}
	case *ir.SliceExpr:
		// The full slice with a max bound names the _subslice3 helper, so it always
		// needs the shim; a two-index slice needs it only if an operand does.
		if e.Max != nil {
			return true
		}
		return exprUsesShim(e.X) || exprUsesShim(e.Low) || exprUsesShim(e.High)
	case *ir.StructLit:
		for _, f := range e.Fields {
			if exprUsesShim(f.Value) {
				return true
			}
		}
	}
	return false
}

func argsUseShim(args []ir.Expr) bool {
	return slices.ContainsFunc(args, exprUsesShim)
}

func capturesUseShim(caps []ir.Capture) bool {
	return slices.ContainsFunc(caps, func(c ir.Capture) bool { return exprUsesShim(c.Value) })
}

func emitFunc(b *strings.Builder, fn *ir.Func) error {
	fmt.Fprintf(b, "def %s(%s):\n", fn.Name, strings.Join(fn.Params, ", "))
	return emitBlock(b, fn.Body, 1)
}

// emitInterface writes the runtime-checkable Protocol a Go interface lowers to:
// the @runtime_checkable decorator, a class deriving Protocol, and one bare
// method per interface method whose body is an ellipsis. The Protocol carries no
// annotations, matching the annotation-free code the emitter produces elsewhere,
// so it documents the method set and answers a structural isinstance without
// taking part in dispatch. An empty interface has no methods, so its body is a
// single pass, and it accepts every value the way Go's any does.
func emitInterface(b *strings.Builder, id *ir.InterfaceDef) {
	b.WriteString("@runtime_checkable\n")
	fmt.Fprintf(b, "class %s(Protocol):\n", id.Name)
	if len(id.Methods) == 0 {
		writeIndent(b, 1)
		b.WriteString("pass\n")
		return
	}
	for i, method := range id.Methods {
		if i > 0 {
			b.WriteString("\n")
		}
		writeIndent(b, 1)
		fmt.Fprintf(b, "def %s(self", method.Name)
		for _, p := range method.Params {
			fmt.Fprintf(b, ", %s", p)
		}
		b.WriteString("): ...\n")
	}
}

// emitStruct writes the Python class a Go struct lowers to: a __slots__ tuple of
// the field names, a constructor whose defaults are each field's zero value, and
// a copy method that copies scalar fields by assignment and recurses into
// value-struct fields. A scalar field defaults to its zero literal and copies by
// sharing; a value-struct field defaults to None and builds a fresh zero instance
// in the constructor body, so a keyword literal that omits it still gets an
// independent value, and copies by calling that field's own copy.
func emitStruct(b *strings.Builder, pkg string, sd *ir.StructDef) error {
	fmt.Fprintf(b, "class %s:\n", sd.Name)
	writeIndent(b, 1)
	// _hebi_type is the Go type name fmt's %T and %#v print, package-qualified the
	// Go way, such as main.Point. It lives on the class, not the instance, so it
	// coexists with __slots__, and go_str keys the struct-rendering path on its
	// presence.
	fmt.Fprintf(b, "_hebi_type = %q\n", pkg+"."+sd.Name)
	writeIndent(b, 1)
	b.WriteString("__slots__ = (")
	for i, f := range sd.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", f.Name)
	}
	if len(sd.Fields) == 1 {
		// A one-element Python tuple needs a trailing comma to be a tuple.
		b.WriteString(",")
	}
	b.WriteString(")\n\n")

	writeIndent(b, 1)
	b.WriteString("def __init__(self")
	for _, f := range sd.Fields {
		def := "None"
		if f.Kind == ir.FieldScalar {
			// A scalar zero always emits without the shim's help, so this cannot
			// error; a struct field defaults to None and is built in the body.
			def, _ = emitExpr(f.Zero)
		}
		fmt.Fprintf(b, ", %s=%s", sd.CtorParamName(f.Name), def)
	}
	b.WriteString("):\n")
	if len(sd.Fields) == 0 {
		writeIndent(b, 2)
		b.WriteString("pass\n")
	}
	for _, f := range sd.Fields {
		param := sd.CtorParamName(f.Name)
		writeIndent(b, 2)
		switch f.Kind {
		case ir.FieldStruct:
			fmt.Fprintf(b, "self.%s = %s if %s is not None else %s()\n", f.Name, param, param, f.Struct)
		case ir.FieldArray:
			// An array field is a value, so like a struct field it defaults to None
			// and builds a fresh zero array in the body, since a mutable default
			// argument would be shared across every instance of the class.
			zero, _ := emitExpr(f.Zero)
			fmt.Fprintf(b, "self.%s = %s if %s is not None else %s\n", f.Name, param, param, zero)
		case ir.FieldSync:
			// A sync field builds its own runtime object in the body so each instance
			// owns one, defaulting to None like an array field, since a shared default
			// argument would give every instance the same lock.
			zero, _ := emitExpr(f.Zero)
			fmt.Fprintf(b, "self.%s = %s if %s is not None else %s\n", f.Name, param, param, zero)
		default:
			fmt.Fprintf(b, "self.%s = %s\n", f.Name, param)
		}
	}

	b.WriteString("\n")
	writeIndent(b, 1)
	b.WriteString("def copy(self):\n")
	writeIndent(b, 2)
	fmt.Fprintf(b, "return %s(", sd.Name)
	for i, f := range sd.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		switch f.Kind {
		case ir.FieldStruct:
			fmt.Fprintf(b, "self.%s.copy()", f.Name)
		case ir.FieldArray:
			fmt.Fprintf(b, "%s._clone_array(self.%s)", shim.Name, f.Name)
		default:
			fmt.Fprintf(b, "self.%s", f.Name)
		}
	}
	b.WriteString(")\n")

	if sd.Comparable {
		emitStructEq(b, sd)
	}

	for _, method := range sd.Methods {
		b.WriteString("\n")
		writeIndent(b, 1)
		fmt.Fprintf(b, "def %s(%s):\n", method.Name, strings.Join(append([]string{"self"}, method.Params...), ", "))
		if err := emitBlock(b, method.Body, 2); err != nil {
			return err
		}
	}
	return nil
}

// emitStructEq writes the field-wise __eq__ and matching __hash__ a comparable Go
// struct earns, so == compares by value and the struct can serve as a map key.
// __eq__ guards on the exact class and returns NotImplemented on a mismatch, which
// is defensive since Go only ever compares two values of the same type, then
// compares field by field, recursing into a value-struct field through its own
// __eq__. __hash__ hashes the tuple of the same fields in the same order, so the
// two agree.
func emitStructEq(b *strings.Builder, sd *ir.StructDef) {
	b.WriteString("\n")
	writeIndent(b, 1)
	b.WriteString("def __eq__(self, other):\n")
	writeIndent(b, 2)
	fmt.Fprintf(b, "if other.__class__ is not %s:\n", sd.Name)
	writeIndent(b, 3)
	b.WriteString("return NotImplemented\n")
	writeIndent(b, 2)
	if len(sd.Fields) == 0 {
		b.WriteString("return True\n")
	} else {
		parts := make([]string, len(sd.Fields))
		for i, f := range sd.Fields {
			parts[i] = fmt.Sprintf("self.%s == other.%s", f.Name, f.Name)
		}
		fmt.Fprintf(b, "return %s\n", strings.Join(parts, " and "))
	}

	b.WriteString("\n")
	writeIndent(b, 1)
	b.WriteString("def __hash__(self):\n")
	writeIndent(b, 2)
	parts := make([]string, len(sd.Fields))
	for i, f := range sd.Fields {
		if f.Kind == ir.FieldArray {
			// A Python list is not hashable, so an array field is projected to a
			// hashable tuple form for the hash, which a Go array field allows since
			// a comparable array is a valid map key.
			parts[i] = fmt.Sprintf("%s._arraykey(self.%s)", shim.Name, f.Name)
		} else {
			parts[i] = fmt.Sprintf("self.%s", f.Name)
		}
	}
	inner := strings.Join(parts, ", ")
	if len(sd.Fields) == 1 {
		// A one-element tuple needs a trailing comma to be a tuple, not a grouping.
		inner += ","
	}
	fmt.Fprintf(b, "return hash((%s))\n", inner)
}

// emitBlock writes a suite at the given indentation depth, one tab stop being
// four spaces. An empty suite becomes a single pass, which Python requires.
func emitBlock(b *strings.Builder, body []ir.Stmt, depth int) error {
	if len(body) == 0 {
		writeIndent(b, depth)
		b.WriteString("pass\n")
		return nil
	}
	for _, s := range body {
		if err := emitStmt(b, s, depth); err != nil {
			return err
		}
	}
	return nil
}

func emitStmt(b *strings.Builder, s ir.Stmt, depth int) error {
	switch s := s.(type) {
	case *ir.ExprStmt:
		expr, err := emitExpr(s.X)
		if err != nil {
			return err
		}
		writeIndent(b, depth)
		b.WriteString(expr)
		b.WriteString("\n")
	case *ir.FuncDef:
		sig, err := emitSignature(s.Params, s.Captures)
		if err != nil {
			return err
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "def %s(%s):\n", s.Name, sig)
		if len(s.Nonlocals) > 0 {
			writeIndent(b, depth+1)
			fmt.Fprintf(b, "nonlocal %s\n", strings.Join(s.Nonlocals, ", "))
		}
		return emitBlock(b, s.Body, depth+1)
	case *ir.ReturnStmt:
		writeIndent(b, depth)
		if s.Value == nil {
			b.WriteString("return\n")
			return nil
		}
		value, err := emitExpr(s.Value)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "return %s\n", value)
	case *ir.AssignStmt:
		// Python draws no distinction between := and =, so both lower to a
		// plain binding; the difference only matters to the Go type checker.
		value, err := emitExpr(s.Value)
		if err != nil {
			return err
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "%s = %s\n", s.Name, value)
	case *ir.TupleAssign:
		// A comma-ok read binds two names from one tuple-valued call, v, ok = ...,
		// which Python unpacks positionally; := and = read the same here.
		value, err := emitExpr(s.Value)
		if err != nil {
			return err
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "%s = %s\n", strings.Join(s.Names, ", "), value)
	case *ir.RangeMap:
		if err := emitRangeMap(b, s, depth); err != nil {
			return err
		}
	case *ir.SetField:
		obj, err := emitExpr(s.Object)
		if err != nil {
			return err
		}
		value, err := emitExpr(s.Value)
		if err != nil {
			return err
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "%s.%s = %s\n", obj, s.Name, value)
	case *ir.SetIndex:
		obj, err := emitExpr(s.Object)
		if err != nil {
			return err
		}
		index, err := emitExpr(s.Index)
		if err != nil {
			return err
		}
		value, err := emitExpr(s.Value)
		if err != nil {
			return err
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "%s[%s] = %s\n", obj, index, value)
	case *ir.DerefSet:
		ptr, err := emitExpr(s.Ptr)
		if err != nil {
			return err
		}
		value, err := emitExpr(s.Value)
		if err != nil {
			return err
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "%s.set(%s)\n", ptr, value)
	case *ir.DeferBlock:
		if s.Reshape {
			return emitDeferReshape(b, s, depth)
		}
		writeIndent(b, depth)
		b.WriteString("_defers = []\n")
		writeIndent(b, depth)
		b.WriteString("try:\n")
		if err := emitBlock(b, s.Body, depth+1); err != nil {
			return err
		}
		writeIndent(b, depth)
		b.WriteString("finally:\n")
		writeIndent(b, depth+1)
		b.WriteString("for _fn, _args in reversed(_defers):\n")
		writeIndent(b, depth+2)
		b.WriteString("_fn(*_args)\n")
	case *ir.DeferReturn:
		writeIndent(b, depth)
		fmt.Fprintf(b, "%s._run_defers(_defers)\n", shim.Name)
		writeIndent(b, depth)
		b.WriteString(deferResultReturn(s.Results))
		b.WriteString("\n")
	case *ir.Panic:
		value, err := emitExpr(s.Value)
		if err != nil {
			return err
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "raise %s.GoPanic(%s)\n", shim.Name, value)
	case *ir.DeferPush:
		fn, err := emitExpr(s.Func)
		if err != nil {
			return err
		}
		args, err := emitArgs(s.Args)
		if err != nil {
			return err
		}
		if len(s.Args) == 1 {
			args += ","
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "_defers.append((%s, (%s)))\n", fn, args)
	case *ir.IfStmt:
		cond, err := emitExpr(s.Cond)
		if err != nil {
			return err
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "if %s:\n", cond)
		if err := emitBlock(b, s.Then, depth+1); err != nil {
			return err
		}
		if err := emitElse(b, s.Else, depth); err != nil {
			return err
		}
	case *ir.ForStmt:
		cond := "True"
		if s.Cond != nil {
			c, err := emitExpr(s.Cond)
			if err != nil {
				return err
			}
			cond = c
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "while %s:\n", cond)
		if err := emitBlock(b, s.Body, depth+1); err != nil {
			return err
		}
	case *ir.ForRange:
		if err := emitForRange(b, s, depth); err != nil {
			return err
		}
	case *ir.Break:
		writeIndent(b, depth)
		b.WriteString("break\n")
	case *ir.Continue:
		writeIndent(b, depth)
		b.WriteString("continue\n")
	case *ir.RangeString:
		if err := emitRangeString(b, s, depth); err != nil {
			return err
		}
	case *ir.LabeledBreak:
		return fmt.Errorf("emit: labeled break to %q reached the emitter unresolved", s.Label)
	case *ir.LabeledContinue:
		return fmt.Errorf("emit: labeled continue to %q reached the emitter unresolved", s.Label)
	default:
		return fmt.Errorf("emit: unsupported statement type %T", s)
	}
	return nil
}

// emitDeferReshape writes the try-and-except shape a function needs when a
// deferred call reads or changes a named result or calls recover. The body runs
// under a try that ends by running the deferred calls and returning the result
// variables, which covers a function that falls off its end or reaches a bare
// return. Each explicit return inside the body is a DeferReturn that runs the
// deferred calls before it hands the results back. The except catches an escaping
// GoPanic, runs the deferred calls, and then, if a deferred recover consumed the
// panic, returns the result variables as the deferred calls left them; otherwise
// it re-raises so the panic keeps unwinding. Every exit runs the deferred calls
// exactly once.
func emitDeferReshape(b *strings.Builder, s *ir.DeferBlock, depth int) error {
	writeIndent(b, depth)
	b.WriteString("_defers = []\n")
	writeIndent(b, depth)
	b.WriteString("try:\n")
	if err := emitBlock(b, s.Body, depth+1); err != nil {
		return err
	}
	if !endsTerminated(s.Body) {
		writeIndent(b, depth+1)
		fmt.Fprintf(b, "%s._run_defers(_defers)\n", shim.Name)
		writeIndent(b, depth+1)
		b.WriteString(deferResultReturn(s.Results))
		b.WriteString("\n")
	}
	writeIndent(b, depth)
	fmt.Fprintf(b, "except %s.GoPanic as _p:\n", shim.Name)
	writeIndent(b, depth+1)
	fmt.Fprintf(b, "%s._run_defers(_defers)\n", shim.Name)
	writeIndent(b, depth+1)
	b.WriteString("if not _p.recovered:\n")
	writeIndent(b, depth+2)
	b.WriteString("raise\n")
	writeIndent(b, depth+1)
	b.WriteString(deferResultReturn(s.Results))
	b.WriteString("\n")
	return nil
}

// endsTerminated reports whether a reshaped body's last statement already leaves
// the function on every path, either a DeferReturn that drains the defers and
// returns or a Panic that raises. When it does, the try needs no trailing normal
// exit, since control never falls off the end. A body that can fall through, such
// as a void function that recovers and returns implicitly, has no such tail and
// still needs the trailing drain and return.
func endsTerminated(body []ir.Stmt) bool {
	if len(body) == 0 {
		return false
	}
	switch body[len(body)-1].(type) {
	case *ir.DeferReturn, *ir.Panic:
		return true
	default:
		return false
	}
}

// deferResultReturn renders the return a reshaped deferring function hands back:
// a bare return for no results, the single name for one, and a parenthesized tuple
// for several, so the emitted return matches the function's result arity.
func deferResultReturn(results []string) string {
	switch len(results) {
	case 0:
		return "return"
	case 1:
		return "return " + results[0]
	default:
		return "return (" + strings.Join(results, ", ") + ")"
	}
}

// emitElse writes an if statement's else part. An else block that is exactly one
// nested if becomes an elif, which is the same semantics as else followed by if
// but reads far better for an else-if chain or a switch's case ladder; any other
// else block is a plain else suite.
func emitElse(b *strings.Builder, els []ir.Stmt, depth int) error {
	if len(els) == 0 {
		return nil
	}
	if len(els) == 1 {
		if nested, ok := els[0].(*ir.IfStmt); ok {
			cond, err := emitExpr(nested.Cond)
			if err != nil {
				return err
			}
			writeIndent(b, depth)
			fmt.Fprintf(b, "elif %s:\n", cond)
			if err := emitBlock(b, nested.Then, depth+1); err != nil {
				return err
			}
			return emitElse(b, nested.Else, depth)
		}
	}
	writeIndent(b, depth)
	b.WriteString("else:\n")
	return emitBlock(b, els, depth+1)
}

// emitForRange writes the for-in-range loop a counted Go loop and a range over
// an integer lower to. A missing start is left off so the call reads range(stop),
// a present start prints as range(start, stop), and a step adds a third argument,
// which needs an explicit start of zero when the source had none. A loop that
// discards its variable spends the throwaway name so the count still runs.
func emitForRange(b *strings.Builder, s *ir.ForRange, depth int) error {
	stop, err := emitExpr(s.Stop)
	if err != nil {
		return err
	}
	var args string
	switch {
	case s.Start == nil && s.Step == nil:
		args = stop
	default:
		start := "0"
		if s.Start != nil {
			start, err = emitExpr(s.Start)
			if err != nil {
				return err
			}
		}
		args = start + ", " + stop
		if s.Step != nil {
			step, err := emitExpr(s.Step)
			if err != nil {
				return err
			}
			args += ", " + step
		}
	}
	name := s.Var
	if name == "" {
		name = "_"
	}
	writeIndent(b, depth)
	fmt.Fprintf(b, "for %s in range(%s):\n", name, args)
	return emitBlock(b, s.Body, depth+1)
}

// emitRangeMap writes the for loop a range over a map lowers to. It iterates a
// snapshot the runtime takes of the map's items or keys, so a delete during the
// range is safe the way Go's is and the nil map yields nothing. A key-and-value
// range walks the items, a key-only range walks the keys, and a blank target
// spends the throwaway name so the iteration still runs.
func emitRangeMap(b *strings.Builder, s *ir.RangeMap, depth int) error {
	src, err := emitExpr(s.Source)
	if err != nil {
		return err
	}
	key := s.Key
	if key == "" {
		key = "_"
	}
	writeIndent(b, depth)
	if s.Value != "" {
		fmt.Fprintf(b, "for %s, %s in %s._map_items(%s):\n", key, s.Value, shim.Name, src)
	} else {
		fmt.Fprintf(b, "for %s in %s._map_keys(%s):\n", key, shim.Name, src)
	}
	return emitBlock(b, s.Body, depth+1)
}

// emitRangeString writes the while form a range over a string lowers to: a byte
// cursor starts at zero, and each step decodes the rune at the cursor, exposes
// the byte index and rune to the loop body under the user's names, runs the
// body, and advances the cursor by the rune's byte width. A missing rune target
// is decoded into the throwaway name so the width is still read, and a missing
// index target drops its assignment, matching Go's blank identifier.
func emitRangeString(b *strings.Builder, s *ir.RangeString, depth int) error {
	src, err := emitExpr(s.Source)
	if err != nil {
		return err
	}
	writeIndent(b, depth)
	fmt.Fprintf(b, "%s = 0\n", s.Cursor)
	writeIndent(b, depth)
	fmt.Fprintf(b, "while %s < len(%s):\n", s.Cursor, src)
	value := s.Value
	if value == "" {
		value = "_"
	}
	writeIndent(b, depth+1)
	fmt.Fprintf(b, "%s, %s = %s._decode_rune(%s, %s)\n", value, s.Width, shim.Name, src, s.Cursor)
	if s.Key != "" {
		writeIndent(b, depth+1)
		fmt.Fprintf(b, "%s = %s\n", s.Key, s.Cursor)
	}
	for _, st := range s.Body {
		if err := emitStmt(b, st, depth+1); err != nil {
			return err
		}
	}
	writeIndent(b, depth+1)
	fmt.Fprintf(b, "%s = %s + %s\n", s.Cursor, s.Cursor, s.Width)
	return nil
}

func emitExpr(e ir.Expr) (string, error) {
	switch e := e.(type) {
	case *ir.IntLit:
		return e.Text, nil
	case *ir.FloatLit:
		return e.Text, nil
	case *ir.StringLit:
		return pyBytes(e.Value), nil
	case *ir.BoolLit:
		if e.Value {
			return "True", nil
		}
		return "False", nil
	case *ir.Ident:
		return e.Name, nil
	case *ir.BinaryExpr:
		op, ok := binOps[e.Op]
		if !ok {
			return "", fmt.Errorf("emit: unsupported binary operator %q", e.Op)
		}
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		y, err := emitExpr(e.Y)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s %s %s)", x, op, y), nil
	case *ir.UnaryExpr:
		op, ok := unaryOps[e.Op]
		if !ok {
			return "", fmt.Errorf("emit: unsupported unary operator %q", e.Op)
		}
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		sep := ""
		if isWordOperator(op) {
			sep = " "
		}
		return fmt.Sprintf("(%s%s%s)", op, sep, x), nil
	case *ir.Mask:
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.%s(%s)", shim.Name, maskHelper(e.Bits, e.Signed), x), nil
	case *ir.Convert:
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s(%s)", e.To, x), nil
	case *ir.IndexExpr:
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		index, err := emitExpr(e.Index)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s[%s]", x, index), nil
	case *ir.CallExpr:
		args, err := emitArgs(e.Args)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s(%s)", e.Name, args), nil
	case *ir.MethodCall:
		recv, err := emitExpr(e.Recv)
		if err != nil {
			return "", err
		}
		args, err := emitArgs(e.Args)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.%s(%s)", recv, e.Name, args), nil
	case *ir.MethodValue:
		recv, err := emitExpr(e.Recv)
		if err != nil {
			return "", err
		}
		if e.Copy {
			return fmt.Sprintf("%s.copy().%s", recv, e.Name), nil
		}
		return fmt.Sprintf("%s.%s", recv, e.Name), nil
	case *ir.MethodExpr:
		if e.ValueCopy {
			return fmt.Sprintf("lambda _s, *_a: %s.%s(_s.copy(), *_a)", e.Recv, e.Name), nil
		}
		return fmt.Sprintf("%s.%s", e.Recv, e.Name), nil
	case *ir.Intrinsic:
		args, err := emitArgs(e.Args)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.%s(%s)", shim.Name, e.Name, args), nil
	case *ir.ShimFunc:
		return fmt.Sprintf("%s.%s", shim.Name, e.Name), nil
	case *ir.FieldAccess:
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.%s", x, e.Name), nil
	case *ir.AddrField:
		container, err := emitExpr(e.Container)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.FieldPtr(%s, %q)", shim.Name, container, e.Name), nil
	case *ir.AddrIndex:
		seq, err := emitExpr(e.Seq)
		if err != nil {
			return "", err
		}
		index, err := emitExpr(e.Index)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.IndexPtr(%s, %s)", shim.Name, seq, index), nil
	case *ir.Deref:
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.get()", x), nil
	case *ir.Tuple:
		parts, err := emitArgs(e.Elems)
		if err != nil {
			return "", err
		}
		if len(e.Elems) == 1 {
			// A one-element Python tuple needs a trailing comma, or the
			// parentheses read as plain grouping rather than a tuple, which would
			// turn a single-case select's case list into the case itself.
			return fmt.Sprintf("(%s,)", parts), nil
		}
		return fmt.Sprintf("(%s)", parts), nil
	case *ir.Lambda:
		sig, err := emitSignature(e.Params, e.Captures)
		if err != nil {
			return "", err
		}
		body, err := emitExpr(e.Body)
		if err != nil {
			return "", err
		}
		if sig == "" {
			return fmt.Sprintf("lambda: %s", body), nil
		}
		return fmt.Sprintf("lambda %s: %s", sig, body), nil
	case *ir.StructLit:
		parts := make([]string, len(e.Fields))
		for i, f := range e.Fields {
			v, err := emitExpr(f.Value)
			if err != nil {
				return "", err
			}
			if e.Keyed {
				parts[i] = fmt.Sprintf("%s=%s", f.Name, v)
			} else {
				parts[i] = v
			}
		}
		return fmt.Sprintf("%s(%s)", e.Type, strings.Join(parts, ", ")), nil
	case *ir.Clone:
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.copy()", x), nil
	case *ir.ArrayZero:
		elem, err := emitExpr(e.Elem)
		if err != nil {
			return "", err
		}
		if e.Len == 0 {
			return "[]", nil
		}
		if e.ElemMutable {
			// A struct or nested-array element must be built fresh at each position,
			// so a comprehension re-evaluates the zero for every slot; a repeated
			// list would alias one mutable element across the whole array.
			return fmt.Sprintf("[%s for _ in range(%d)]", elem, e.Len), nil
		}
		// A scalar element is immutable, so repeating one value is both correct and
		// the most readable form.
		return fmt.Sprintf("[%s] * %d", elem, e.Len), nil
	case *ir.ArrayLit:
		elems, err := emitArgs(e.Elems)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("[%s]", elems), nil
	case *ir.ArrayClone:
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s._clone_array(%s)", shim.Name, x), nil
	case *ir.SliceLit:
		elems, err := emitArgs(e.Elems)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s._slice_lit([%s])", shim.Name, elems), nil
	case *ir.SliceMake:
		length, err := emitExpr(e.Len)
		if err != nil {
			return "", err
		}
		capacity, err := emitExpr(e.Cap)
		if err != nil {
			return "", err
		}
		elem, err := emitExpr(e.Elem)
		if err != nil {
			return "", err
		}
		backing := fmt.Sprintf("[%s] * %s", elem, capacity)
		if e.ElemMutable {
			// A struct or nested-array element must be built fresh at each slot, so a
			// comprehension re-evaluates the zero per position; a repeated list would
			// alias one mutable element across the whole backing.
			backing = fmt.Sprintf("[%s for _ in range(%s)]", elem, capacity)
		}
		return fmt.Sprintf("%s.Slice(%s, 0, %s, %s)", shim.Name, backing, length, capacity), nil
	case *ir.SliceExpr:
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		low := ""
		if e.Low != nil {
			low, err = emitExpr(e.Low)
			if err != nil {
				return "", err
			}
		}
		high := ""
		if e.High != nil {
			high, err = emitExpr(e.High)
			if err != nil {
				return "", err
			}
		}
		if e.Max != nil {
			// The full slice caps the reserved capacity, which Python's slice syntax
			// cannot express, so it routes through the runtime helper. A low bound that
			// the source omitted defaults to zero there, since the helper takes it as an
			// explicit offset rather than an empty slot.
			max, err := emitExpr(e.Max)
			if err != nil {
				return "", err
			}
			if low == "" {
				low = "0"
			}
			return fmt.Sprintf("%s._subslice3(%s, %s, %s, %s)", shim.Name, x, low, high, max), nil
		}
		return fmt.Sprintf("%s[%s:%s]", x, low, high), nil
	case *ir.NilSlice:
		return shim.Name + ".NIL_SLICE", nil
	case *ir.MapLit:
		parts := make([]string, len(e.Entries))
		for i, en := range e.Entries {
			key, err := emitExpr(en.Key)
			if err != nil {
				return "", err
			}
			value, err := emitExpr(en.Value)
			if err != nil {
				return "", err
			}
			parts[i] = key + ": " + value
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case *ir.NilMap:
		return shim.Name + ".NIL_MAP", nil
	case *ir.NilPtr:
		return shim.Name + ".NIL_PTR", nil
	case *ir.NilInterface:
		return "None", nil
	case *ir.EmptyStruct:
		return "()", nil
	default:
		return "", fmt.Errorf("emit: unsupported expression type %T", e)
	}
}

func emitArgs(args []ir.Expr) (string, error) {
	parts := make([]string, len(args))
	for i, a := range args {
		s, err := emitExpr(a)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return strings.Join(parts, ", "), nil
}

// emitSignature joins a closure's plain parameters with its captured defaults,
// the parameters first and each capture as name=value after them, so the reuse
// name form name=name reads as binding the current value once at creation.
func emitSignature(params []string, caps []ir.Capture) (string, error) {
	parts := make([]string, 0, len(params)+len(caps))
	parts = append(parts, params...)
	for _, c := range caps {
		value, err := emitExpr(c.Value)
		if err != nil {
			return "", err
		}
		parts = append(parts, c.Param+"="+value)
	}
	return strings.Join(parts, ", "), nil
}

func writeIndent(b *strings.Builder, depth int) {
	for range depth {
		b.WriteString("    ")
	}
}

// pyBytes renders a Go string value as a Python bytes literal holding its UTF-8
// encoding, which is hebi's internal representation of a Go string. It iterates
// bytes, not runes, escaping the ones that would break the literal and writing a
// hex escape for anything outside printable ASCII, so a multibyte rune becomes
// its exact bytes and the output stays plain ASCII.
func pyBytes(s string) string {
	var b strings.Builder
	b.WriteString(`b"`)
	for i := range len(s) {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 || c >= 0x7f {
				fmt.Fprintf(&b, `\x%02x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
