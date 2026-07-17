// Package ir is hebi's intermediate representation: a small, explicit node set
// between the typed Go AST and the Python emitter, plus a verifier that checks
// a tree's structural invariants before it is handed on to be emitted.
//
// The IR is deliberately narrow. It carries only what the emitter needs, and it
// grows one node at a time as milestones add language surface. At M0 it covers
// the hello-scale subset: a package of functions whose bodies assign, branch,
// loop, and call, over integer, string, and boolean literals and binary
// arithmetic.
package ir

// Module is one Go package lowered to the IR. It emits to one Python module.
type Module struct {
	// Package is the Go package clause name, such as main.
	Package string
	// Structs are the package's struct types in source order, each emitted as a
	// Python class before the functions that use it.
	Structs []*StructDef
	// Funcs are the package's functions in source order.
	Funcs []*Func
}

// StructDef is a Go struct type lowered to a Python class with __slots__, a
// zero-value constructor, and a type-directed copy method. The class is
// generated directly rather than with a dataclass so hebi controls exactly which
// methods it emits.
type StructDef struct {
	// Name is the struct type name, which becomes the class name.
	Name string
	// Fields are the struct's fields in declaration order.
	Fields []StructField
	// Comparable is true when every field is comparable, so the struct earns a
	// field-wise __eq__ and a matching __hash__; Go rejects == on a struct that is
	// not comparable, so the emitter leaves such a struct with Python's identity
	// equality instead.
	Comparable bool
}

// CtorParamName returns the constructor parameter name for a field of this
// struct. It matches the field's attribute name except when that name would
// shadow a sibling value-struct's class inside the constructor body, where a
// zero value is built by calling that class: then the parameter is suffixed with
// an underscore so the class name still resolves. This arises for an embedded
// value struct, whose field name is its type name, so a field named Base would
// otherwise shadow the Base() call that builds another field's zero.
func (sd *StructDef) CtorParamName(field string) string {
	for _, g := range sd.Fields {
		if g.Kind == FieldStruct && g.Struct == field {
			return field + "_"
		}
	}
	return field
}

// FieldKind tells the emitter how a field takes part in construction and
// copying. A scalar field is an immutable value shared by assignment; a value
// struct field is itself a value, so it defaults to a freshly zeroed instance
// and copies by recursing into its own copy method.
type FieldKind int

const (
	// FieldScalar is a numeric, boolean, or string field: its zero value is a
	// literal and copy shares it directly.
	FieldScalar FieldKind = iota
	// FieldStruct is a value-struct field: its zero value is a fresh instance of
	// the field's struct type and copy recurses into that type's copy method.
	FieldStruct
)

// StructField is one field of a struct. Zero is the constructor default for a
// scalar field, the Go zero value of its type. Struct is the field's struct type
// name for a value-struct field, used to build its zero instance and to recurse
// in copy.
type StructField struct {
	Name   string
	Kind   FieldKind
	Zero   Expr
	Struct string
}

// Func is a function definition. Params are the parameter names in order, bound
// positionally at the call site; a blank or unnamed Go parameter is given a
// synthetic name so the Python signature stays well formed. A function returns at
// most one value in this milestone, through ReturnStmt; multiple and named
// returns arrive in M3.
type Func struct {
	// Name is the function name.
	Name string
	// Params are the parameter names in declaration order.
	Params []string
	// Body is the ordered list of statements.
	Body []Stmt
}

// Stmt is a statement node.
type Stmt interface{ isStmt() }

// ExprStmt is an expression evaluated for its effect, such as a call.
type ExprStmt struct{ X Expr }

// AssignStmt binds Value to Name. Define is true for a short variable
// declaration (:=) and false for a plain assignment (=).
type AssignStmt struct {
	Name   string
	Value  Expr
	Define bool
}

// ReturnStmt returns from a function. Value is the returned expression, or nil
// for a bare return with no value. A struct value returned by value is cloned at
// the return by the lowering, since the caller receives an independent value.
type ReturnStmt struct{ Value Expr }

