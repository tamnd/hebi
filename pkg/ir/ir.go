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
	// Interfaces are the package's interface types in source order, each emitted
	// as a runtime-checkable Protocol class ahead of the structs and functions.
	Interfaces []*InterfaceDef
	// Named are the package's named non-struct types that carry methods, in source
	// order, each emitted as a Python subclass of the base its underlying basic type
	// takes so a value carries the type's methods and its Stringer.
	Named []*NamedDef
	// Funcs are the package's functions in source order.
	Funcs []*Func
}

// NamedDef is a named non-struct type with methods, such as type Duration int64,
// lowered to a Python class that subclasses the base its underlying basic type
// takes: int for an integer, float for a floating type, bytes for a string. A
// value of the type is boxed into an instance of this class so recv.M(args)
// dispatches the method and go_str finds a String or Error the type defines. The
// value still is-a its base, so arithmetic, comparison, and integer formatting
// read straight through, and the lowerer re-boxes a fresh arithmetic result whose
// static type is the named type so the method stays reachable.
type NamedDef struct {
	// Name is the named type's name, which becomes the class name.
	Name string
	// Base is the Python base class the values subclass: int, float, or bytes.
	Base string
	// Type is the package-qualified Go type name fmt's %T prints, such as
	// main.Duration, held on the class the way a struct carries _hebi_type.
	Type string
	// Methods are the type's methods in source order, each emitted as an instance
	// method the same way a struct method is.
	Methods []*Method
}

// InterfaceDef is a Go interface type lowered to a runtime-checkable Protocol.
// The Protocol documents the method set and answers a structural isinstance, but
// it does not take part in dispatch: hebi holds an interface value as the
// concrete Python object itself, so recv.M(args) dispatches straight on that
// object the way Go's dynamic dispatch does. Emitting the Protocol keeps the
// interface's shape visible in the source and is what the milestone's exit gate
// means by lowering interfaces to Protocols.
type InterfaceDef struct {
	// Name is the interface type name, which becomes the Protocol class name.
	Name string
	// Methods are the interface's methods, in the type checker's stable order, so
	// the emit is a pure function of the type. An embedded interface's methods are
	// already folded in here, so embedding needs no separate node.
	Methods []InterfaceMethod
}

// InterfaceMethod is one method of an interface's method set, lowered to a bare
// Protocol method whose body is an ellipsis. Params are synthetic positional
// names, since an interface signature need not name its parameters and the
// Protocol only needs the arity to read right.
type InterfaceMethod struct {
	// Name is the method name.
	Name string
	// Params are the positional parameter names after the receiver.
	Params []string
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
	// Methods are the type's methods in source order, emitted onto the class after
	// the generated constructor, copy, and equality methods. A Go method with a
	// value receiver operates on a copy, which the caller passes at the call site;
	// a pointer receiver operates on the instance, so both receiver kinds lower to
	// the same instance method here and the copy obligation lives at the call.
	Methods []*Method
}

// Method is one method of a struct, lowered to an instance method on the class.
// The Go receiver becomes self, whatever the source named it, so Params holds
// only the ordinary parameters after the receiver and the emitter prepends self.
type Method struct {
	// Name is the method name, which becomes the Python method name.
	Name string
	// Params are the ordinary parameter names in declaration order, after the
	// receiver.
	Params []string
	// Body is the ordered list of statements.
	Body []Stmt
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
	// FieldArray is an array field: a Go array is a value, so like a value struct
	// it defaults to a fresh zero array built in the constructor body and copies
	// element-wise through the array clone helper, never by sharing the list.
	FieldArray
	// FieldSync is a sync primitive field, a Mutex, RWMutex, WaitGroup, or Once:
	// its zero value is a fresh runtime object built in the constructor body, so
	// each instance owns its own, and copy shares the object since a sync value is
	// a reference the pointer receiver reaches and copying a used one is a Go vet
	// error the source would not contain.
	FieldSync
)

// StructField is one field of a struct. Zero is the constructor default for a
// scalar field, the Go zero value of its type, and for an array field the fresh
// zero array the constructor builds when the field is omitted. Struct is the
// field's struct type name for a value-struct field, used to build its zero
// instance and to recurse in copy.
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

// TupleAssign binds a tuple-valued expression to several names at once,
// Names = Value, matching a Go comma-ok read v, ok := m[k]. Define is true for a
// short declaration and false for a plain assignment, though Python draws no
// distinction so the emitter renders both as a tuple target. A blank name in
// Names is the throwaway, which Python allows as an ordinary target.
type TupleAssign struct {
	Names  []string
	Value  Expr
	Define bool
}

