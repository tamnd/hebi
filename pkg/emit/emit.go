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
	if usesShim(m) {
		fmt.Fprintf(&b, "import %s\n\n\n", shim.Name)
	}
	// Structs emit first as classes, in source order, so a function that
	// constructs one refers to a name already bound.
	wrote := false
	for _, sd := range m.Structs {
		if wrote {
			b.WriteString("\n\n")
		}
		if err := emitStruct(&b, sd); err != nil {
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
		b.WriteString("\n\nif __name__ == \"__main__\":\n    main()\n")
	}
	return b.String(), nil
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
		}
	}
	return false
}

func exprUsesShim(e ir.Expr) bool {
	switch e := e.(type) {
	case *ir.Intrinsic, *ir.Mask:
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

// emitStruct writes the Python class a Go struct lowers to: a __slots__ tuple of
// the field names, a constructor whose defaults are each field's zero value, and
// a copy method that copies scalar fields by assignment and recurses into
// value-struct fields. A scalar field defaults to its zero literal and copies by
// sharing; a value-struct field defaults to None and builds a fresh zero instance
// in the constructor body, so a keyword literal that omits it still gets an
// independent value, and copies by calling that field's own copy.
func emitStruct(b *strings.Builder, sd *ir.StructDef) error {
	fmt.Fprintf(b, "class %s:\n", sd.Name)
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
