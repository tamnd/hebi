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
			case FieldArray:
				if err := verifyExpr(where+": zero", f.Zero); err != nil {
					return err
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
	case *FuncDef:
		if s.Name == "" {
			return fmt.Errorf("ir: %s is a nested def with no name", where)
		}
		if err := verifyParamNames(where, s.Params); err != nil {
			return err
		}
		if err := verifyCaptures(where, s.Captures); err != nil {
			return err
		}
		for i, n := range s.Nonlocals {
			if n == "" {
				return fmt.Errorf("ir: %s: nonlocal %d has no name", where, i)
			}
		}
		return verifyBlock(where+": "+s.Name+" body", s.Body)
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
	case *TupleAssign:
		if len(s.Names) < 2 {
			return fmt.Errorf("ir: %s assigns a tuple to fewer than two names", where)
		}
		for i, n := range s.Names {
			if n == "" {
				return fmt.Errorf("ir: %s: tuple target %d has no name", where, i)
			}
		}
		return verifyExpr(where+": value", s.Value)
	case *RangeMap:
		if err := verifyExpr(where+": range source", s.Source); err != nil {
			return err
		}
		return verifyBlock(where+": body", s.Body)
	case *SetField:
		if s.Name == "" {
			return fmt.Errorf("ir: %s assigns to an empty field name", where)
		}
		if err := verifyExpr(where+": object", s.Object); err != nil {
			return err
		}
		return verifyExpr(where+": value", s.Value)
	case *SetIndex:
		if err := verifyExpr(where+": object", s.Object); err != nil {
			return err
		}
		if err := verifyExpr(where+": index", s.Index); err != nil {
			return err
		}
		return verifyExpr(where+": value", s.Value)
	case *DerefSet:
		if err := verifyExpr(where+": pointer", s.Ptr); err != nil {
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
	case *AddrField:
		if e.Name == "" {
			return fmt.Errorf("ir: %s takes the address of a field with no name", where)
		}
		return verifyExpr(where+": container", e.Container)
	case *AddrIndex:
		if err := verifyExpr(where+": sequence", e.Seq); err != nil {
			return err
		}
		return verifyExpr(where+": index", e.Index)
	case *Deref:
		return verifyExpr(where+": pointer", e.X)
	case *Tuple:
		if len(e.Elems) < 2 {
			return fmt.Errorf("ir: %s is a tuple with fewer than two elements", where)
		}
		for i, el := range e.Elems {
			if err := verifyExpr(fmt.Sprintf("%s: element %d", where, i), el); err != nil {
				return err
			}
		}
	case *Lambda:
		if err := verifyParamNames(where, e.Params); err != nil {
			return err
		}
		if err := verifyCaptures(where, e.Captures); err != nil {
			return err
		}
		return verifyExpr(where+": lambda body", e.Body)
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
	case *ArrayZero:
		if e.Len < 0 {
			return fmt.Errorf("ir: %s builds an array with a negative length %d", where, e.Len)
		}
		return verifyExpr(where+": element zero", e.Elem)
	case *ArrayLit:
		for i, el := range e.Elems {
			if err := verifyExpr(fmt.Sprintf("%s: element %d", where, i), el); err != nil {
				return err
			}
		}
	case *ArrayClone:
		return verifyExpr(where+": cloned", e.X)
	case *SliceLit:
		for i, el := range e.Elems {
			if err := verifyExpr(fmt.Sprintf("%s: element %d", where, i), el); err != nil {
				return err
			}
		}
	case *SliceMake:
		if err := verifyExpr(where+": length", e.Len); err != nil {
			return err
		}
		if err := verifyExpr(where+": capacity", e.Cap); err != nil {
			return err
		}
		return verifyExpr(where+": element zero", e.Elem)
	case *SliceExpr:
		if err := verifyExpr(where+": sliced", e.X); err != nil {
			return err
		}
		if e.Low != nil {
			if err := verifyExpr(where+": low bound", e.Low); err != nil {
				return err
			}
		}
		if e.High != nil {
			if err := verifyExpr(where+": high bound", e.High); err != nil {
				return err
			}
		}
		if e.Max != nil {
			return verifyExpr(where+": max bound", e.Max)
		}
	case *NilSlice:
		// The nil slice sentinel carries no operand, so there is nothing to check.
	case *MapLit:
		for i, en := range e.Entries {
			if err := verifyExpr(fmt.Sprintf("%s: entry %d key", where, i), en.Key); err != nil {
				return err
			}
			if err := verifyExpr(fmt.Sprintf("%s: entry %d value", where, i), en.Value); err != nil {
				return err
			}
		}
	case *NilMap:
		// The nil map sentinel carries no operand, so there is nothing to check.
	case *NilPtr:
		// The nil pointer sentinel carries no operand, so there is nothing to check.
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

func verifyParamNames(where string, params []string) error {
	for i, p := range params {
		if p == "" {
			return fmt.Errorf("ir: %s: parameter %d has no name", where, i)
		}
	}
	return nil
}

func verifyCaptures(where string, caps []Capture) error {
	for i, c := range caps {
		if c.Param == "" {
			return fmt.Errorf("ir: %s: capture %d has no name", where, i)
		}
		if err := verifyExpr(fmt.Sprintf("%s: capture %d value", where, i), c.Value); err != nil {
			return err
		}
	}
	return nil
}
