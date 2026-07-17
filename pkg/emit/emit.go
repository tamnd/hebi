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
}

// unaryOps maps the Go unary operator text to its Python spelling. Negation and
// the no-op unary plus are shared; the growing case, negation, is masked by the
// lowering, not here.
var unaryOps = map[string]string{
	"-": "-",
	"+": "+",
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
	hasMain := false
	for i, fn := range m.Funcs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if err := emitFunc(&b, fn); err != nil {
			return "", err
		}
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
		case *ir.AssignStmt:
			if exprUsesShim(s.Value) {
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
	case *ir.CallExpr:
		return argsUseShim(e.Args)
	}
	return false
}

func argsUseShim(args []ir.Expr) bool {
	return slices.ContainsFunc(args, exprUsesShim)
}

func emitFunc(b *strings.Builder, fn *ir.Func) error {
	fmt.Fprintf(b, "def %s():\n", fn.Name)
	return emitBlock(b, fn.Body, 1)
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
	case *ir.AssignStmt:
		// Python draws no distinction between := and =, so both lower to a
		// plain binding; the difference only matters to the Go type checker.
		value, err := emitExpr(s.Value)
		if err != nil {
			return err
		}
		writeIndent(b, depth)
		fmt.Fprintf(b, "%s = %s\n", s.Name, value)
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
		if len(s.Else) > 0 {
			writeIndent(b, depth)
			b.WriteString("else:\n")
			if err := emitBlock(b, s.Else, depth+1); err != nil {
				return err
			}
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
	default:
		return fmt.Errorf("emit: unsupported statement type %T", s)
	}
	return nil
}

func emitExpr(e ir.Expr) (string, error) {
	switch e := e.(type) {
	case *ir.IntLit:
		return e.Text, nil
	case *ir.StringLit:
		return pyString(e.Value), nil
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
		return fmt.Sprintf("(%s%s)", op, x), nil
	case *ir.Mask:
		x, err := emitExpr(e.X)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.%s(%s)", shim.Name, maskHelper(e.Bits, e.Signed), x), nil
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

// pyString renders a Go string value as a double-quoted Python string literal,
// escaping the characters that would otherwise break the literal or read
// differently, and falling back to a hex escape for anything non-printable so
// the output stays plain ASCII.
func pyString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
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
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
