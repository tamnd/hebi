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

func (*ExprStmt) isStmt()   {}
func (*AssignStmt) isStmt() {}
func (*IfStmt) isStmt()     {}
func (*ForStmt) isStmt()    {}

// Expr is an expression node.
type Expr interface{ isExpr() }

// IntLit is an integer literal carried as its exact source text, so no width or
// precision is lost before the emitter decides how to render it.
type IntLit struct{ Text string }

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
func (*StringLit) isExpr()  {}
func (*BoolLit) isExpr()    {}
func (*Ident) isExpr()      {}
func (*BinaryExpr) isExpr() {}
func (*UnaryExpr) isExpr()  {}
func (*Mask) isExpr()       {}
func (*CallExpr) isExpr()   {}
func (*Intrinsic) isExpr()  {}
