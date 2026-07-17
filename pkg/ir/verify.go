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
	structs := make(map[string]bool, len(m.Structs))
	for i, sd := range m.Structs {
		if sd == nil {
			return fmt.Errorf("ir: struct %d is nil", i)
		}
		if sd.Name == "" {
			return fmt.Errorf("ir: struct %d has no name", i)
		}
		if structs[sd.Name] {
			return fmt.Errorf("ir: struct %s is defined more than once", sd.Name)
		}
		structs[sd.Name] = true
		for j, f := range sd.Fields {
			where := fmt.Sprintf("struct %s: field %d", sd.Name, j)
			if f.Name == "" {
				return fmt.Errorf("ir: %s has no name", where)
			}
			switch f.Kind {
			case FieldScalar:
				if err := verifyExpr(where+": zero", f.Zero); err != nil {
					return err
				}
			case FieldStruct:
				if f.Struct == "" {
					return fmt.Errorf("ir: %s is a struct field with no type", where)
				}
			default:
				return fmt.Errorf("ir: %s has an unknown kind %d", where, f.Kind)
			}
		}
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
		for j, p := range fn.Params {
			if p == "" {
				return fmt.Errorf("ir: func %s: parameter %d has no name", fn.Name, j)
			}
		}
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
	case *ReturnStmt:
		if s.Value == nil {
			// A bare return carries no value, so there is nothing to check.
			return nil
		}
		return verifyExpr(where+": value", s.Value)
	case *AssignStmt:
		if s.Name == "" {
			return fmt.Errorf("ir: %s assigns to an empty name", where)
		}
		return verifyExpr(where+": value", s.Value)
	case *SetField:
		if s.Name == "" {
			return fmt.Errorf("ir: %s assigns to an empty field name", where)
		}
		if err := verifyExpr(where+": object", s.Object); err != nil {
			return err
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
		if err := verifyBlock(where+": continue step", s.ContinueStep); err != nil {
			return err
		}
		return verifyBlock(where+": body", s.Body)
	case *ForRange:
		if s.Stop == nil {
			return fmt.Errorf("ir: %s ranges without a stop bound", where)
		}
		if err := verifyExpr(where+": range stop", s.Stop); err != nil {
			return err
		}
		if s.Start != nil {
			if err := verifyExpr(where+": range start", s.Start); err != nil {
				return err
			}
		}
		if s.Step != nil {
			if err := verifyExpr(where+": range step", s.Step); err != nil {
				return err
			}
		}
		return verifyBlock(where+": body", s.Body)
	case *Break, *Continue:
		// A break or continue carries no operand, so there is nothing to check.
		return nil
	case *LabeledBreak:
		// The labeled-break pass rewrites every LabeledBreak into a flag and a
		// plain break, so one reaching the verifier is a lowering bug.
		return fmt.Errorf("ir: %s is an unresolved labeled break to %q", where, s.Label)
	case *LabeledContinue:
		// The labeled-jump pass rewrites every LabeledContinue into a flag and a
		// continue, so one reaching the verifier is a lowering bug.
		return fmt.Errorf("ir: %s is an unresolved labeled continue to %q", where, s.Label)
	case *RangeString:
		if s.Cursor == "" || s.Width == "" {
			return fmt.Errorf("ir: %s ranges a string without a cursor or width name", where)
		}
		if err := verifyExpr(where+": range source", s.Source); err != nil {
			return err
		}
		if err := verifyBlock(where+": continue step", s.ContinueStep); err != nil {
			return err
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
	case *FloatLit:
		if e.Text == "" {
			return fmt.Errorf("ir: %s is a float literal with no text", where)
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
	case *Convert:
		switch e.To {
		case "int", "float":
		default:
			return fmt.Errorf("ir: %s converts with an unknown builtin %q", where, e.To)
		}
		return verifyExpr(where+": converted", e.X)
	case *IndexExpr:
		if err := verifyExpr(where+": indexed", e.X); err != nil {
			return err
		}
		return verifyExpr(where+": index", e.Index)
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
	case *FieldAccess:
		if e.Name == "" {
			return fmt.Errorf("ir: %s reads a field with no name", where)
		}
		return verifyExpr(where+": object", e.X)
	case *StructLit:
		if e.Type == "" {
			return fmt.Errorf("ir: %s constructs a struct with no type", where)
		}
		for i, f := range e.Fields {
			if e.Keyed && f.Name == "" {
				return fmt.Errorf("ir: %s: keyed field %d has no name", where, i)
			}
			if err := verifyExpr(fmt.Sprintf("%s: field %d", where, i), f.Value); err != nil {
				return err
			}
		}
	case *Clone:
		return verifyExpr(where+": cloned", e.X)
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