// IfStmt is a conditional. Else may be empty.
type IfStmt struct {
	Cond Expr
	Then []Stmt
	Else []Stmt
}

// ForStmt is a for loop lowered to the while form. A nil Cond is an unconditional
// loop, matching Go's bare for. Label carries the Go loop label when the source
// labeled this loop, which the labeled-break pass reads to place its flag checks
// and which the emitter ignores, since Python has no loop labels. ContinueStep is
// the step a continue owes this loop, the post of a three-clause for, which the
// labeled-continue pass runs before a continue it injects into this loop; it is
// nil for a bare while and is already present at the bottom of Body for the
// emitter, so the emitter ignores it.
type ForStmt struct {
	Cond         Expr
	Body         []Stmt
	Label        string
	ContinueStep []Stmt
}

// ForRange is a Python for-in-range loop, the readable form a simple counted Go
// loop and a range over an integer both lower to. Var is the loop variable, left
// empty when the Go program ranged for the count alone, in which case the emitter
// spends the throwaway name. Start is the first value, nil when it is an implicit
// zero so the emitter writes range(Stop) rather than range(0, Stop). Step is the
// stride, nil for the default of one. The lowering only builds this node when it
// has proven the loop is a plain forward or backward count, so the Python range
// walks exactly the values Go's loop variable would take.
type ForRange struct {
	Var   string
	Start Expr
	Stop  Expr
	Step  Expr
	Body  []Stmt
	Label string
}

// Break leaves the innermost enclosing loop, matching Go's unlabeled break; it
// carries nothing because the target is always that innermost loop.
type Break struct{}

// Continue advances the innermost enclosing loop to its next iteration, matching
// Go's unlabeled continue. When the loop is a while form with a step at the
// bottom of its body, the lowering runs that step before the continue, so the
// loop still advances the way Go's for does.
type Continue struct{}

// RangeString is a range over a string, which iterates runes and yields the
// byte index of each rune's start and the decoded rune, matching Go's for range
// over a string. Key is the byte-index variable and Value is the rune variable,
// either empty when the Go program used the blank identifier or omitted the
// clause. Cursor and Width are the internal byte cursor and rune-width names the
// lowering allocates, kept distinct from user names so a nested range does not
// collide. Source is bound once by the lowering so it is evaluated a single time.
type RangeString struct {
	Key    string
	Value  string
	Cursor string
	Width  string
	Source Expr
	Body   []Stmt
	Label  string
	// ContinueStep is the cursor advance a continue owes this loop, which the
	// labeled-continue pass runs before a continue it injects here. The emitter
	// writes its own copy at the bottom of the loop for a normal iteration, so it
	// ignores this field.
	ContinueStep []Stmt
}

// SetField assigns to a struct field, obj.Name = Value, matching a Go field
// assignment. Object is the struct instance expression.
type SetField struct {
	Object Expr
	Name   string
	Value  Expr
}

// LabeledBreak marks a Go break that names an outer loop. It is a transient node
// the lowering emits at the break site and the labeled-break pass rewrites away
// into a flag set and a plain break before the module is verified, so it never
// reaches the emitter; both the verifier and the emitter reject one that leaks.
type LabeledBreak struct{ Label string }

// LabeledContinue marks a Go continue that names an outer loop. Like LabeledBreak
// it is transient: the labeled-jump pass rewrites it into a flag that breaks the
// inner loops and continues the named loop, running that loop's step first, all
// before the module is verified, so it never reaches the emitter and both the
// verifier and the emitter reject one that leaks.
type LabeledContinue struct{ Label string }

func (*ExprStmt) isStmt()        {}
func (*ReturnStmt) isStmt()      {}
func (*AssignStmt) isStmt()      {}
func (*SetField) isStmt()        {}
func (*IfStmt) isStmt()          {}
func (*ForStmt) isStmt()         {}
func (*ForRange) isStmt()        {}
func (*Break) isStmt()           {}
func (*Continue) isStmt()        {}
func (*RangeString) isStmt()     {}
func (*LabeledBreak) isStmt()    {}
func (*LabeledContinue) isStmt() {}

