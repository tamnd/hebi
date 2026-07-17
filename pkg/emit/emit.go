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
		emitStruct(&b, sd)
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
			if f.Kind == ir.FieldScalar && exprUsesShim(f.Zero) {
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
		case *ir.SetField:
			if exprUsesShim(s.Object) || exprUsesShim(s.Value) {
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
	case *ir.FieldAccess:
		return exprUsesShim(e.X)
	case *ir.Clone:
		return exprUsesShim(e.X)
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
func emitStruct(b *strings.Builder, sd *ir.StructDef) {
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
		if f.Kind == ir.FieldStruct {
			fmt.Fprintf(b, "self.%s = %s if %s is not None else %s()\n", f.Name, param, param, f.Struct)
		} else {
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
		if f.Kind == ir.FieldStruct {
			fmt.Fprintf(b, "self.%s.copy()", f.Name)
		} else {
			fmt.Fprintf(b, "self.%s", f.Name)
		}
	}
	b.WriteString(")\n")

	if sd.Comparable {
		emitStructEq(b, sd)
	}
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
		parts[i] = fmt.Sprintf("self.%s", f.Name)
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