// RangeMap is a range over a map, for k, v := range m or for k := range m, which
// iterates a snapshot of the map's items or keys so a delete during the range is
// safe the way Go's is. Key is the key variable and Value is the value variable,
// either empty when the Go program used the blank identifier or omitted the
// clause; when Value is empty the loop ranges the keys alone. Source is the map,
// which the runtime snapshots and reads as empty when it is the nil map.
type RangeMap struct {
	Key    string
	Value  string
	Source Expr
	Body   []Stmt
	Label  string
}

// SetField assigns to a struct field, obj.Name = Value, matching a Go field
// assignment. Object is the struct instance expression.
type SetField struct {
	Object Expr
	Name   string
	Value  Expr
}

// SetIndex assigns to an element of an indexable value, Object[Index] = Value,
// matching a Go array element assignment. The value is cloned by the lowering
// where Go copies it, so storing a struct or array value into an element stores
// an independent copy.
type SetIndex struct {
	Object Expr
	Index  Expr
	Value  Expr
}

// DerefSet assigns through a pointer, *p = Value, which lowers to the pointer's
// set. The value is cloned by the lowering where Go copies it, so writing a
// struct or array value through the pointer stores an independent copy in the
// field or element it points at.
type DerefSet struct {
	Ptr   Expr
	Value Expr
}

// DeferBlock wraps the statements of a function that runs deferred calls. In its
// plain form it lowers to a _defers list, a try over Body, and a finally that
// runs the pushed calls in last-in-first-out order, matching Go's rule that a
// function's deferred calls run in reverse of the order they were deferred as the
// function returns.
//
// When Reshape is set the block instead lowers to a try over Body with an except
// that catches an escaping panic, runs the deferred calls, and re-raises only if
// none of them recovered, and each return in Body has already been rewritten to a
// DeferReturn that runs the deferred calls before it hands back Results. This is
// the shape a function needs when a deferred call reads or changes a named result
// or calls recover, since the deferred calls must run between the return's value
// assignment and the actual handing back of that value, which the plain finally
// cannot express. Results names the function's result variables, which the reshape
// initialises to their zero values before the try so a panic path can still return
// them.
type DeferBlock struct {
	Body    []Stmt
	Reshape bool
	Results []string
}

// Panic raises a Go panic carrying Value, which lowers to raising the runtime's
// GoPanic exception. The panic unwinds the Python stack the way it unwinds the
// goroutine stack, running deferred calls through each reshaped DeferBlock it
// passes and stopping only where a deferred call recovers it.
type Panic struct{ Value Expr }

// DeferReturn is the return of a reshaped deferring function: it runs the recorded
// deferred calls and then hands back the result variables. Results names those
// variables, empty for a function with no results, one name for a single result,
// or several for a multiple-return, and the emitter renders the handoff as a bare
// return, a single name, or a tuple. A plain return cannot express this because
// Python evaluates a return's expression before the finally runs, so a deferred
// call's change to a result would not be seen; assigning the results first and
// running the deferred calls here lets that change land before the value is read.
type DeferReturn struct{ Results []string }

