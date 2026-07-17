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
	// Funcs are the package's functions in source order.
	Funcs []*Func
}

// Func is a function definition. At M0 functions take no parameters and return
// nothing; parameters and returns arrive in M3.
type Func struct {
	// Name is the function name.
	Name string
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

// IfStmt is a conditional. Else may be empty.
type IfStmt struct {
	Cond Expr
	Then []Stmt
	Else []Stmt
}

// ForStmt is a for loop lowered to the while form. A nil Cond is an unconditional
// loop, matching Go's bare for.
type ForStmt struct {
	Cond Expr
	Body []Stmt
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
}

func (*ExprStmt) isStmt()    {}
func (*AssignStmt) isStmt()  {}
func (*IfStmt) isStmt()      {}
func (*ForStmt) isStmt()     {}
func (*ForRange) isStmt()    {}
func (*Break) isStmt()       {}
func (*Continue) isStmt()    {}
func (*RangeString) isStmt() {}

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

// Intrinsic is a call the runtime provides rather than user code, such as the
// println path that fmt.Println lowers to. Keeping these explicit lets the
// emitter route them to the shim without pattern-matching call targets.
type Intrinsic struct {
	Name string
	Args []Expr
}

func (*IntLit) isExpr()     {}
func (*FloatLit) isExpr()   {}
func (*StringLit) isExpr()  {}
func (*BoolLit) isExpr()    {}
func (*Ident) isExpr()      {}
func (*BinaryExpr) isExpr() {}
func (*UnaryExpr) isExpr()  {}
func (*Mask) isExpr()       {}
func (*Convert) isExpr()    {}
func (*IndexExpr) isExpr()  {}
func (*CallExpr) isExpr()   {}
func (*Intrinsic) isExpr()  {}
