package ir

import "fmt"

// Verify checks a module's structural invariants and returns the first
// violation it finds, walking in a fixed order so the error is a pure function
// of the tree. A module that verifies is safe for the emitter to lower without
// nil checks of its own.
func Verify(m *Module) error {
	if m == nil {
		return fmt.Errorf("ir: module is nil")
	}
	if m.Package == "" {
		return fmt.Errorf("ir: module has no package name")
	}
	seen := make(map[string]bool, len(m.Funcs))
	for i, fn := range m.Funcs {
		if fn == nil {
			return fmt.Errorf("ir: func %d is nil", i)
		}
		if fn.Name == "" {
			return fmt.Errorf("ir: func %d has no name", i)
		}
		if seen[fn.Name] {
			return fmt.Errorf("ir: func %s is defined more than once", fn.Name)
		}
		seen[fn.Name] = true
		if err := verifyBlock(fn.Name, fn.Body); err != nil {
			return err
		}
	}
	return nil
}

func verifyBlock(where string, body []Stmt) error {
	for i, s := range body {
		if err := verifyStmt(fmt.Sprintf("%s: statement %d", where, i), s); err != nil {
			return err
		}
	}
	return nil
}

func verifyStmt(where string, s Stmt) error {
	switch s := s.(type) {
	case nil:
		return fmt.Errorf("ir: %s is nil", where)
	case *ExprStmt:
		return verifyExpr(where, s.X)
	case *AssignStmt:
		if s.Name == "" {
			return fmt.Errorf("ir: %s assigns to an empty name", where)
		}
		return verifyExpr(where+": value", s.Value)
	case *IfStmt:
		if err := verifyExpr(where+": if condition", s.Cond); err != nil {
			return err
		}
		if err := verifyBlock(where+": then", s.Then); err != nil {
			return err
		}
		return verifyBlock(where+": else", s.Else)
	case *ForStmt:
		if s.Cond != nil {
			if err := verifyExpr(where+": for condition", s.Cond); err != nil {
				return err
			}
		}
		return verifyBlock(where+": body", s.Body)
	default:
		return fmt.Errorf("ir: %s is an unknown statement type %T", where, s)
	}
}

func verifyExpr(where string, e Expr) error {
	switch e := e.(type) {
	case nil:
		return fmt.Errorf("ir: %s is a nil expression", where)
	case *IntLit:
		if e.Text == "" {
			return fmt.Errorf("ir: %s is an integer literal with no text", where)
		}
	case *StringLit:
		// Any string value is valid, including the empty string.
	case *BoolLit:
		// Both boolean values are valid.
	case *Ident:
		if e.Name == "" {
			return fmt.Errorf("ir: %s is an identifier with no name", where)
		}
	case *BinaryExpr:
		if e.Op == "" {
			return fmt.Errorf("ir: %s is a binary expression with no operator", where)
		}
		if err := verifyExpr(where+": left", e.X); err != nil {
			return err
		}
		return verifyExpr(where+": right", e.Y)
	case *UnaryExpr:
		if e.Op == "" {
			return fmt.Errorf("ir: %s is a unary expression with no operator", where)
		}
		return verifyExpr(where+": operand", e.X)
	case *Mask:
		switch e.Bits {
		case 8, 16, 32, 64:
		default:
			return fmt.Errorf("ir: %s masks to an invalid width %d", where, e.Bits)
		}
		return verifyExpr(where+": masked", e.X)
	case *CallExpr:
		if e.Name == "" {
			return fmt.Errorf("ir: %s calls an empty name", where)
		}
		return verifyArgs(where, e.Args)
	case *Intrinsic:
		if e.Name == "" {
			return fmt.Errorf("ir: %s is an intrinsic with no name", where)
		}
		return verifyArgs(where, e.Args)
	default:
		return fmt.Errorf("ir: %s is an unknown expression type %T", where, e)
	}
	return nil
}

func verifyArgs(where string, args []Expr) error {
	for i, a := range args {
		if err := verifyExpr(fmt.Sprintf("%s: arg %d", where, i), a); err != nil {
			return err
		}
	}
	return nil
}