// DeferPush records a deferred call at the point the defer statement runs, which
// lowers to appending the callable and its argument tuple to the _defers list.
// The arguments are the values captured at the defer site, so a later change to a
// variable is not seen by the deferred call, matching Go's evaluation of deferred
// arguments at the defer statement rather than at the call.
type DeferPush struct {
	Func Expr
	Args []Expr
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
func (*FuncDef) isStmt()         {}
func (*ReturnStmt) isStmt()      {}
func (*AssignStmt) isStmt()      {}
func (*TupleAssign) isStmt()     {}
func (*RangeMap) isStmt()        {}
func (*SetField) isStmt()        {}
func (*SetIndex) isStmt()        {}
func (*DerefSet) isStmt()        {}
func (*DeferBlock) isStmt()      {}
func (*DeferPush) isStmt()       {}
func (*DeferReturn) isStmt()     {}
func (*Panic) isStmt()           {}
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

// MethodCall is a call to a method on a receiver expression, recv.Name(args). The
// receiver is the struct instance the method binds to; a value-receiver call
// wraps the receiver in a Clone at the lowering so the method's mutations do not
// escape, and a pointer-receiver call passes the instance directly.
type MethodCall struct {
	Recv Expr
	Name string
	Args []Expr
}

// MethodValue binds a method to a receiver, recv.Name, yielding a Python bound
// method that carries the receiver. When Copy is set the method has a value
// receiver, so the receiver is snapshotted with a copy at the point the value is
// taken, recv.copy().Name, matching Go's copy of the receiver into the value; a
// pointer receiver keeps the shared instance, so Copy is clear.
type MethodValue struct {
	Recv Expr
	Name string
	Copy bool
}

// MethodExpr is an unbound method expression, T.Name, yielding the class function
// whose first argument is the receiver. When ValueCopy is set the method has a
// value receiver, so a lambda wraps the call to snapshot that first argument with
// a copy before the method runs, matching Go's copy of a value receiver; a pointer
// receiver takes the instance directly, so the bare class function is emitted.
type MethodExpr struct {
	Recv      string
	Name      string
	ValueCopy bool
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

// ArrayZero builds the zero value of a Go array [Len]T as a Python list. Elem is
// the zero value of one element, so a nested array carries an ArrayZero of its
// own and an array of structs carries a StructLit. ElemMutable tells the emitter
// which form keeps every element independent: a scalar element is immutable, so
// the list may repeat one value, while a struct or array element must be built
// fresh per position so the elements do not alias.
type ArrayZero struct {
	Len         int
	Elem        Expr
	ElemMutable bool
}

// ArrayLit builds a Go array literal as a Python list of its elements in order.
// A partial Go literal is padded to the array length with zero-value elements by
// the lowering, so Elems always holds exactly the array's length.
type ArrayLit struct{ Elems []Expr }

// ArrayClone copies a Go array value element-wise through the array clone helper,
// emitted at a site where Go performs a value copy of an array: an assignment,
// an argument, a return, or a read of an array-typed field or element that
// yields a value. The helper recurses so nested arrays and struct elements each
// become independent.
type ArrayClone struct{ X Expr }

// SliceLit builds a Go slice literal as a slice header over a fresh backing
// list of its elements in order, through the slice-literal helper. Unlike an
// array literal it is never padded, since a slice literal's length is exactly
// its element count, and a value element that reads an existing value is cloned
// by the lowering so the backing owns an independent copy.
type SliceLit struct{ Elems []Expr }

// SliceMake builds make([]T, len, cap) as a slice header over a freshly zeroed
// backing. Len and Cap are the length and capacity expressions, equal when the
// source gave no capacity. Elem is the zero value of one element and ElemMutable
// picks the emitted backing form: an immutable scalar element repeats one value,
// while a struct or array element is built fresh at each slot so the elements do
// not alias.
type SliceMake struct {
	Len         Expr
	Cap         Expr
	Elem        Expr
	ElemMutable bool
}

// SliceExpr is a slice expression s[Low:High] or the full form s[Low:High:Max],
// which builds a new slice header sharing the operand's backing rather than
// copying it, so the result aliases the operand the way a Go reslice does. Low or
// High is nil when the source omitted that bound, and the two-index emitter leaves
// the corresponding side of the Python slice empty. Max is nil for the two-index
// form; when it is set the expression is the full slice, which caps the result's
// reserved capacity explicitly, and the emitter routes it through the runtime
// since Python's slice syntax carries no third bound.
type SliceExpr struct {
	X    Expr
	Low  Expr
	High Expr
	Max  Expr
}

// NilSlice is the nil slice sentinel, the zero value a slice variable or a
// slice-typed struct field takes. It is the runtime's shared empty header, whose
// length and capacity are zero, so it never aliases live data and is safe to
// share, and an append to it allocates a fresh backing.
type NilSlice struct{}

// MapLit builds a Go map literal or a make(map) as a Python dict, {k: v, ...},
// with the entries in source order. An empty Entries list builds an empty dict,
// the form both map[K]V{} and make(map[K]V) take, distinct from the nil map. A
// value entry that reads an existing value is cloned by the lowering so the map
// owns an independent copy.
type MapLit struct{ Entries []MapEntry }

// MapEntry is one key-value pair of a map literal.
type MapEntry struct {
	Key   Expr
	Value Expr
}

// NilMap is the nil map sentinel, the zero value a map variable or a map-typed
// struct field takes. It is the runtime's shared empty map, which reads as empty
// and yields no iterations, and panics with Go's "assignment to entry in nil map"
// on any write, so a read-only use is fine and a write is a panic, exactly as Go.
type NilMap struct{}

// NilPtr is the nil pointer sentinel, the zero value a pointer variable or a
// pointer-typed struct field takes. It compares equal only to itself, so p == nil
// is an identity test, and a dereference raises Go's nil pointer panic, so a read
// through it stops the Go way rather than as a Python AttributeError.
type NilPtr struct{}

// NilInterface is the nil interface value, the zero value an interface variable
// such as an error takes and the value a bare nil returns into an interface
// result. It lowers to Python None, so a nil check reads as err is None and a
// success return reads as return v, None, which is why an interface compared to
// nil uses identity rather than the sentinel equality a pointer or slice uses.
type NilInterface struct{}

// EmptyStruct is the value of an empty struct type, struct{}. An empty struct
// carries no fields, so every value of it is equal and holds nothing, which lets
// it lower to Python's empty tuple: immutable, hashable, and cheap to share. It
// is the value make(chan struct{}) sends and struct{}{} constructs, so a signal
// channel needs no per-send allocation.
type EmptyStruct struct{}

// Intrinsic is a call the runtime provides rather than user code, such as the
// println path that fmt.Println lowers to. Keeping these explicit lets the
// emitter route them to the shim without pattern-matching call targets.
type Intrinsic struct {
	Name string
	Args []Expr
}

// ShimFunc is a bare reference to a runtime function, rather than a call to it. A
// deferred fmt.Println captures the println callable itself so the finally can
// invoke it later, so the value is the shim function without its arguments.
type ShimFunc struct{ Name string }

// AddrField is the address of a struct field, &s.Field. It lowers to a FieldPtr
// over the struct object and the field name, which reads and writes the live
// field, so a write through the pointer is visible in the struct. Container is
// the object holding the field, which for a promoted field is the embedded value
// rather than the outer struct.
type AddrField struct {
	Container Expr
	Name      string
}

// AddrIndex is the address of an array or slice element, &a[i]. It lowers to an
// IndexPtr over the container and the index, which reads and writes the live
// element, so a write through the pointer is visible in the array or slice.
type AddrIndex struct {
	Seq   Expr
	Index Expr
}

// Deref reads through a pointer, *p, which lowers to the pointer's get. The
// pointer is a FieldPtr or IndexPtr, so the read goes through to the live field
// or element it points at.
type Deref struct{ X Expr }

// Tuple is a parenthesized sequence of expressions, which lowers to a Python
// tuple. It carries a function's multiple return values, `return a, b`, and the
// right side of a parallel assignment, `a, b = c, d`, so both flow through one
// node the emitter renders as `(a, b)`.
type Tuple struct{ Elems []Expr }

// Capture is one variable a closure snapshots at the point it is created, lowered
// to a default-argument parameter, `name=value`. The reuse-name form, where the
// parameter and its value share the source name, gives Go 1.22's per-iteration
// loop variable: the default binds the current value once, so the closure keeps
// the value the variable held when the closure was made rather than the value it
// ends on.
type Capture struct {
	// Param is the default-argument parameter name the closure body reads.
	Param string
	// Value is the expression evaluated once, at closure creation, to seed Param.
	Value Expr
}

// Lambda is a single-expression closure, the form a Go function literal whose
// body is one `return expr` lowers to. Params are the literal's parameters in
// order, Captures are the snapshot defaults appended after them, and Body is the
// returned expression. A closure whose body is more than one statement lowers to
// a FuncDef instead, since a Python lambda holds only an expression.
type Lambda struct {
	Params   []string
	Captures []Capture
	Body     Expr
}

// FuncDef is a multi-statement closure, hoisted to a nested Python def just above
// the statement that creates it. Params and Captures mirror Lambda's. Nonlocals
// are the enclosing locals the body assigns, declared nonlocal on the def's first
// line so the write reaches the outer binding rather than making a fresh local,
// matching Go's capture by reference. Body is the def's statement list.
type FuncDef struct {
	Name      string
	Params    []string
	Captures  []Capture
	Nonlocals []string
	Body      []Stmt
}

func (*IntLit) isExpr()       {}
func (*FloatLit) isExpr()     {}
func (*StringLit) isExpr()    {}
func (*BoolLit) isExpr()      {}
func (*Ident) isExpr()        {}
func (*BinaryExpr) isExpr()   {}
func (*UnaryExpr) isExpr()    {}
func (*Mask) isExpr()         {}
func (*Convert) isExpr()      {}
func (*IndexExpr) isExpr()    {}
func (*CallExpr) isExpr()     {}
func (*MethodCall) isExpr()   {}
func (*MethodValue) isExpr()  {}
func (*MethodExpr) isExpr()   {}
func (*Intrinsic) isExpr()    {}
func (*ShimFunc) isExpr()     {}
func (*AddrField) isExpr()    {}
func (*AddrIndex) isExpr()    {}
func (*Deref) isExpr()        {}
func (*Tuple) isExpr()        {}
func (*Lambda) isExpr()       {}
func (*FieldAccess) isExpr()  {}
func (*StructLit) isExpr()    {}
func (*Clone) isExpr()        {}
func (*ArrayZero) isExpr()    {}
func (*ArrayLit) isExpr()     {}
func (*ArrayClone) isExpr()   {}
func (*SliceLit) isExpr()     {}
func (*SliceMake) isExpr()    {}
func (*SliceExpr) isExpr()    {}
func (*NilSlice) isExpr()     {}
func (*MapLit) isExpr()       {}
func (*NilMap) isExpr()       {}
func (*NilPtr) isExpr()       {}
func (*NilInterface) isExpr() {}
func (*EmptyStruct) isExpr()  {}