// Expr is an expression node.
type Expr interface{ isExpr() }

// IntLit is an integer literal carried as its exact source text, so no width or
// precision is lost before the emitter decides how to render it.
type IntLit struct{ Text string }

// FloatLit is a floating-point literal carried as its exact source text, such
// as 0.1 or 1e10, which is already valid Python. A float32 literal is wrapped
// by the lowering in the single-precision helper, since Python's float is
// always 64-bit.
type FloatLit struct{ Text string }

// StringLit is a string literal carried as its decoded Go value, not the quoted
// source.
type StringLit struct{ Value string }

// BoolLit is a boolean literal.
type BoolLit struct{ Value bool }

// Ident is a reference to a bound name.
type Ident struct{ Name string }

// BinaryExpr is a binary operation. Op is the Go operator text, such as + or <.
type BinaryExpr struct {
	Op   string
	X, Y Expr
}

// UnaryExpr is a unary operation. Op is the Go operator text, such as - for
// negation.
type UnaryExpr struct {
	Op string
	X  Expr
}

// Mask wraps an expression in a fixed-width integer helper, so a growing
// operation on a sized Go integer wraps two's-complement the way Go does. Bits
// is the width (8, 16, 32, or 64) and Signed picks the signed or unsigned
// helper.
type Mask struct {
	Bits   int
	Signed bool
	X      Expr
}

// Convert wraps an expression in a Python builtin conversion, int or float,
// which is how a Go conversion between the number kinds lowers: int(x)
// truncates a float toward zero, float(x) widens an integer. The integer width
// mask, when the destination is a sized integer, is a separate Mask node around
// this one.
type Convert struct {
	To string
	X  Expr
}

// IndexExpr is an index into an indexable value, such as s[i] on a string.
// Because a Go string is represented as Python bytes, indexing one yields an int
// 0-255, exactly like Go's byte.
type IndexExpr struct {
	X     Expr
	Index Expr
}

// CallExpr is a call to a named function within the module.
type CallExpr struct {
	Name string
	Args []Expr
}

// FieldAccess reads a field of a struct value, obj.Name. The read alone does not
// copy; the copy at a value read is a separate Clone the lowering wraps around
// this node where Go's value semantics demand an independent value.
type FieldAccess struct {
	X    Expr
	Name string
}

// StructLit constructs a struct instance through its generated constructor. When
// Keyed is true the fields carry names and emit as keyword arguments Name=Value,
// matching a keyed Go literal, so an omitted field takes the constructor's zero
// default; otherwise the fields are positional, matching a positional literal. An
// empty Fields list builds the zero value.
type StructLit struct {
	Type   string
	Keyed  bool
	Fields []StructArg
}

// StructArg is one argument of a struct constructor call. Name is set only for a
// keyed literal, where it is the field name.
type StructArg struct {
	Name  string
	Value Expr
}

// Clone copies a struct value by calling its generated copy method, emitted at a
// site where Go performs a value copy of a struct: an assignment, or a read of a
// struct-typed field that yields a value.
type Clone struct{ X Expr }

// Intrinsic is a call the runtime provides rather than user code, such as the
// println path that fmt.Println lowers to. Keeping these explicit lets the
// emitter route them to the shim without pattern-matching call targets.
type Intrinsic struct {
	Name string
	Args []Expr
}

func (*IntLit) isExpr()      {}
func (*FloatLit) isExpr()    {}
func (*StringLit) isExpr()   {}
func (*BoolLit) isExpr()     {}
func (*Ident) isExpr()       {}
func (*BinaryExpr) isExpr()  {}
func (*UnaryExpr) isExpr()   {}
func (*Mask) isExpr()        {}
func (*Convert) isExpr()     {}
func (*IndexExpr) isExpr()   {}
func (*CallExpr) isExpr()    {}
func (*Intrinsic) isExpr()   {}
func (*FieldAccess) isExpr() {}
func (*StructLit) isExpr()   {}
func (*Clone) isExpr()       {}
