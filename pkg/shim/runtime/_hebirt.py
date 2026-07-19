"""hebi compiled-tier runtime shim.

Emitted Python imports this module for the small pieces of Go's object model
that Python does not provide directly. At M0 that is Go-style value formatting
and the println path that fmt.Println lowers to. The module grows one helper at
a time as later milestones add language surface.
"""

import decimal
import functools
import math
import os
import random
import struct
import sys
import threading
import time


def go_str(value, plus=False):
    """Return Go's fmt default string for a value, %v by default and %+v when plus.

    Go prints booleans as true and false where Python prints True and False, so
    those two are special-cased. Floats are formatted the Go way, which differs
    from Python's str: Go uses the shortest round-tripping form and switches to
    exponent notation at different thresholds. A struct dumps its fields in
    braces, and under plus each field is labelled with its name, the difference
    between %v and %+v. Everything else defers to Python's str, which already
    matches Go for the integers hebi covers.
    """
    if value is True:
        return "true"
    if value is False:
        return "false"
    if value is None:
        # A nil interface, the one nil that lowers to Python None, prints as <nil>,
        # the same text Go's fmt writes for a nil error or a nil interface value.
        return "<nil>"
    if isinstance(value, float):
        return _gofloat(value)
    if isinstance(value, bytes):
        # A Go string is bytes internally, and printing it writes its text, so
        # the UTF-8 is decoded back to a str, with the replacement character for
        # any invalid byte, matching Go's decode. A []byte is a bytearray, not
        # bytes, so it does not take this path and still prints as its ints.
        return value.decode("utf-8", "replace")
    if isinstance(value, list):
        # A Go array prints as its elements in brackets separated by single
        # spaces, each element formatted by its own rule, and a nested array
        # recurses, so [2][2]int reads as [[1 2] [3 4]] the way Go prints it.
        return "[" + " ".join(go_str(e, plus) for e in value) + "]"
    if isinstance(value, Slice):
        # A Go slice prints the same bracket form as an array, walking the header
        # so only the visible length is shown and the backing beyond it stays
        # hidden, matching fmt's view of a slice through its length.
        return "[" + " ".join(go_str(value.array[value.offset + k], plus) for k in range(value.length)) + "]"
    if value is NIL_MAP:
        # A nil map prints as an empty map, the same form an empty map takes, since
        # Go's fmt shows both as map[].
        return "map[]"
    if isinstance(value, dict):
        # A Go map prints as map[k:v ...] with its keys sorted, which fmt does so
        # the output is stable, so the entries are sorted by key where the key type
        # orders; a key type Python cannot order (a struct) falls back to insertion
        # order, the one map-print case that is not a fidelity target.
        try:
            items = sorted(value.items(), key=lambda kv: kv[0])
        except TypeError:
            items = list(value.items())
        return "map[" + " ".join(go_str(k, plus) + ":" + go_str(v, plus) for k, v in items) + "]"
    err = getattr(value, "Error", None)
    if callable(err):
        # An error value prints as its Error text, so fmt.Println(err) writes the
        # message the way Go does; the result is a Go string, so it decodes through
        # go_str the same as any other string.
        return go_str(err())
    s = getattr(value, "String", None)
    if callable(s):
        # A Stringer prints through String, which fmt uses when a value carries no
        # Error, so a type that defines String reads as its own text.
        return go_str(s())
    slots = getattr(type(value), "__slots__", None)
    if slots is not None and hasattr(type(value), "_hebi_type"):
        # A struct that carries no Stringer or error dumps its fields in braces,
        # each field rendered by its own rule and separated by single spaces. Under
        # plus each field is prefixed with its name and a colon, so %v reads {1 2}
        # and %+v reads {X:1 Y:2}, matching Go's default struct formats.
        if plus:
            body = " ".join(name + ":" + go_str(getattr(value, name), True) for name in slots)
        else:
            body = " ".join(go_str(getattr(value, name), False) for name in slots)
        return "{" + body + "}"
    return str(value)


def println(*args):
    """Match fmt.Println: operands joined by single spaces, then a newline."""
    print(" ".join(go_str(a) for a in args))


# Fixed-width integer helpers. Go integers wrap two's-complement at their
# declared width and Python integers do not, so the emitter wraps every growing
# operation on a sized integer in the matching helper. The unsigned helper
# masks; the signed helper masks then sign-extends by subtracting 2**N when the
# top bit is set.


def _u8(v):
    return v & 0xFF


def _u16(v):
    return v & 0xFFFF


def _u32(v):
    return v & 0xFFFFFFFF


def _u64(v):
    return v & 0xFFFFFFFFFFFFFFFF


def _i8(v):
    v &= 0xFF
    return v - 0x100 if v >= 0x80 else v


def _i16(v):
    v &= 0xFFFF
    return v - 0x10000 if v >= 0x8000 else v


def _i32(v):
    v &= 0xFFFFFFFF
    return v - 0x100000000 if v >= 0x80000000 else v


def _i64(v):
    v &= 0xFFFFFFFFFFFFFFFF
    return v - 0x10000000000000000 if v >= 0x8000000000000000 else v


# Integer division and remainder. Go truncates a quotient toward zero and gives a
# remainder the sign of the dividend, where Python's // and % floor toward minus
# infinity, so a signed operand routes through these helpers rather than the bare
# operators. A zero divisor panics the Go way through _runtime_error so a recover
# sees "integer divide by zero" instead of Python's ZeroDivisionError.


def _idiv(a, b):
    if b == 0:
        raise _runtime_error("integer divide by zero")
    q = abs(a) // abs(b)
    return -q if (a < 0) != (b < 0) else q


def _imod(a, b):
    if b == 0:
        raise _runtime_error("integer divide by zero")
    return a - _idiv(a, b) * b


def _quo(a, b):
    """Division for an erased type parameter whose constraint mixes integer and
    float: integer operands truncate the Go way and any float operand divides by
    IEEE rules, the choice the single erased definition cannot make statically."""
    if isinstance(a, int) and isinstance(b, int):
        return _idiv(a, b)
    return _fdiv(a, b)


# Float helpers. Go float64 is Python's float, so float64 arithmetic is native,
# but Go float32 is single precision and Python has no 32-bit float, so a
# float32 result is round-tripped back through a 4-byte IEEE single after every
# producing operation, the float analog of the integer width masks. Formatting
# also needs care: fmt prints a float with the shortest round-tripping decimal
# and switches to exponent notation at thresholds that differ from Python's.


def _f32(v):
    """Round a value to IEEE single precision, matching Go's float32."""
    return struct.unpack("f", struct.pack("f", v))[0]


def _fdiv(a, b):
    """Go float division, which never raises: a zero divisor yields signed infinity,
    or NaN when the dividend is also zero or NaN, where Python would raise instead."""
    if b == 0.0:
        if a == 0.0 or a != a:
            return float("nan")
        return math.copysign(float("inf"), a) * math.copysign(1.0, b)
    return a / b


def _gofloat(v):
    """Format a float64 the way fmt does, that is strconv 'g' with -1 precision."""
    return _goformat(v, repr(v))


def _gofloat32(v):
    """Format a float32 value, whose shortest decimal is found at single precision.

    The value is already narrowed to float32, so the shortest decimal is the
    fewest digits that round back to it under single precision, which is what Go
    prints for a float32.
    """
    if v == v and v not in (float("inf"), float("-inf")):
        for prec in range(1, 10):
            candidate = "%.*g" % (prec, v)
            if _f32(float(candidate)) == v:
                return _goformat(v, candidate)
    return _goformat(v, repr(v))


def _goformat(v, shortest):
    """Render v Go's way given its shortest decimal string.

    The special values print as Go's NaN, +Inf, and -Inf. A finite value is
    split into its significant digits and decimal exponent, then printed in
    fixed notation unless the scientific exponent is below -4 or at least 6,
    which is the threshold fmt uses for a shortest float, in which case Go uses
    exponent notation.
    """
    if v != v:
        return "NaN"
    if v == float("inf"):
        return "+Inf"
    if v == float("-inf"):
        return "-Inf"
    sign, digits, exp = decimal.Decimal(shortest).normalize().as_tuple()
    ds = "".join(str(d) for d in digits)
    sciexp = exp + len(ds) - 1
    if sciexp < -4 or sciexp >= 6:
        body = _goexp(ds, sciexp)
    else:
        body = _gofixed(ds, exp)
    return "-" + body if sign else body


def _goexp(ds, sciexp):
    """Exponent notation: one leading digit, a signed two-or-more-digit exponent."""
    mantissa = ds if len(ds) == 1 else ds[0] + "." + ds[1:]
    return "%se%s%02d" % (mantissa, "+" if sciexp >= 0 else "-", abs(sciexp))


def _gofixed(ds, exp):
    """Fixed notation from significant digits and a base-ten exponent."""
    if exp >= 0:
        return ds + "0" * exp
    point = len(ds) + exp
    if point > 0:
        return ds[:point] + "." + ds[point:]
    return "0." + "0" * -point + ds


# Array helpers. A Go array is a value type, represented as a plain Python list
# that the copy machinery always clones, so it never aliases the way a slice
# does. Cloning recurses so nested arrays and struct elements each become
# independent, while an immutable scalar element is shared safely. The array-key
# helper projects an array to a hashable tuple, since a struct that contains a
# comparable array is a valid Go map key but a Python list is not hashable.


def _clone_array(a):
    """Deep-copy a Go array value element by element.

    A nested array recurses, a struct element copies through its own copy method,
    and a scalar element is shared because it is immutable, which together
    reproduce Go's value copy of an array at every copy site.
    """
    out = []
    for e in a:
        if isinstance(e, list):
            out.append(_clone_array(e))
        elif hasattr(e, "copy"):
            out.append(e.copy())
        else:
            out.append(e)
    return out


def _arraykey(a):
    """Return a hashable form of a Go array value for use as a map key.

    A nested array recurses to a tuple, and a struct element is already hashable
    through its own __hash__, so the whole array becomes a tuple of hashables.
    """
    return tuple(_arraykey(e) if isinstance(e, list) else e for e in a)


# Slice helpers. A Go slice is not a plain list but a header over a shared
# backing store: a backing array, an offset into it, a length, and a capacity.
# Two headers over the same backing alias, so a write through one is visible
# through the other, which a list copy would silently break, so sub-slicing
# returns a new header sharing the backing rather than a copied list. Indexing
# reads through the offset and is bounds-checked against the length, while
# sub-slicing is bounds-checked against the capacity, matching Go's reslice into
# reserved capacity. append writes into the shared backing while there is spare
# capacity and reallocates onto a fresh backing once it is full, which is where an
# appended-to slice stops aliasing the slices it grew from. copy moves elements
# between two slices and handles an overlapping backing the way Go's memmove does.


class Slice:
    """A Go slice header: a shared backing array, an offset, a length, and a cap.

    Indexing goes through the offset and is checked against the length, so a
    header with offset 3 reads array[3 + i], len(s) is the length, and cap(s) is
    the cap. A slice argument to the subscript is a sub-slice, which builds a new
    header sharing this backing rather than copying the list, which is what makes
    two slices over one backing alias the way Go's do.
    """

    __slots__ = ("array", "offset", "length", "cap")

    def __init__(self, array, offset, length, cap):
        self.array = array
        self.offset = offset
        self.length = length
        self.cap = cap

    def __getitem__(self, i):
        if isinstance(i, slice):
            return _subslice(self, i)
        if i < 0 or i >= self.length:
            raise _runtime_error("index out of range [%d] with length %d" % (i, self.length))
        return self.array[self.offset + i]

    def __setitem__(self, i, v):
        if i < 0 or i >= self.length:
            raise _runtime_error("index out of range [%d] with length %d" % (i, self.length))
        self.array[self.offset + i] = v

    def __len__(self):
        return self.length


def _slice_lit(backing):
    """Build a slice header over a fresh backing list, len and cap its length.

    A slice literal owns its backing, so the header spans the whole list with no
    spare capacity, exactly as a Go slice literal starts life full.
    """
    return Slice(backing, 0, len(backing), len(backing))


def _subslice(s, sl):
    """Return the header s[low:high] shares with s, offset and length adjusted.

    The bounds are checked against the capacity, not the length, because Go lets
    a reslice reach up to cap(s) into the reserved backing, and the new header's
    capacity runs from low to the end of the original capacity, so a later append
    knows how far it may still write before it must reallocate.
    """
    low = 0 if sl.start is None else sl.start
    high = s.length if sl.stop is None else sl.stop
    if low < 0 or high > s.cap or low > high:
        raise _runtime_error("slice bounds out of range")
    return Slice(s.array, s.offset + low, high - low, s.cap - low)


def _subslice3(s, low, high, max):
    """Return the full slice s[low:high:max], its capacity capped at max - low.

    The three-index form sets the capacity explicitly rather than running it to the
    end of the backing, which is how Go bounds a later append: the returned header
    can reach only up to max before an append must reallocate. The bounds are
    checked the Go way, low through high through max, with max itself no larger than
    the operand's capacity, and any violation raises a bounds panic.
    """
    if low < 0 or high < low or max < high or max > s.cap:
        raise _runtime_error("slice bounds out of range")
    return Slice(s.array, s.offset + low, high - low, max - low)


# The nil slice is a distinct empty header with capacity zero: len and cap are
# zero, indexing panics, and append to it allocates a fresh backing, so a slice
# built up from a nil zero value grows the way Go's does. It is the zero value a
# slice variable or a slice-typed struct field takes, shared safely because it is
# never mutated in place.
NIL_SLICE = Slice([], 0, 0, 0)


def _slice_append(s, *vals):
    """Return s with vals appended, Go's way, sharing or reallocating the backing.

    While the new length still fits the capacity the values go into the existing
    backing and the returned header shares it with every other slice over it, so a
    later write through any of them is visible through the rest, matching Go's
    in-place append. Once the length would exceed the capacity a fresh, larger
    backing is allocated and the elements copied into it, so the returned header no
    longer aliases the slices it grew from, which is the reallocation programs feel
    when an append silently un-shares a backing. The nil slice has capacity zero,
    so an append to it always reallocates and returns a fresh non-nil slice.
    """
    n = s.length + len(vals)
    if n <= s.cap:
        arr = s.array
        base = s.offset + s.length
        for k, v in enumerate(vals):
            arr[base + k] = v
        return Slice(arr, s.offset, n, s.cap)
    newcap = _grow(s.cap, n)
    newarr = [None] * newcap
    for k in range(s.length):
        newarr[k] = s.array[s.offset + k]
    for k, v in enumerate(vals):
        newarr[s.length + k] = v
    return Slice(newarr, 0, n, newcap)


def _grow(oldcap, needed):
    """Return the capacity Go grows a slice to when an append overflows it.

    Go doubles the capacity while the slice is small and eases toward growing it
    by a quarter once it is large, so a run of appends amortizes to constant time.
    A single append that needs far more than double jumps straight to the needed
    length. This reproduces the growth curve of the pinned Go toolchain; the final
    rounding to a memory size class, which only shifts cap(s) by a few elements, is
    left to the fidelity a fixture pins, per the compatibility ledger.
    """
    if needed > 2 * oldcap:
        return needed
    if oldcap < 256:
        doubled = 2 * oldcap
        return doubled if doubled >= needed else needed
    newcap = oldcap
    while newcap < needed:
        newcap += (newcap + 3 * 256) >> 2
    return newcap


def _slice_copy(dst, src):
    """Copy min(len(dst), len(src)) elements from src into dst and return the count.

    Go's copy moves as many elements as the shorter slice holds, and it handles a
    source and destination that overlap in the same backing as if through a
    temporary, so copying a slice onto a later position of itself does not clobber
    a source element before it is read. When the two share a backing and the
    destination starts after the source the copy runs backward to preserve that,
    matching Go's memmove.
    """
    n = dst.length if dst.length < src.length else src.length
    if dst.array is src.array and dst.offset > src.offset:
        for k in range(n - 1, -1, -1):
            dst.array[dst.offset + k] = src.array[src.offset + k]
    else:
        for k in range(n):
            dst.array[dst.offset + k] = src.array[src.offset + k]
    return n


# Panic machinery. Go has two failure mechanisms: an error is an ordinary value a
# function returns, and a panic is a stack-unwinding event that runs deferred
# calls on the way out and is caught only by recover inside one. An error stays a
# value in emitted Python, but a panic is the one thing that unwinds a stack, so
# it lowers to a raised exception, GoPanic, whose propagation up the Python stack
# mirrors the panic's up the goroutine stack.


class GoPanic(BaseException):
    """A Go panic in flight, carrying the panicked value verbatim.

    It derives from BaseException, not Exception, so a stray except Exception in
    hand-written Python that wraps compiled code does not swallow a Go panic, the
    same reasoning that puts KeyboardInterrupt and SystemExit under BaseException.
    The value is whatever Go panicked with, handed back unchanged by recover, and
    the recovered flag lets a function's deferred-call harness tell a consumed
    panic from one still unwinding.
    """

    __slots__ = ("value", "recovered")

    def __init__(self, value):
        super().__init__(value)
        self.value = value
        self.recovered = False

    def __str__(self):
        return panic_message(self.value)


class _RuntimeError:
    """A Go runtime error, the value a runtime panic carries.

    Its message reads "runtime error: " followed by the specific failure, exactly
    as Go's runtime.Error values print, so a recover sees a message that matches
    the oracle and an unrecovered panic renders the same banner line go run does.
    """

    __slots__ = ("_msg",)

    def __init__(self, msg):
        self._msg = msg

    def Error(self):
        return "runtime error: " + self._msg

    def __str__(self):
        return self.Error()


class _PlainError:
    """A runtime panic whose message carries no "runtime error:" prefix.

    A few runtime panics, the nil map assignment among them, are plain-string
    panics rather than runtime.Error values, so Go prints the bare message, which
    this type reproduces for those cases.
    """

    __slots__ = ("_msg",)

    def __init__(self, msg):
        self._msg = msg

    def Error(self):
        return self._msg

    def __str__(self):
        return self._msg


class PanicNilError:
    """The error Go 1.21 panics with for panic(nil).

    Since Go 1.21 panic(nil) panics with a *runtime.PanicNilError rather than a
    literal nil, so recover always returns non-nil when a panic occurred. Under an
    older go.mod hebi panics with None instead, reproducing the old ambiguity for
    a module that opted into it.
    """

    __slots__ = ()

    def Error(self):
        return "panic called with nil argument"

    def __str__(self):
        return self.Error()


class TypeAssertionError:
    """Go's *runtime.TypeAssertionError, the value a failed type assertion panics with.

    Its Error text reads "interface conversion: ..." the way Go's does, so a
    recovered assertion failure hands back the message go run prints. The message
    is a Go string, carried as bytes like every other, so Error returns the bytes
    and go_str decodes them when the value is printed.
    """

    __slots__ = ("_msg",)

    def __init__(self, msg):
        self._msg = msg

    def Error(self):
        return self._msg

    def __str__(self):
        return go_str(self._msg)


class _StringError:
    """The error value errors.New returns, a string wrapped as an error.

    Go's errors.New builds an *errorString whose Error returns the string it was
    given, and each call yields a distinct pointer so two errors built from the
    same text are never equal. This mirrors that: the message is a Go string, so
    it is carried as bytes and Error hands it back unchanged, and equality is the
    default identity Python gives an object, so two _StringError values compare
    equal only when they are the same instance, matching Go's pointer errors.
    """

    __slots__ = ("_msg",)

    def __init__(self, msg):
        self._msg = msg

    def Error(self):
        return self._msg

    def __str__(self):
        return go_str(self._msg)


def errors_new(msg):
    """Build the error value errors.New returns from a Go string."""
    return _StringError(msg)


class _WrapError:
    """The error fmt.Errorf returns when the format holds one %w verb.

    It carries the combined message and the single wrapped error, and its Unwrap
    hands that error back so errors.Unwrap, errors.Is, and errors.As can walk to
    it. The message is a Go string, so it is bytes, the same as every other error.
    """

    __slots__ = ("_msg", "_err")

    def __init__(self, msg, err):
        self._msg = msg
        self._err = err

    def Error(self):
        return self._msg

    def Unwrap(self):
        return self._err

    def __str__(self):
        return go_str(self._msg)


class _WrapErrors:
    """The error fmt.Errorf returns when the format holds more than one %w verb.

    Its Unwrap returns the list of wrapped errors, the multi-error shape Go added
    in 1.20, so errors.Is and errors.As walk every branch while the single-level
    errors.Unwrap does not follow it.
    """

    __slots__ = ("_msg", "_errs")

    def __init__(self, msg, errs):
        self._msg = msg
        self._errs = errs

    def Error(self):
        return self._msg

    def Unwrap(self):
        return list(self._errs)

    def __str__(self):
        return go_str(self._msg)


class _JoinError:
    """The error errors.Join returns, wrapping a list of non-nil errors.

    Its message is the wrapped errors' messages joined by newlines, and its Unwrap
    returns the list, the same multi-error shape _WrapErrors carries, so the chain
    walk recurses into every branch.
    """

    __slots__ = ("_errs",)

    def __init__(self, errs):
        self._errs = errs

    def Error(self):
        return b"\n".join(_error_bytes(e) for e in self._errs)

    def Unwrap(self):
        return list(self._errs)

    def __str__(self):
        return go_str(self.Error())


def _error_bytes(err):
    """Return an error value's Error text as bytes, the Go string it renders to."""
    m = err.Error()
    if isinstance(m, str):
        return m.encode("utf-8")
    return bytes(m)


# fmt.Errorf formatting. The verb set the lowerer admits is v, s, d, t, w, and q;
# every one but q renders exactly as go_str already does, since go_str is Go's
# default format, so the formatter routes those through it and handles q, Go's
# double-quoted string form, on its own. The lowerer has already checked the
# format is a constant whose verbs are all in this set, so no unknown verb reaches
# here.

_quote_escapes = {
    0x07: b"\\a",
    0x08: b"\\b",
    0x09: b"\\t",
    0x0A: b"\\n",
    0x0B: b"\\v",
    0x0C: b"\\f",
    0x0D: b"\\r",
    0x22: b'\\"',
    0x5C: b"\\\\",
}


def _go_quote(value):
    """Render a Go string, carried as bytes, in Go's %q double-quoted form.

    Each byte takes its escape where Go escapes it, a printable ASCII byte prints
    as itself, and any other byte prints as a \\x hex escape, which matches
    strconv.Quote over the ASCII strings the error messages use.
    """
    out = bytearray(b'"')
    for b in value:
        esc = _quote_escapes.get(b)
        if esc is not None:
            out += esc
        elif 0x20 <= b < 0x7F:
            out.append(b)
        else:
            out += b"\\x%02x" % b
    out += b'"'
    return bytes(out)


def errorf(fmt, *args):
    """Build the error fmt.Errorf returns, formatting the message and recording %w.

    The format is scanned once: a %w renders its operand like %v and also records
    the operand as wrapped, and the other verbs render through go_str or the quote
    helper. No %w yields a plain string error, one yields a single-wrap error, and
    several yield the multi-error wrap shape.
    """
    out = bytearray()
    wrapped = []
    i = 0
    argi = 0
    n = len(fmt)
    while i < n:
        c = fmt[i]
        if c != 0x25:
            out.append(c)
            i += 1
            continue
        verb = fmt[i + 1]
        i += 2
        if verb == 0x25:
            out.append(0x25)
            continue
        arg = args[argi]
        argi += 1
        if verb == 0x71:
            out += _go_quote(arg)
            continue
        if verb == 0x77:
            wrapped.append(arg)
        out += go_str(arg).encode("utf-8")
    msg = bytes(out)
    if not wrapped:
        return _StringError(msg)
    if len(wrapped) == 1:
        return _WrapError(msg, wrapped[0])
    return _WrapErrors(msg, wrapped)


# fmt.Printf and its siblings. Go's printf verbs carry flags, a width, and a
# precision that Python's own formatting does not reproduce byte for byte, so the
# format is interpreted here directly over its bytes and each verb renders its
# operand to Go's exact text. go_str already supplies the %v default, so the
# engine reuses it for v and layers the numeric, string, and float verbs on top.

_FMT_DIGITS = frozenset(range(0x30, 0x3A))
_FMT_FLAGS = frozenset((0x2B, 0x2D, 0x20, 0x23, 0x30))  # + - space # 0


def _go_type(value):
    """Return the Go type name fmt's %T prints for a value.

    A struct carries its package-qualified name as a class attribute, and the
    scalar kinds map to their Go names; the integer width and named scalar types
    are not distinguished at runtime yet, so a plain int reads as int.
    """
    t = getattr(type(value), "_hebi_type", None)
    if t is not None:
        return t
    if value is None:
        return "<nil>"
    if value is True or value is False:
        return "bool"
    if isinstance(value, bool):
        return "bool"
    if isinstance(value, int):
        return "int"
    if isinstance(value, float):
        return "float64"
    if isinstance(value, bytes):
        return "string"
    if isinstance(value, bytearray):
        return "[]uint8"
    return type(value).__name__


def _go_sharp(value):
    """Render a value in Go's %#v form, its Go-syntax representation.

    A struct prints its package-qualified name and its fields as name:value pairs
    in braces, a string prints Go-quoted, and the scalars print as their source
    literals. A Stringer is not consulted, since %#v always shows the underlying
    representation.
    """
    if value is True:
        return "true"
    if value is False:
        return "false"
    if value is None:
        return "<nil>"
    if isinstance(value, float):
        return _gofloat(value)
    if isinstance(value, bytes):
        return _go_quote(value).decode("utf-8", "replace")
    t = getattr(type(value), "_hebi_type", None)
    slots = getattr(type(value), "__slots__", None)
    if t is not None and slots is not None:
        body = ", ".join(name + ":" + _go_sharp(getattr(value, name)) for name in slots)
        return t + "{" + body + "}"
    return str(value)


def _pad(b, flags, width, zero_ok, headlen, dlen):
    """Pad a rendered field to width, the Go way.

    A field at least the width wide is returned unchanged. The minus flag pads on
    the right with spaces; the zero flag, when the verb allows it, pads with zeros
    after any sign or base prefix; otherwise the field is right-justified with
    spaces. dlen is the display width in runes, which is what Go counts.
    """
    if width is None or dlen >= width:
        return b
    padn = width - dlen
    if 0x2D in flags:
        return b + b" " * padn
    if zero_ok:
        return b[:headlen] + b"0" * padn + b[headlen:]
    return b" " * padn + b


def _fmt_int(value, base, upper, flags, width, prec):
    """Render an integer verb, %d %b %o %x %X, with flags, width, and precision."""
    neg = value < 0
    mag = -value if neg else value
    if base == 10:
        digits = str(mag)
    else:
        digits = format(mag, {2: "b", 8: "o", 16: "X" if upper else "x"}[base])
    if prec is not None:
        if prec == 0 and mag == 0:
            digits = ""
        elif len(digits) < prec:
            digits = "0" * (prec - len(digits)) + digits
    prefix = ""
    if 0x23 in flags:
        if base == 8 and not digits.startswith("0"):
            prefix = "0"
        elif base == 16 and mag != 0:
            prefix = "0X" if upper else "0x"
    sign = "-" if neg else ("+" if 0x2B in flags else (" " if 0x20 in flags else ""))
    head = sign + prefix
    body = (head + digits).encode("utf-8")
    zero_ok = 0x30 in flags and prec is None and 0x2D not in flags
    return _pad(body, flags, width, zero_ok, len(head), len(head) + len(digits))


def _fmt_float(value, kind, flags, width, prec):
    """Render a float verb, %f %e %E %g %G, with flags, width, and precision."""
    special = True
    if value != value:
        core, neg = "NaN", False
    elif value == float("inf"):
        core, neg = "Inf", False
    elif value == float("-inf"):
        core, neg = "Inf", True
    else:
        special = False
        neg = math.copysign(1.0, value) < 0.0
        av = -value if neg else value
        if kind in (0x66, 0x46):  # f F
            core = "%.*f" % (6 if prec is None else prec, av)
        elif kind in (0x65, 0x45):  # e E
            core = "%.*e" % (6 if prec is None else prec, av)
        else:  # g G
            core = _gofloat(av) if prec is None else "%.*g" % (prec if prec else 1, av)
        if kind in (0x45, 0x47):  # E G
            core = core.upper()
    sign = "-" if neg else ("+" if 0x2B in flags else (" " if 0x20 in flags else ""))
    body = (sign + core).encode("utf-8")
    zero_ok = 0x30 in flags and 0x2D not in flags and not special
    return _pad(body, flags, width, zero_ok, len(sign), len(sign) + len(core))


def _fmt_str(value, flags, width, prec):
    """Render a %s verb, decoding a Go string or deferring to go_str otherwise."""
    if isinstance(value, (bytes, bytearray)):
        s = bytes(value).decode("utf-8", "replace")
    else:
        s = go_str(value)
    if prec is not None:
        s = s[:prec]
    return _pad(s.encode("utf-8"), flags, width, False, 0, len(s))


def _fmt_hexbytes(value, upper, flags, width):
    """Render %x or %X over a Go string or byte slice as its hex digits."""
    s = ("%02X" if upper else "%02x")
    body = ("".join(s % b for b in bytes(value))).encode("utf-8")
    return _pad(body, flags, width, False, 0, len(body))


def _fmt_verb(verb, arg, flags, width, prec):
    """Render one directive's operand to bytes for the given verb."""
    if verb == 0x76:  # v
        if 0x23 in flags:
            s = _go_sharp(arg)
        else:
            s = go_str(arg, 0x2B in flags)
        return _pad(s.encode("utf-8"), flags, width, False, 0, len(s))
    if verb == 0x54:  # T
        s = _go_type(arg)
        return _pad(s.encode("utf-8"), flags, width, False, 0, len(s))
    if verb == 0x64:  # d
        return _fmt_int(arg, 10, False, flags, width, prec)
    if verb == 0x62:  # b
        return _fmt_int(arg, 2, False, flags, width, prec)
    if verb == 0x6F:  # o
        return _fmt_int(arg, 8, False, flags, width, prec)
    if verb == 0x78:  # x
        if isinstance(arg, (bytes, bytearray)):
            return _fmt_hexbytes(arg, False, flags, width)
        return _fmt_int(arg, 16, False, flags, width, prec)
    if verb == 0x58:  # X
        if isinstance(arg, (bytes, bytearray)):
            return _fmt_hexbytes(arg, True, flags, width)
        return _fmt_int(arg, 16, True, flags, width, prec)
    if verb == 0x73:  # s
        return _fmt_str(arg, flags, width, prec)
    if verb == 0x71:  # q
        b = _go_quote(arg) if isinstance(arg, (bytes, bytearray)) else go_str(arg).encode("utf-8")
        return _pad(b, flags, width, False, 0, len(b))
    if verb in (0x66, 0x46, 0x65, 0x45, 0x67, 0x47):  # f F e E g G
        return _fmt_float(arg, verb, flags, width, prec)
    if verb == 0x74:  # t
        s = "true" if arg else "false"
        return _pad(s.encode("utf-8"), flags, width, False, 0, len(s))
    if verb == 0x63:  # c
        s = chr(arg)
        return _pad(s.encode("utf-8"), flags, width, False, 0, len(s))
    if verb == 0x55:  # U
        s = "U+%04X" % arg
        return _pad(s.encode("utf-8"), flags, width, False, 0, len(s))
    # An unknown verb prints Go's bad-verb marker so a mismatch is visible rather
    # than silently dropped.
    return b"%!" + bytes((verb,)) + b"(" + go_str(arg).encode("utf-8") + b")"


def _fmt(fmt, args):
    """Interpret a printf-style format over its bytes and return the result bytes.

    A percent begins a directive: any flags, an optional width, an optional
    precision after a dot, then the verb. A star for the width or precision reads
    its value from the next operand, the way Go does, and the operand for the verb
    follows. Everything outside a directive copies through unchanged.
    """
    out = bytearray()
    i = 0
    argi = 0
    n = len(fmt)
    while i < n:
        c = fmt[i]
        if c != 0x25:
            out.append(c)
            i += 1
            continue
        i += 1
        if i < n and fmt[i] == 0x25:  # %%
            out.append(0x25)
            i += 1
            continue
        flags = set()
        while i < n and fmt[i] in _FMT_FLAGS:
            flags.add(fmt[i])
            i += 1
        width = None
        if i < n and fmt[i] == 0x2A:  # *
            width = args[argi]
            argi += 1
            i += 1
            if width < 0:
                flags.add(0x2D)
                width = -width
        else:
            w, saw = 0, False
            while i < n and fmt[i] in _FMT_DIGITS:
                w = w * 10 + (fmt[i] - 0x30)
                saw = True
                i += 1
            if saw:
                width = w
        prec = None
        if i < n and fmt[i] == 0x2E:  # .
            i += 1
            if i < n and fmt[i] == 0x2A:
                prec = args[argi]
                argi += 1
                i += 1
            else:
                p = 0
                while i < n and fmt[i] in _FMT_DIGITS:
                    p = p * 10 + (fmt[i] - 0x30)
                    i += 1
                prec = p
        if i >= n:
            break
        verb = fmt[i]
        i += 1
        arg = args[argi]
        argi += 1
        out += _fmt_verb(verb, arg, flags, width, prec)
    return bytes(out)


def sprintf(fmt, *args):
    """fmt.Sprintf: the formatted text as a Go string, which is bytes."""
    return _fmt(fmt, args)


def printf(fmt, *args):
    """fmt.Printf: write the formatted text to standard output, no newline."""
    sys.stdout.write(_fmt(fmt, args).decode("utf-8", "replace"))


def _sprint(args):
    """Join operands the way fmt.Sprint does: a space only between two non-strings."""
    out = []
    prev_str = False
    for k, a in enumerate(args):
        is_str = isinstance(a, (bytes, bytearray))
        if k > 0 and not prev_str and not is_str:
            out.append(" ")
        out.append(go_str(a))
        prev_str = is_str
    return "".join(out).encode("utf-8")


def sprint(*args):
    """fmt.Sprint: operands in their default formats, spaces between non-strings."""
    return _sprint(args)


def goprint(*args):
    """fmt.Print: like Sprint, written to standard output."""
    sys.stdout.write(_sprint(args).decode("utf-8", "replace"))


def sprintln(*args):
    """fmt.Sprintln: operands space-separated with a trailing newline, as a string."""
    return (" ".join(go_str(a) for a in args) + "\n").encode("utf-8")


# strings package. A Go string is bytes on this tier, so each function takes and
# returns bytes and a rune it names is an int. The byte-oriented searches map
# straight onto Python's bytes methods, which share Go's semantics for a byte
# index, while the case, space, and field functions decode to text so Unicode
# folds and splits the way Go's do. A function that returns []string builds a
# slice header over a fresh list, the shape a Go slice takes on this tier.


def _rune_list(s):
    """Split a Go string into a list of its UTF-8 runes, each as bytes."""
    return [c.encode("utf-8") for c in s.decode("utf-8", "replace")]


def _slice_iter(sl):
    """Yield the elements a Go slice header spans, in order."""
    if sl is NIL_SLICE:
        return
    for k in range(sl.length):
        yield sl.array[sl.offset + k]


def str_contains(s, sub):
    return sub in s


def str_contains_rune(s, r):
    return chr(r).encode("utf-8") in s


def str_has_prefix(s, prefix):
    return s.startswith(prefix)


def str_has_suffix(s, suffix):
    return s.endswith(suffix)


def str_index(s, sub):
    return s.find(sub)


def str_last_index(s, sub):
    return s.rfind(sub)


def str_index_byte(s, b):
    return s.find(bytes((b,)))


def str_count(s, sub):
    if len(sub) == 0:
        # Go counts an empty separator once before each rune and once at the end,
        # which is the rune count plus one.
        return len(s.decode("utf-8", "replace")) + 1
    return s.count(sub)


def str_split(s, sep):
    if len(sep) == 0:
        parts = _rune_list(s)
    else:
        parts = s.split(sep)
    return _slice_lit(list(parts))


def str_split_n(s, sep, n):
    if n == 0:
        return NIL_SLICE
    if len(sep) == 0:
        parts = _rune_list(s)
        if n > 0 and n < len(parts):
            parts = parts[: n - 1] + [b"".join(parts[n - 1 :])]
    elif n < 0:
        parts = s.split(sep)
    else:
        parts = s.split(sep, n - 1)
    return _slice_lit(list(parts))


def str_fields(s):
    return _slice_lit([p.encode("utf-8") for p in s.decode("utf-8", "replace").split()])


def str_join(elems, sep):
    return sep.join(_slice_iter(elems))


def str_to_upper(s):
    return s.decode("utf-8", "replace").upper().encode("utf-8")


def str_to_lower(s):
    return s.decode("utf-8", "replace").lower().encode("utf-8")


def str_trim_space(s):
    return s.decode("utf-8", "replace").strip().encode("utf-8")


def str_trim(s, cutset):
    cs = cutset.decode("utf-8", "replace")
    return s.decode("utf-8", "replace").strip(cs).encode("utf-8")


def str_trim_left(s, cutset):
    cs = cutset.decode("utf-8", "replace")
    return s.decode("utf-8", "replace").lstrip(cs).encode("utf-8")


def str_trim_right(s, cutset):
    cs = cutset.decode("utf-8", "replace")
    return s.decode("utf-8", "replace").rstrip(cs).encode("utf-8")


def str_trim_prefix(s, prefix):
    return s[len(prefix):] if s.startswith(prefix) else s


def str_trim_suffix(s, suffix):
    return s[: len(s) - len(suffix)] if suffix and s.endswith(suffix) else s


def str_repeat(s, count):
    return s * count


def str_replace(s, old, new, n):
    if n < 0:
        return s.replace(old, new)
    return s.replace(old, new, n)


def str_replace_all(s, old, new):
    return s.replace(old, new)


def str_equal_fold(s, t):
    return s.decode("utf-8", "replace").casefold() == t.decode("utf-8", "replace").casefold()


# string, []byte, and []rune conversions bridge the byte-string world and the
# slice world. A Go string is a bytes object, and a []byte or []rune is a Slice
# header of ints, so a conversion walks one representation into the other.


def _str_to_bytes(s):
    """[]byte(string): a fresh byte slice over the string's bytes as ints."""
    return _slice_lit(list(s))


def _bytes_to_str(sl):
    """string([]byte): the bytes the slice header spans, as a Go string."""
    return bytes(bytearray(_slice_iter(sl)))


def _encode_rune(r):
    """The UTF-8 of a code point, U+FFFD when the value is not a valid one, which
    is how Go encodes an out-of-range or surrogate code point in a conversion."""
    if r < 0 or r > 0x10FFFF or 0xD800 <= r <= 0xDFFF:
        return b"\xef\xbf\xbd"
    return chr(r).encode("utf-8")


def _rune_to_str(r):
    """string(rune): the UTF-8 encoding of a single code point."""
    return _encode_rune(r)


def _str_to_runes(s):
    """[]rune(string): the decoded code points, each as an int."""
    return _slice_lit([ord(c) for c in s.decode("utf-8", "replace")])


def _runes_to_str(sl):
    """string([]rune): the UTF-8 of each code point joined into a string."""
    out = bytearray()
    for r in _slice_iter(sl):
        out += _encode_rune(r)
    return bytes(out)


# bytes package. A []byte is a Slice header of ints, so each function reads the
# window into a Python byte string, runs the same operation strings does, and
# returns a []byte result as a fresh header.


def _bytes_of(sl):
    """The bytes a []byte slice header spans, as an immutable byte string."""
    return bytes(bytearray(_slice_iter(sl)))


def _bytes_slice(b):
    """A fresh []byte header over the bytes of b."""
    return _slice_lit(list(b))


def bytes_contains(sl, sub):
    return _bytes_of(sub) in _bytes_of(sl)


def bytes_equal(a, b):
    return _bytes_of(a) == _bytes_of(b)


def bytes_compare(a, b):
    x, y = _bytes_of(a), _bytes_of(b)
    return (x > y) - (x < y)


def bytes_has_prefix(sl, prefix):
    return _bytes_of(sl).startswith(_bytes_of(prefix))


def bytes_has_suffix(sl, suffix):
    return _bytes_of(sl).endswith(_bytes_of(suffix))


def bytes_index(sl, sub):
    return _bytes_of(sl).find(_bytes_of(sub))


def bytes_last_index(sl, sub):
    return _bytes_of(sl).rfind(_bytes_of(sub))


def bytes_index_byte(sl, b):
    return _bytes_of(sl).find(bytes((b,)))


def bytes_count(sl, sub):
    s, sub = _bytes_of(sl), _bytes_of(sub)
    if len(sub) == 0:
        # Go counts an empty separator once before each rune and once at the end.
        return len(s.decode("utf-8", "replace")) + 1
    return s.count(sub)


def bytes_repeat(sl, count):
    return _bytes_slice(_bytes_of(sl) * count)


def bytes_join(elems, sep):
    s = _bytes_of(sep)
    return _bytes_slice(s.join(_bytes_of(e) for e in _slice_iter(elems)))


def bytes_split(sl, sep):
    s, sep = _bytes_of(sl), _bytes_of(sep)
    if len(sep) == 0:
        parts = _rune_list(s)
    else:
        parts = s.split(sep)
    return _slice_lit([_bytes_slice(p) for p in parts])


def bytes_to_upper(sl):
    return _bytes_slice(_bytes_of(sl).decode("utf-8", "replace").upper().encode("utf-8"))


def bytes_to_lower(sl):
    return _bytes_slice(_bytes_of(sl).decode("utf-8", "replace").lower().encode("utf-8"))


def bytes_trim_space(sl):
    return _bytes_slice(_bytes_of(sl).decode("utf-8", "replace").strip().encode("utf-8"))


# unicode/utf8 package. Pure UTF-8 mechanics, independent of the Unicode
# version, so a Go-faithful decoder reads one rune at a time and every function
# matches Go bit for bit, including the RuneError with width one an invalid byte
# decodes to and the legitimately encoded U+FFFD that decodes to width three.


def _utf8_decode(b, i):
    """Decode the rune at b[i:], returning (rune, size) the way Go's
    utf8.DecodeRune does. An empty input is (RuneError, 0), and any malformed
    sequence is (RuneError, 1), following Go's first-byte accept ranges that
    reject an overlong form, a surrogate, and an out-of-range code point."""
    n = len(b)
    if i >= n:
        return 0xFFFD, 0
    c0 = b[i]
    if c0 < 0x80:
        return c0, 1
    if c0 < 0xC2:
        # A continuation byte or an overlong two-byte lead is invalid.
        return 0xFFFD, 1
    if c0 < 0xE0:
        size, lo, hi, cp = 2, 0x80, 0xBF, c0 & 0x1F
    elif c0 < 0xF0:
        size, cp = 3, c0 & 0x0F
        lo, hi = 0x80, 0xBF
        if c0 == 0xE0:
            lo = 0xA0
        elif c0 == 0xED:
            hi = 0x9F
    elif c0 < 0xF5:
        size, cp = 4, c0 & 0x07
        lo, hi = 0x80, 0xBF
        if c0 == 0xF0:
            lo = 0x90
        elif c0 == 0xF4:
            hi = 0x8F
    else:
        return 0xFFFD, 1
    if i + 1 >= n:
        return 0xFFFD, 1
    c1 = b[i + 1]
    if c1 < lo or c1 > hi:
        return 0xFFFD, 1
    cp = (cp << 6) | (c1 & 0x3F)
    for k in range(2, size):
        if i + k >= n:
            return 0xFFFD, 1
        ck = b[i + k]
        if ck < 0x80 or ck > 0xBF:
            return 0xFFFD, 1
        cp = (cp << 6) | (ck & 0x3F)
    return cp, size


def _utf8_count(b):
    i, n, count = 0, len(b), 0
    while i < n:
        _, size = _utf8_decode(b, i)
        i += size
        count += 1
    return count


def _utf8_valid(b):
    i, n = 0, len(b)
    while i < n:
        r, size = _utf8_decode(b, i)
        if r == 0xFFFD and size == 1:
            return False
        i += size
    return True


def utf8_rune_count_in_string(s):
    return _utf8_count(s)


def utf8_rune_count(sl):
    return _utf8_count(_bytes_of(sl))


def utf8_valid_string(s):
    return _utf8_valid(s)


def utf8_valid(sl):
    return _utf8_valid(_bytes_of(sl))


def utf8_rune_len(r):
    if r < 0 or 0xD800 <= r <= 0xDFFF or r > 0x10FFFF:
        return -1
    if r < 0x80:
        return 1
    if r < 0x800:
        return 2
    if r < 0x10000:
        return 3
    return 4


def utf8_valid_rune(r):
    if 0xD800 <= r <= 0xDFFF:
        return False
    return 0 <= r <= 0x10FFFF


def utf8_decode_rune_in_string(s):
    return _utf8_decode(s, 0)


def utf8_decode_rune(sl):
    return _utf8_decode(_bytes_of(sl), 0)


def utf8_decode_last_rune_in_string(s):
    """The last rune of s and its size, matching Go's utf8.DecodeLastRuneInString.
    Go walks back up to UTFMax continuation bytes to find the lead, decodes
    forward, and falls back to a width-one RuneError when that does not line up."""
    n = len(s)
    if n == 0:
        return 0xFFFD, 0
    start = n - 1
    if s[start] < 0x80:
        return s[start], 1
    lim = n - 4
    if lim < 0:
        lim = 0
    start = n - 1
    while start >= lim:
        if s[start] < 0x80 or s[start] >= 0xC0:
            break
        start -= 1
    if start < lim:
        start = n - 1
    r, size = _utf8_decode(s, start)
    if start + size != n:
        return 0xFFFD, 1
    return r, size


# unicode package. A rune classifies against Go's pinned category tables rather
# than the host Python's unicodedata, which drifts by CPython release, so the
# result matches go run byte for byte on any interpreter. IsControl and IsSpace
# are fixed code-point sets independent of the Unicode version. The case-mapping
# functions and the wider classifiers wait for their own slice, which pins the
# case ranges the same way.

# BEGIN GENERATED UNICODE TABLES
# Pinned from Go's unicode package, version 15.0.0, so a rune classifies the
# same as go run whatever the host Python's unicodedata version is. Each table
# is a flat run of low, high, stride triples sorted by low bound for the binary
# search in _in_ranges. Regenerate with go generate ./pkg/shim/...
_UNICODE_VERSION = "15.0.0"
_U_LETTER = (65, 90, 1, 97, 122, 1, 170, 181, 11, 186, 192, 6, 193, 214, 1, 216, 246, 1, 248, 705, 1, 710, 721, 1, 736, 740, 1, 748, 750, 2, 880, 884, 1, 886, 887, 1, 890, 893, 1, 895, 902, 7, 904, 906, 1, 908, 910, 2, 911, 929, 1, 931, 1013, 1, 1015, 1153, 1, 1162, 1327, 1, 1329, 1366, 1, 1369, 1376, 7, 1377, 1416, 1, 1488, 1514, 1, 1519, 1522, 1, 1568, 1610, 1, 1646, 1647, 1, 1649, 1747, 1, 1749, 1765, 16, 1766, 1774, 8, 1775, 1786, 11, 1787, 1788, 1, 1791, 1808, 17, 1810, 1839, 1, 1869, 1957, 1, 1969, 1994, 25, 1995, 2026, 1, 2036, 2037, 1, 2042, 2048, 6, 2049, 2069, 1, 2074, 2084, 10, 2088, 2112, 24, 2113, 2136, 1, 2144, 2154, 1, 2160, 2183, 1, 2185, 2190, 1, 2208, 2249, 1, 2308, 2361, 1, 2365, 2384, 19, 2392, 2401, 1, 2417, 2432, 1, 2437, 2444, 1, 2447, 2448, 1, 2451, 2472, 1, 2474, 2480, 1, 2482, 2486, 4, 2487, 2489, 1, 2493, 2510, 17, 2524, 2525, 1, 2527, 2529, 1, 2544, 2545, 1, 2556, 2565, 9, 2566, 2570, 1, 2575, 2576, 1, 2579, 2600, 1, 2602, 2608, 1, 2610, 2611, 1, 2613, 2614, 1, 2616, 2617, 1, 2649, 2652, 1, 2654, 2674, 20, 2675, 2676, 1, 2693, 2701, 1, 2703, 2705, 1, 2707, 2728, 1, 2730, 2736, 1, 2738, 2739, 1, 2741, 2745, 1, 2749, 2768, 19, 2784, 2785, 1, 2809, 2821, 12, 2822, 2828, 1, 2831, 2832, 1, 2835, 2856, 1, 2858, 2864, 1, 2866, 2867, 1, 2869, 2873, 1, 2877, 2908, 31, 2909, 2911, 2, 2912, 2913, 1, 2929, 2947, 18, 2949, 2954, 1, 2958, 2960, 1, 2962, 2965, 1, 2969, 2970, 1, 2972, 2974, 2, 2975, 2979, 4, 2980, 2984, 4, 2985, 2986, 1, 2990, 3001, 1, 3024, 3077, 53, 3078, 3084, 1, 3086, 3088, 1, 3090, 3112, 1, 3114, 3129, 1, 3133, 3160, 27, 3161, 3162, 1, 3165, 3168, 3, 3169, 3200, 31, 3205, 3212, 1, 3214, 3216, 1, 3218, 3240, 1, 3242, 3251, 1, 3253, 3257, 1, 3261, 3293, 32, 3294, 3296, 2, 3297, 3313, 16, 3314, 3332, 18, 3333, 3340, 1, 3342, 3344, 1, 3346, 3386, 1, 3389, 3406, 17, 3412, 3414, 1, 3423, 3425, 1, 3450, 3455, 1, 3461, 3478, 1, 3482, 3505, 1, 3507, 3515, 1, 3517, 3520, 3, 3521, 3526, 1, 3585, 3632, 1, 3634, 3635, 1, 3648, 3654, 1, 3713, 3714, 1, 3716, 3718, 2, 3719, 3722, 1, 3724, 3747, 1, 3749, 3751, 2, 3752, 3760, 1, 3762, 3763, 1, 3773, 3776, 3, 3777, 3780, 1, 3782, 3804, 22, 3805, 3807, 1, 3840, 3904, 64, 3905, 3911, 1, 3913, 3948, 1, 3976, 3980, 1, 4096, 4138, 1, 4159, 4176, 17, 4177, 4181, 1, 4186, 4189, 1, 4193, 4197, 4, 4198, 4206, 8, 4207, 4208, 1, 4213, 4225, 1, 4238, 4256, 18, 4257, 4293, 1, 4295, 4301, 6, 4304, 4346, 1, 4348, 4680, 1, 4682, 4685, 1, 4688, 4694, 1, 4696, 4698, 2, 4699, 4701, 1, 4704, 4744, 1, 4746, 4749, 1, 4752, 4784, 1, 4786, 4789, 1, 4792, 4798, 1, 4800, 4802, 2, 4803, 4805, 1, 4808, 4822, 1, 4824, 4880, 1, 4882, 4885, 1, 4888, 4954, 1, 4992, 5007, 1, 5024, 5109, 1, 5112, 5117, 1, 5121, 5740, 1, 5743, 5759, 1, 5761, 5786, 1, 5792, 5866, 1, 5873, 5880, 1, 5888, 5905, 1, 5919, 5937, 1, 5952, 5969, 1, 5984, 5996, 1, 5998, 6000, 1, 6016, 6067, 1, 6103, 6108, 5, 6176, 6264, 1, 6272, 6276, 1, 6279, 6312, 1, 6314, 6320, 6, 6321, 6389, 1, 6400, 6430, 1, 6480, 6509, 1, 6512, 6516, 1, 6528, 6571, 1, 6576, 6601, 1, 6656, 6678, 1, 6688, 6740, 1, 6823, 6917, 94, 6918, 6963, 1, 6981, 6988, 1, 7043, 7072, 1, 7086, 7087, 1, 7098, 7141, 1, 7168, 7203, 1, 7245, 7247, 1, 7258, 7293, 1, 7296, 7304, 1, 7312, 7354, 1, 7357, 7359, 1, 7401, 7404, 1, 7406, 7411, 1, 7413, 7414, 1, 7418, 7424, 6, 7425, 7615, 1, 7680, 7957, 1, 7960, 7965, 1, 7968, 8005, 1, 8008, 8013, 1, 8016, 8023, 1, 8025, 8031, 2, 8032, 8061, 1, 8064, 8116, 1, 8118, 8124, 1, 8126, 8130, 4, 8131, 8132, 1, 8134, 8140, 1, 8144, 8147, 1, 8150, 8155, 1, 8160, 8172, 1, 8178, 8180, 1, 8182, 8188, 1, 8305, 8319, 14, 8336, 8348, 1, 8450, 8455, 5, 8458, 8467, 1, 8469, 8473, 4, 8474, 8477, 1, 8484, 8490, 2, 8491, 8493, 1, 8495, 8505, 1, 8508, 8511, 1, 8517, 8521, 1, 8526, 8579, 53, 8580, 11264, 2684, 11265, 11492, 1, 11499, 11502, 1, 11506, 11507, 1, 11520, 11557, 1, 11559, 11565, 6, 11568, 11623, 1, 11631, 11648, 17, 11649, 11670, 1, 11680, 11686, 1, 11688, 11694, 1, 11696, 11702, 1, 11704, 11710, 1, 11712, 11718, 1, 11720, 11726, 1, 11728, 11734, 1, 11736, 11742, 1, 11823, 12293, 470, 12294, 12337, 43, 12338, 12341, 1, 12347, 12348, 1, 12353, 12438, 1, 12445, 12447, 1, 12449, 12538, 1, 12540, 12543, 1, 12549, 12591, 1, 12593, 12686, 1, 12704, 12735, 1, 12784, 12799, 1, 13312, 19903, 1, 19968, 42124, 1, 42192, 42237, 1, 42240, 42508, 1, 42512, 42527, 1, 42538, 42539, 1, 42560, 42606, 1, 42623, 42653, 1, 42656, 42725, 1, 42775, 42783, 1, 42786, 42888, 1, 42891, 42954, 1, 42960, 42961, 1, 42963, 42965, 2, 42966, 42969, 1, 42994, 43009, 1, 43011, 43013, 1, 43015, 43018, 1, 43020, 43042, 1, 43072, 43123, 1, 43138, 43187, 1, 43250, 43255, 1, 43259, 43261, 2, 43262, 43274, 12, 43275, 43301, 1, 43312, 43334, 1, 43360, 43388, 1, 43396, 43442, 1, 43471, 43488, 17, 43489, 43492, 1, 43494, 43503, 1, 43514, 43518, 1, 43520, 43560, 1, 43584, 43586, 1, 43588, 43595, 1, 43616, 43638, 1, 43642, 43646, 4, 43647, 43695, 1, 43697, 43701, 4, 43702, 43705, 3, 43706, 43709, 1, 43712, 43714, 2, 43739, 43741, 1, 43744, 43754, 1, 43762, 43764, 1, 43777, 43782, 1, 43785, 43790, 1, 43793, 43798, 1, 43808, 43814, 1, 43816, 43822, 1, 43824, 43866, 1, 43868, 43881, 1, 43888, 44002, 1, 44032, 55203, 1, 55216, 55238, 1, 55243, 55291, 1, 63744, 64109, 1, 64112, 64217, 1, 64256, 64262, 1, 64275, 64279, 1, 64285, 64287, 2, 64288, 64296, 1, 64298, 64310, 1, 64312, 64316, 1, 64318, 64320, 2, 64321, 64323, 2, 64324, 64326, 2, 64327, 64433, 1, 64467, 64829, 1, 64848, 64911, 1, 64914, 64967, 1, 65008, 65019, 1, 65136, 65140, 1, 65142, 65276, 1, 65313, 65338, 1, 65345, 65370, 1, 65382, 65470, 1, 65474, 65479, 1, 65482, 65487, 1, 65490, 65495, 1, 65498, 65500, 1, 65536, 65547, 1, 65549, 65574, 1, 65576, 65594, 1, 65596, 65597, 1, 65599, 65613, 1, 65616, 65629, 1, 65664, 65786, 1, 66176, 66204, 1, 66208, 66256, 1, 66304, 66335, 1, 66349, 66368, 1, 66370, 66377, 1, 66384, 66421, 1, 66432, 66461, 1, 66464, 66499, 1, 66504, 66511, 1, 66560, 66717, 1, 66736, 66771, 1, 66776, 66811, 1, 66816, 66855, 1, 66864, 66915, 1, 66928, 66938, 1, 66940, 66954, 1, 66956, 66962, 1, 66964, 66965, 1, 66967, 66977, 1, 66979, 66993, 1, 66995, 67001, 1, 67003, 67004, 1, 67072, 67382, 1, 67392, 67413, 1, 67424, 67431, 1, 67456, 67461, 1, 67463, 67504, 1, 67506, 67514, 1, 67584, 67589, 1, 67592, 67594, 2, 67595, 67637, 1, 67639, 67640, 1, 67644, 67647, 3, 67648, 67669, 1, 67680, 67702, 1, 67712, 67742, 1, 67808, 67826, 1, 67828, 67829, 1, 67840, 67861, 1, 67872, 67897, 1, 67968, 68023, 1, 68030, 68031, 1, 68096, 68112, 16, 68113, 68115, 1, 68117, 68119, 1, 68121, 68149, 1, 68192, 68220, 1, 68224, 68252, 1, 68288, 68295, 1, 68297, 68324, 1, 68352, 68405, 1, 68416, 68437, 1, 68448, 68466, 1, 68480, 68497, 1, 68608, 68680, 1, 68736, 68786, 1, 68800, 68850, 1, 68864, 68899, 1, 69248, 69289, 1, 69296, 69297, 1, 69376, 69404, 1, 69415, 69424, 9, 69425, 69445, 1, 69488, 69505, 1, 69552, 69572, 1, 69600, 69622, 1, 69635, 69687, 1, 69745, 69746, 1, 69749, 69763, 14, 69764, 69807, 1, 69840, 69864, 1, 69891, 69926, 1, 69956, 69959, 3, 69968, 70002, 1, 70006, 70019, 13, 70020, 70066, 1, 70081, 70084, 1, 70106, 70108, 2, 70144, 70161, 1, 70163, 70187, 1, 70207, 70208, 1, 70272, 70278, 1, 70280, 70282, 2, 70283, 70285, 1, 70287, 70301, 1, 70303, 70312, 1, 70320, 70366, 1, 70405, 70412, 1, 70415, 70416, 1, 70419, 70440, 1, 70442, 70448, 1, 70450, 70451, 1, 70453, 70457, 1, 70461, 70480, 19, 70493, 70497, 1, 70656, 70708, 1, 70727, 70730, 1, 70751, 70753, 1, 70784, 70831, 1, 70852, 70853, 1, 70855, 71040, 185, 71041, 71086, 1, 71128, 71131, 1, 71168, 71215, 1, 71236, 71296, 60, 71297, 71338, 1, 71352, 71424, 72, 71425, 71450, 1, 71488, 71494, 1, 71680, 71723, 1, 71840, 71903, 1, 71935, 71942, 1, 71945, 71948, 3, 71949, 71955, 1, 71957, 71958, 1, 71960, 71983, 1, 71999, 72001, 2, 72096, 72103, 1, 72106, 72144, 1, 72161, 72163, 2, 72192, 72203, 11, 72204, 72242, 1, 72250, 72272, 22, 72284, 72329, 1, 72349, 72368, 19, 72369, 72440, 1, 72704, 72712, 1, 72714, 72750, 1, 72768, 72818, 50, 72819, 72847, 1, 72960, 72966, 1, 72968, 72969, 1, 72971, 73008, 1, 73030, 73056, 26, 73057, 73061, 1, 73063, 73064, 1, 73066, 73097, 1, 73112, 73440, 328, 73441, 73458, 1, 73474, 73476, 2, 73477, 73488, 1, 73490, 73523, 1, 73648, 73728, 80, 73729, 74649, 1, 74880, 75075, 1, 77712, 77808, 1, 77824, 78895, 1, 78913, 78918, 1, 82944, 83526, 1, 92160, 92728, 1, 92736, 92766, 1, 92784, 92862, 1, 92880, 92909, 1, 92928, 92975, 1, 92992, 92995, 1, 93027, 93047, 1, 93053, 93071, 1, 93760, 93823, 1, 93952, 94026, 1, 94032, 94099, 67, 94100, 94111, 1, 94176, 94177, 1, 94179, 94208, 29, 94209, 100343, 1, 100352, 101589, 1, 101632, 101640, 1, 110576, 110579, 1, 110581, 110587, 1, 110589, 110590, 1, 110592, 110882, 1, 110898, 110928, 30, 110929, 110930, 1, 110933, 110948, 15, 110949, 110951, 1, 110960, 111355, 1, 113664, 113770, 1, 113776, 113788, 1, 113792, 113800, 1, 113808, 113817, 1, 119808, 119892, 1, 119894, 119964, 1, 119966, 119967, 1, 119970, 119973, 3, 119974, 119977, 3, 119978, 119980, 1, 119982, 119993, 1, 119995, 119997, 2, 119998, 120003, 1, 120005, 120069, 1, 120071, 120074, 1, 120077, 120084, 1, 120086, 120092, 1, 120094, 120121, 1, 120123, 120126, 1, 120128, 120132, 1, 120134, 120138, 4, 120139, 120144, 1, 120146, 120485, 1, 120488, 120512, 1, 120514, 120538, 1, 120540, 120570, 1, 120572, 120596, 1, 120598, 120628, 1, 120630, 120654, 1, 120656, 120686, 1, 120688, 120712, 1, 120714, 120744, 1, 120746, 120770, 1, 120772, 120779, 1, 122624, 122654, 1, 122661, 122666, 1, 122928, 122989, 1, 123136, 123180, 1, 123191, 123197, 1, 123214, 123536, 322, 123537, 123565, 1, 123584, 123627, 1, 124112, 124139, 1, 124896, 124902, 1, 124904, 124907, 1, 124909, 124910, 1, 124912, 124926, 1, 124928, 125124, 1, 125184, 125251, 1, 125259, 126464, 1205, 126465, 126467, 1, 126469, 126495, 1, 126497, 126498, 1, 126500, 126503, 3, 126505, 126514, 1, 126516, 126519, 1, 126521, 126523, 2, 126530, 126535, 5, 126537, 126541, 2, 126542, 126543, 1, 126545, 126546, 1, 126548, 126551, 3, 126553, 126561, 2, 126562, 126564, 2, 126567, 126570, 1, 126572, 126578, 1, 126580, 126583, 1, 126585, 126588, 1, 126590, 126592, 2, 126593, 126601, 1, 126603, 126619, 1, 126625, 126627, 1, 126629, 126633, 1, 126635, 126651, 1, 131072, 173791, 1, 173824, 177977, 1, 177984, 178205, 1, 178208, 183969, 1, 183984, 191456, 1, 194560, 195101, 1, 196608, 201546, 1, 201552, 205743, 1)
_U_DIGIT = (48, 57, 1, 1632, 1641, 1, 1776, 1785, 1, 1984, 1993, 1, 2406, 2415, 1, 2534, 2543, 1, 2662, 2671, 1, 2790, 2799, 1, 2918, 2927, 1, 3046, 3055, 1, 3174, 3183, 1, 3302, 3311, 1, 3430, 3439, 1, 3558, 3567, 1, 3664, 3673, 1, 3792, 3801, 1, 3872, 3881, 1, 4160, 4169, 1, 4240, 4249, 1, 6112, 6121, 1, 6160, 6169, 1, 6470, 6479, 1, 6608, 6617, 1, 6784, 6793, 1, 6800, 6809, 1, 6992, 7001, 1, 7088, 7097, 1, 7232, 7241, 1, 7248, 7257, 1, 42528, 42537, 1, 43216, 43225, 1, 43264, 43273, 1, 43472, 43481, 1, 43504, 43513, 1, 43600, 43609, 1, 44016, 44025, 1, 65296, 65305, 1, 66720, 66729, 1, 68912, 68921, 1, 69734, 69743, 1, 69872, 69881, 1, 69942, 69951, 1, 70096, 70105, 1, 70384, 70393, 1, 70736, 70745, 1, 70864, 70873, 1, 71248, 71257, 1, 71360, 71369, 1, 71472, 71481, 1, 71904, 71913, 1, 72016, 72025, 1, 72784, 72793, 1, 73040, 73049, 1, 73120, 73129, 1, 73552, 73561, 1, 92768, 92777, 1, 92864, 92873, 1, 93008, 93017, 1, 120782, 120831, 1, 123200, 123209, 1, 123632, 123641, 1, 124144, 124153, 1, 125264, 125273, 1, 130032, 130041, 1)
_U_NUMBER = (48, 57, 1, 178, 179, 1, 185, 188, 3, 189, 190, 1, 1632, 1641, 1, 1776, 1785, 1, 1984, 1993, 1, 2406, 2415, 1, 2534, 2543, 1, 2548, 2553, 1, 2662, 2671, 1, 2790, 2799, 1, 2918, 2927, 1, 2930, 2935, 1, 3046, 3058, 1, 3174, 3183, 1, 3192, 3198, 1, 3302, 3311, 1, 3416, 3422, 1, 3430, 3448, 1, 3558, 3567, 1, 3664, 3673, 1, 3792, 3801, 1, 3872, 3891, 1, 4160, 4169, 1, 4240, 4249, 1, 4969, 4988, 1, 5870, 5872, 1, 6112, 6121, 1, 6128, 6137, 1, 6160, 6169, 1, 6470, 6479, 1, 6608, 6618, 1, 6784, 6793, 1, 6800, 6809, 1, 6992, 7001, 1, 7088, 7097, 1, 7232, 7241, 1, 7248, 7257, 1, 8304, 8308, 4, 8309, 8313, 1, 8320, 8329, 1, 8528, 8578, 1, 8581, 8585, 1, 9312, 9371, 1, 9450, 9471, 1, 10102, 10131, 1, 11517, 12295, 778, 12321, 12329, 1, 12344, 12346, 1, 12690, 12693, 1, 12832, 12841, 1, 12872, 12879, 1, 12881, 12895, 1, 12928, 12937, 1, 12977, 12991, 1, 42528, 42537, 1, 42726, 42735, 1, 43056, 43061, 1, 43216, 43225, 1, 43264, 43273, 1, 43472, 43481, 1, 43504, 43513, 1, 43600, 43609, 1, 44016, 44025, 1, 65296, 65305, 1, 65799, 65843, 1, 65856, 65912, 1, 65930, 65931, 1, 66273, 66299, 1, 66336, 66339, 1, 66369, 66378, 9, 66513, 66517, 1, 66720, 66729, 1, 67672, 67679, 1, 67705, 67711, 1, 67751, 67759, 1, 67835, 67839, 1, 67862, 67867, 1, 68028, 68029, 1, 68032, 68047, 1, 68050, 68095, 1, 68160, 68168, 1, 68221, 68222, 1, 68253, 68255, 1, 68331, 68335, 1, 68440, 68447, 1, 68472, 68479, 1, 68521, 68527, 1, 68858, 68863, 1, 68912, 68921, 1, 69216, 69246, 1, 69405, 69414, 1, 69457, 69460, 1, 69573, 69579, 1, 69714, 69743, 1, 69872, 69881, 1, 69942, 69951, 1, 70096, 70105, 1, 70113, 70132, 1, 70384, 70393, 1, 70736, 70745, 1, 70864, 70873, 1, 71248, 71257, 1, 71360, 71369, 1, 71472, 71483, 1, 71904, 71922, 1, 72016, 72025, 1, 72784, 72812, 1, 73040, 73049, 1, 73120, 73129, 1, 73552, 73561, 1, 73664, 73684, 1, 74752, 74862, 1, 92768, 92777, 1, 92864, 92873, 1, 93008, 93017, 1, 93019, 93025, 1, 93824, 93846, 1, 119488, 119507, 1, 119520, 119539, 1, 119648, 119672, 1, 120782, 120831, 1, 123200, 123209, 1, 123632, 123641, 1, 124144, 124153, 1, 125127, 125135, 1, 125264, 125273, 1, 126065, 126123, 1, 126125, 126127, 1, 126129, 126132, 1, 126209, 126253, 1, 126255, 126269, 1, 127232, 127244, 1, 130032, 130041, 1)
_U_UPPER = (65, 90, 1, 192, 214, 1, 216, 222, 1, 256, 310, 2, 313, 327, 2, 330, 376, 2, 377, 381, 2, 385, 386, 1, 388, 390, 2, 391, 393, 2, 394, 395, 1, 398, 401, 1, 403, 404, 1, 406, 408, 1, 412, 413, 1, 415, 416, 1, 418, 422, 2, 423, 425, 2, 428, 430, 2, 431, 433, 2, 434, 435, 1, 437, 439, 2, 440, 444, 4, 452, 461, 3, 463, 475, 2, 478, 494, 2, 497, 500, 3, 502, 504, 1, 506, 562, 2, 570, 571, 1, 573, 574, 1, 577, 579, 2, 580, 582, 1, 584, 590, 2, 880, 882, 2, 886, 895, 9, 902, 904, 2, 905, 906, 1, 908, 910, 2, 911, 913, 2, 914, 929, 1, 931, 939, 1, 975, 978, 3, 979, 980, 1, 984, 1006, 2, 1012, 1015, 3, 1017, 1018, 1, 1021, 1071, 1, 1120, 1152, 2, 1162, 1216, 2, 1217, 1229, 2, 1232, 1326, 2, 1329, 1366, 1, 4256, 4293, 1, 4295, 4301, 6, 5024, 5109, 1, 7312, 7354, 1, 7357, 7359, 1, 7680, 7828, 2, 7838, 7934, 2, 7944, 7951, 1, 7960, 7965, 1, 7976, 7983, 1, 7992, 7999, 1, 8008, 8013, 1, 8025, 8031, 2, 8040, 8047, 1, 8120, 8123, 1, 8136, 8139, 1, 8152, 8155, 1, 8168, 8172, 1, 8184, 8187, 1, 8450, 8455, 5, 8459, 8461, 1, 8464, 8466, 1, 8469, 8473, 4, 8474, 8477, 1, 8484, 8490, 2, 8491, 8493, 1, 8496, 8499, 1, 8510, 8511, 1, 8517, 8579, 62, 11264, 11311, 1, 11360, 11362, 2, 11363, 11364, 1, 11367, 11373, 2, 11374, 11376, 1, 11378, 11381, 3, 11390, 11392, 1, 11394, 11490, 2, 11499, 11501, 2, 11506, 42560, 31054, 42562, 42604, 2, 42624, 42650, 2, 42786, 42798, 2, 42802, 42862, 2, 42873, 42877, 2, 42878, 42886, 2, 42891, 42893, 2, 42896, 42898, 2, 42902, 42922, 2, 42923, 42926, 1, 42928, 42932, 1, 42934, 42948, 2, 42949, 42951, 1, 42953, 42960, 7, 42966, 42968, 2, 42997, 65313, 22316, 65314, 65338, 1, 66560, 66599, 1, 66736, 66771, 1, 66928, 66938, 1, 66940, 66954, 1, 66956, 66962, 1, 66964, 66965, 1, 68736, 68786, 1, 71840, 71871, 1, 93760, 93791, 1, 119808, 119833, 1, 119860, 119885, 1, 119912, 119937, 1, 119964, 119966, 2, 119967, 119973, 3, 119974, 119977, 3, 119978, 119980, 1, 119982, 119989, 1, 120016, 120041, 1, 120068, 120069, 1, 120071, 120074, 1, 120077, 120084, 1, 120086, 120092, 1, 120120, 120121, 1, 120123, 120126, 1, 120128, 120132, 1, 120134, 120138, 4, 120139, 120144, 1, 120172, 120197, 1, 120224, 120249, 1, 120276, 120301, 1, 120328, 120353, 1, 120380, 120405, 1, 120432, 120457, 1, 120488, 120512, 1, 120546, 120570, 1, 120604, 120628, 1, 120662, 120686, 1, 120720, 120744, 1, 120778, 125184, 4406, 125185, 125217, 1)
_U_LOWER = (97, 122, 1, 181, 223, 42, 224, 246, 1, 248, 255, 1, 257, 311, 2, 312, 328, 2, 329, 375, 2, 378, 382, 2, 383, 384, 1, 387, 389, 2, 392, 396, 4, 397, 402, 5, 405, 409, 4, 410, 411, 1, 414, 417, 3, 419, 421, 2, 424, 426, 2, 427, 429, 2, 432, 436, 4, 438, 441, 3, 442, 445, 3, 446, 447, 1, 454, 460, 3, 462, 476, 2, 477, 495, 2, 496, 499, 3, 501, 505, 4, 507, 563, 2, 564, 569, 1, 572, 575, 3, 576, 578, 2, 583, 591, 2, 592, 659, 1, 661, 687, 1, 881, 883, 2, 887, 891, 4, 892, 893, 1, 912, 940, 28, 941, 974, 1, 976, 977, 1, 981, 983, 1, 985, 1007, 2, 1008, 1011, 1, 1013, 1019, 3, 1020, 1072, 52, 1073, 1119, 1, 1121, 1153, 2, 1163, 1215, 2, 1218, 1230, 2, 1231, 1327, 2, 1376, 1416, 1, 4304, 4346, 1, 4349, 4351, 1, 5112, 5117, 1, 7296, 7304, 1, 7424, 7467, 1, 7531, 7543, 1, 7545, 7578, 1, 7681, 7829, 2, 7830, 7837, 1, 7839, 7935, 2, 7936, 7943, 1, 7952, 7957, 1, 7968, 7975, 1, 7984, 7991, 1, 8000, 8005, 1, 8016, 8023, 1, 8032, 8039, 1, 8048, 8061, 1, 8064, 8071, 1, 8080, 8087, 1, 8096, 8103, 1, 8112, 8116, 1, 8118, 8119, 1, 8126, 8130, 4, 8131, 8132, 1, 8134, 8135, 1, 8144, 8147, 1, 8150, 8151, 1, 8160, 8167, 1, 8178, 8180, 1, 8182, 8183, 1, 8458, 8462, 4, 8463, 8467, 4, 8495, 8505, 5, 8508, 8509, 1, 8518, 8521, 1, 8526, 8580, 54, 11312, 11359, 1, 11361, 11365, 4, 11366, 11372, 2, 11377, 11379, 2, 11380, 11382, 2, 11383, 11387, 1, 11393, 11491, 2, 11492, 11500, 8, 11502, 11507, 5, 11520, 11557, 1, 11559, 11565, 6, 42561, 42605, 2, 42625, 42651, 2, 42787, 42799, 2, 42800, 42801, 1, 42803, 42865, 2, 42866, 42872, 1, 42874, 42876, 2, 42879, 42887, 2, 42892, 42894, 2, 42897, 42899, 2, 42900, 42901, 1, 42903, 42921, 2, 42927, 42933, 6, 42935, 42947, 2, 42952, 42954, 2, 42961, 42969, 2, 42998, 43002, 4, 43824, 43866, 1, 43872, 43880, 1, 43888, 43967, 1, 64256, 64262, 1, 64275, 64279, 1, 65345, 65370, 1, 66600, 66639, 1, 66776, 66811, 1, 66967, 66977, 1, 66979, 66993, 1, 66995, 67001, 1, 67003, 67004, 1, 68800, 68850, 1, 71872, 71903, 1, 93792, 93823, 1, 119834, 119859, 1, 119886, 119892, 1, 119894, 119911, 1, 119938, 119963, 1, 119990, 119993, 1, 119995, 119997, 2, 119998, 120003, 1, 120005, 120015, 1, 120042, 120067, 1, 120094, 120119, 1, 120146, 120171, 1, 120198, 120223, 1, 120250, 120275, 1, 120302, 120327, 1, 120354, 120379, 1, 120406, 120431, 1, 120458, 120485, 1, 120514, 120538, 1, 120540, 120545, 1, 120572, 120596, 1, 120598, 120603, 1, 120630, 120654, 1, 120656, 120661, 1, 120688, 120712, 1, 120714, 120719, 1, 120746, 120770, 1, 120772, 120777, 1, 120779, 122624, 1845, 122625, 122633, 1, 122635, 122654, 1, 122661, 122666, 1, 125218, 125251, 1)
_U_PUNCT = (33, 35, 1, 37, 42, 1, 44, 47, 1, 58, 59, 1, 63, 64, 1, 91, 93, 1, 95, 123, 28, 125, 161, 36, 167, 171, 4, 182, 183, 1, 187, 191, 4, 894, 903, 9, 1370, 1375, 1, 1417, 1418, 1, 1470, 1472, 2, 1475, 1478, 3, 1523, 1524, 1, 1545, 1546, 1, 1548, 1549, 1, 1563, 1565, 2, 1566, 1567, 1, 1642, 1645, 1, 1748, 1792, 44, 1793, 1805, 1, 2039, 2041, 1, 2096, 2110, 1, 2142, 2404, 262, 2405, 2416, 11, 2557, 2678, 121, 2800, 3191, 391, 3204, 3572, 368, 3663, 3674, 11, 3675, 3844, 169, 3845, 3858, 1, 3860, 3898, 38, 3899, 3901, 1, 3973, 4048, 75, 4049, 4052, 1, 4057, 4058, 1, 4170, 4175, 1, 4347, 4960, 613, 4961, 4968, 1, 5120, 5742, 622, 5787, 5788, 1, 5867, 5869, 1, 5941, 5942, 1, 6100, 6102, 1, 6104, 6106, 1, 6144, 6154, 1, 6468, 6469, 1, 6686, 6687, 1, 6816, 6822, 1, 6824, 6829, 1, 7002, 7008, 1, 7037, 7038, 1, 7164, 7167, 1, 7227, 7231, 1, 7294, 7295, 1, 7360, 7367, 1, 7379, 8208, 829, 8209, 8231, 1, 8240, 8259, 1, 8261, 8273, 1, 8275, 8286, 1, 8317, 8318, 1, 8333, 8334, 1, 8968, 8971, 1, 9001, 9002, 1, 10088, 10101, 1, 10181, 10182, 1, 10214, 10223, 1, 10627, 10648, 1, 10712, 10715, 1, 10748, 10749, 1, 11513, 11516, 1, 11518, 11519, 1, 11632, 11776, 144, 11777, 11822, 1, 11824, 11855, 1, 11858, 11869, 1, 12289, 12291, 1, 12296, 12305, 1, 12308, 12319, 1, 12336, 12349, 13, 12448, 12539, 91, 42238, 42239, 1, 42509, 42511, 1, 42611, 42622, 11, 42738, 42743, 1, 43124, 43127, 1, 43214, 43215, 1, 43256, 43258, 1, 43260, 43310, 50, 43311, 43359, 48, 43457, 43469, 1, 43486, 43487, 1, 43612, 43615, 1, 43742, 43743, 1, 43760, 43761, 1, 44011, 64830, 20819, 64831, 65040, 209, 65041, 65049, 1, 65072, 65106, 1, 65108, 65121, 1, 65123, 65128, 5, 65130, 65131, 1, 65281, 65283, 1, 65285, 65290, 1, 65292, 65295, 1, 65306, 65307, 1, 65311, 65312, 1, 65339, 65341, 1, 65343, 65371, 28, 65373, 65375, 2, 65376, 65381, 1, 65792, 65794, 1, 66463, 66512, 49, 66927, 67671, 744, 67871, 67903, 32, 68176, 68184, 1, 68223, 68336, 113, 68337, 68342, 1, 68409, 68415, 1, 68505, 68508, 1, 69293, 69461, 168, 69462, 69465, 1, 69510, 69513, 1, 69703, 69709, 1, 69819, 69820, 1, 69822, 69825, 1, 69952, 69955, 1, 70004, 70005, 1, 70085, 70088, 1, 70093, 70107, 14, 70109, 70111, 1, 70200, 70205, 1, 70313, 70731, 418, 70732, 70735, 1, 70746, 70747, 1, 70749, 70854, 105, 71105, 71127, 1, 71233, 71235, 1, 71264, 71276, 1, 71353, 71484, 131, 71485, 71486, 1, 71739, 72004, 265, 72005, 72006, 1, 72162, 72255, 93, 72256, 72262, 1, 72346, 72348, 1, 72350, 72354, 1, 72448, 72457, 1, 72769, 72773, 1, 72816, 72817, 1, 73463, 73464, 1, 73539, 73551, 1, 73727, 74864, 1137, 74865, 74868, 1, 77809, 77810, 1, 92782, 92783, 1, 92917, 92983, 66, 92984, 92987, 1, 92996, 93847, 851, 93848, 93850, 1, 94178, 113823, 19645, 121479, 121483, 1, 125278, 125279, 1)
# END GENERATED UNICODE TABLES


def _in_ranges(table, r):
    """Whether r falls in one of the low, high, stride triples of a pinned table,
    found by a binary search over the sorted low bounds and a stride check."""
    if r < 0:
        return False
    lo, hi = 0, len(table) // 3
    while lo < hi:
        mid = (lo + hi) // 2
        base = mid * 3
        if r < table[base]:
            hi = mid
        elif r > table[base + 1]:
            lo = mid + 1
        else:
            stride = table[base + 2]
            return (r - table[base]) % stride == 0
    return False


_UNICODE_SPACE = frozenset(
    (
        0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20, 0x85, 0xA0,
        0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006,
        0x2007, 0x2008, 0x2009, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000,
    )
)


def unicode_is_control(r):
    return r <= 0x1F or 0x7F <= r <= 0x9F


def unicode_is_space(r):
    return r in _UNICODE_SPACE


def unicode_is_letter(r):
    return _in_ranges(_U_LETTER, r)


def unicode_is_digit(r):
    # Go's IsDigit is the Nd category, and in latin1 that is only 0 through 9,
    # which the pinned Nd table already carries, so the plain lookup matches.
    return _in_ranges(_U_DIGIT, r)


def unicode_is_number(r):
    return _in_ranges(_U_NUMBER, r)


def unicode_is_upper(r):
    return _in_ranges(_U_UPPER, r)


def unicode_is_lower(r):
    return _in_ranges(_U_LOWER, r)


def unicode_is_punct(r):
    return _in_ranges(_U_PUNCT, r)


# strconv error sentinels. Go's strconv.ErrSyntax and ErrRange are the base
# errors a NumError wraps, and their Error text is what a parse failure prints.
_STRCONV_ERR_SYNTAX = _StringError(b"invalid syntax")
_STRCONV_ERR_RANGE = _StringError(b"value out of range")

# _DIGIT_VALS maps a digit byte to its value, both cases for the letter digits,
# so a base validator can reject a digit the base does not admit.
_DIGIT_VALS = {}
for _di, _dc in enumerate(b"0123456789abcdefghijklmnopqrstuvwxyz"):
    _DIGIT_VALS[_dc] = _di
    if _di >= 10:
        _DIGIT_VALS[_dc - 32] = _di


class _StrconvRange(Exception):
    """Raised inside the integer parser when the value overflows its bit size,
    so the caller can attach ErrRange rather than ErrSyntax."""


class _NumError:
    """The *strconv.NumError a parse function returns on a bad input.

    Go wraps a failed parse in a *NumError whose Error reads strconv.<Func>:
    parsing <quoted input>: <base error>, and whose Unwrap hands back the base
    error so errors.Is against ErrSyntax or ErrRange matches. The fields are Go
    strings, so they are bytes.
    """

    __slots__ = ("Func", "Num", "Err")

    def __init__(self, fn, num, err):
        self.Func = fn
        self.Num = num
        self.Err = err

    def Error(self):
        return b"strconv." + self.Func + b": parsing " + _go_quote(self.Num) + b": " + self.Err.Error()

    def Unwrap(self):
        return self.Err

    def __str__(self):
        return go_str(self.Error())


def _int_bounds(bit_size, signed):
    """The inclusive low and high bound for an integer of the given bit size, with
    a zero bit size meaning the 64-bit word Go uses for int and uint."""
    bits = 64 if bit_size == 0 else bit_size
    if signed:
        return -(1 << (bits - 1)), (1 << (bits - 1)) - 1
    return 0, (1 << bits) - 1


def _valid_digits(s, base):
    """Whether every byte of s is a digit the base admits."""
    for c in s:
        v = _DIGIT_VALS.get(c)
        if v is None or v >= base:
            return False
    return True


def _parse_int_go(text, base, bit_size, signed):
    """Parse an integer the way strconv.ParseInt and ParseUint do.

    Raises ValueError on a syntax error and _StrconvRange on an out-of-range
    value, so the caller can build the right NumError. A base of zero is inferred
    from a 0x, 0o, 0b, or leading-zero prefix, and only the base-zero forms admit
    the digit-separating underscore.
    """
    s = text
    if len(s) == 0:
        raise ValueError
    neg = False
    if s[:1] == b"+":
        s = s[1:]
    elif s[:1] == b"-":
        if not signed:
            raise ValueError
        neg = True
        s = s[1:]
    if len(s) == 0:
        raise ValueError
    base0 = base == 0
    if base0:
        head = s[:2]
        if head in (b"0x", b"0X"):
            base, s = 16, s[2:]
        elif head in (b"0o", b"0O"):
            base, s = 8, s[2:]
        elif head in (b"0b", b"0B"):
            base, s = 2, s[2:]
        elif s[:1] == b"0":
            base = 8
        else:
            base = 10
    if b"_" in s:
        if not base0 or s[:1] == b"_" or s[-1:] == b"_" or b"__" in s:
            raise ValueError
        s = s.replace(b"_", b"")
    if len(s) == 0 or not _valid_digits(s, base):
        raise ValueError
    val = int(s, base)
    if neg:
        val = -val
    lo, hi = _int_bounds(bit_size, signed)
    if val < lo or val > hi:
        raise _StrconvRange
    return val


def _format_int(i, base):
    """Render an integer in the given base with Go's lowercase digits."""
    if base == 10:
        return b"%d" % i
    digs = b"0123456789abcdefghijklmnopqrstuvwxyz"
    if i == 0:
        return b"0"
    neg = i < 0
    n = -i if neg else i
    out = bytearray()
    while n > 0:
        out.append(digs[n % base])
        n //= base
    if neg:
        out.append(0x2D)
    out.reverse()
    return bytes(out)


def strconv_atoi(s):
    text = bytes(s)
    try:
        return _parse_int_go(text, 10, 0, True), None
    except _StrconvRange:
        return 0, _NumError(b"Atoi", text, _STRCONV_ERR_RANGE)
    except ValueError:
        return 0, _NumError(b"Atoi", text, _STRCONV_ERR_SYNTAX)


def strconv_parse_int(s, base, bit_size):
    text = bytes(s)
    try:
        return _parse_int_go(text, base, bit_size, True), None
    except _StrconvRange:
        return 0, _NumError(b"ParseInt", text, _STRCONV_ERR_RANGE)
    except ValueError:
        return 0, _NumError(b"ParseInt", text, _STRCONV_ERR_SYNTAX)


def strconv_parse_uint(s, base, bit_size):
    text = bytes(s)
    try:
        return _parse_int_go(text, base, bit_size, False), None
    except _StrconvRange:
        return 0, _NumError(b"ParseUint", text, _STRCONV_ERR_RANGE)
    except ValueError:
        return 0, _NumError(b"ParseUint", text, _STRCONV_ERR_SYNTAX)


def strconv_itoa(i):
    return b"%d" % i


def strconv_format_int(i, base):
    return _format_int(i, base)


def strconv_format_uint(i, base):
    return _format_int(i, base)


def strconv_format_bool(b):
    return b"true" if b else b"false"


def strconv_parse_bool(s):
    text = bytes(s)
    if text in (b"1", b"t", b"T", b"TRUE", b"true", b"True"):
        return True, None
    if text in (b"0", b"f", b"F", b"FALSE", b"false", b"False"):
        return False, None
    return False, _NumError(b"ParseBool", text, _STRCONV_ERR_SYNTAX)


def strconv_quote(s):
    return _go_quote(bytes(s))


def strconv_parse_float(s, bit_size):
    text = bytes(s)
    low = text.lower()
    if low in (b"inf", b"+inf", b"infinity", b"+infinity"):
        return float("inf"), None
    if low in (b"-inf", b"-infinity"):
        return float("-inf"), None
    if low == b"nan":
        return float("nan"), None
    body = text
    if b"_" in body:
        # Go admits an underscore only between two digits, never leading, trailing,
        # or doubled, and Python's float would accept a looser placement.
        if body[:1] == b"_" or body[-1:] == b"_" or b"__" in body:
            return 0.0, _NumError(b"ParseFloat", text, _STRCONV_ERR_SYNTAX)
        body = body.replace(b"_", b"")
    try:
        value = float(body.decode("ascii"))
    except (ValueError, UnicodeDecodeError):
        return 0.0, _NumError(b"ParseFloat", text, _STRCONV_ERR_SYNTAX)
    # Python parses a whitespace-padded number that Go rejects, so a value whose
    # round trip does not match the trimmed input is treated as a syntax error.
    if body.strip() != body:
        return 0.0, _NumError(b"ParseFloat", text, _STRCONV_ERR_SYNTAX)
    if value in (float("inf"), float("-inf")):
        # A finite literal that overflows float64 is a range error, not syntax.
        return value, _NumError(b"ParseFloat", text, _STRCONV_ERR_RANGE)
    if bit_size == 32:
        narrowed = _f32(value)
        if narrowed in (float("inf"), float("-inf")):
            return narrowed, _NumError(b"ParseFloat", text, _STRCONV_ERR_RANGE)
        return narrowed, None
    return value, None


def strconv_format_float(f, verb, prec, bit_size):
    if bit_size == 32:
        f = _f32(f)
    kind = chr(verb)
    if f != f:
        return b"NaN"
    if f in (float("inf"), float("-inf")):
        return b"+Inf" if f > 0 else b"-Inf"
    if prec < 0:
        if kind in ("g", "G"):
            shortest = _gofloat32(f) if bit_size == 32 else _gofloat(f)
            return shortest.upper() if kind == "G" else shortest
        text = repr(f)
        prec = 0
        if "." in text:
            prec = len(text.split(".", 1)[1].rstrip("0").split("e")[0])
    if kind == "f":
        return ("%.*f" % (prec, f)).encode("ascii")
    if kind in ("e", "E"):
        return _format_float_exp(f, prec, kind).encode("ascii")
    if kind in ("g", "G"):
        body = "%.*g" % (prec if prec > 0 else 1, f)
        if "e" in body or "E" in body:
            mant, _, exp = body.replace("E", "e").partition("e")
            body = mant + "e" + _go_exp_sign(int(exp))
        return body.upper().encode("ascii") if kind == "G" else body.encode("ascii")
    return ("%g" % f).encode("ascii")


def _go_exp_sign(exp):
    """Render an exponent with Go's sign and at-least-two digits, +07 or -12."""
    return ("+" if exp >= 0 else "-") + "%02d" % abs(exp)


def _format_float_exp(f, prec, kind):
    """Render f in exponent notation with Go's two-digit-minimum signed exponent."""
    body = "%.*e" % (prec, f)
    mant, _, exp = body.partition("e")
    out = mant + kind.lower() + _go_exp_sign(int(exp))
    return out.upper() if kind == "E" else out


def _sort_window(sl):
    """Sort the elements a slice header spans in place, ascending, the way
    sort.Ints, sort.Float64s, and sort.Strings each do over their element type."""
    if sl is NIL_SLICE:
        return
    b, n = sl.offset, sl.length
    sl.array[b:b + n] = sorted(sl.array[b:b + n])


def sort_ints(sl):
    _sort_window(sl)


def sort_float64s(sl):
    _sort_window(sl)


def sort_strings(sl):
    _sort_window(sl)


def _are_sorted(sl):
    """Whether the elements a slice header spans are in ascending order."""
    if sl is NIL_SLICE:
        return True
    b, n = sl.offset, sl.length
    for i in range(1, n):
        if sl.array[b + i] < sl.array[b + i - 1]:
            return False
    return True


def sort_ints_are_sorted(sl):
    return _are_sorted(sl)


def sort_float64s_are_sorted(sl):
    return _are_sorted(sl)


def sort_strings_are_sorted(sl):
    return _are_sorted(sl)


def _search_window(sl, x):
    """The smallest index at which x could be inserted to keep the window sorted,
    which is what sort.SearchInts and its siblings return over a sorted slice."""
    n = 0 if sl is NIL_SLICE else sl.length
    b = 0 if sl is NIL_SLICE else sl.offset
    lo, hi = 0, n
    while lo < hi:
        mid = (lo + hi) // 2
        if sl.array[b + mid] < x:
            lo = mid + 1
        else:
            hi = mid
    return lo


def sort_search_ints(sl, x):
    return _search_window(sl, x)


def sort_search_strings(sl, x):
    return _search_window(sl, x)


def sort_search_float64s(sl, x):
    return _search_window(sl, x)


def sort_search(n, f):
    """sort.Search: the smallest index in [0, n) at which f turns true, or n."""
    lo, hi = 0, n
    while lo < hi:
        mid = (lo + hi) // 2
        if not f(mid):
            lo = mid + 1
        else:
            hi = mid
    return lo


def sort_slice(sl, less):
    """sort.Slice: sort the slice in place by a less function over live indices.

    less reads the slice by index, so the sort mutates the backing array in place
    and less sees each swap. An insertion sort keeps the calls index-consistent
    and produces the sorted order any total order defines, and sort.Slice makes
    no stability promise, so a stable pass is a valid result.
    """
    if sl is NIL_SLICE:
        return
    b, n = sl.offset, sl.length
    arr = sl.array
    for i in range(1, n):
        j = i
        while j > 0 and less(j, j - 1):
            arr[b + j], arr[b + j - 1] = arr[b + j - 1], arr[b + j]
            j -= 1


def sort_slice_stable(sl, less):
    # The insertion sort sort_slice runs is already stable, so SliceStable shares
    # it, holding the order of elements a less does not distinguish.
    sort_slice(sl, less)


def sort_slice_is_sorted(sl, less):
    """sort.SliceIsSorted: whether no element is less than the one before it."""
    n = 0 if sl is NIL_SLICE else sl.length
    for i in range(1, n):
        if less(i, i - 1):
            return False
    return True


def _copy_value(v):
    """Copy a value the way an assignment does on this tier: a struct is copied by
    value through its copy method, and every other value is passed through, since
    an int, a string, or a slice header needs no copy for a shallow clone."""
    if hasattr(type(v), "_hebi_type") and hasattr(v, "copy"):
        return v.copy()
    return v


def slices_contains(sl, v):
    for e in _slice_iter(sl):
        if e == v:
            return True
    return False


def slices_index(sl, v):
    i = 0
    for e in _slice_iter(sl):
        if e == v:
            return i
        i += 1
    return -1


def slices_index_func(sl, f):
    i = 0
    for e in _slice_iter(sl):
        if f(e):
            return i
        i += 1
    return -1


def slices_contains_func(sl, f):
    for e in _slice_iter(sl):
        if f(e):
            return True
    return False


def slices_sort(sl):
    _sort_window(sl)


def slices_sort_func(sl, cmp):
    """slices.SortFunc: sort in place by a cmp that returns Go's negative, zero, or
    positive over two element values, ordering through the same comparison Go's
    pattern-defeating sort would settle on for a total order."""
    if sl is NIL_SLICE:
        return
    b, n = sl.offset, sl.length
    sl.array[b:b + n] = sorted(sl.array[b:b + n], key=functools.cmp_to_key(cmp))


def slices_max(sl):
    n = 0 if sl is NIL_SLICE else sl.length
    if n == 0:
        raise _runtime_error("slices.Max: empty list")
    it = _slice_iter(sl)
    m = next(it)
    for e in it:
        if e > m:
            m = e
    return m


def slices_min(sl):
    n = 0 if sl is NIL_SLICE else sl.length
    if n == 0:
        raise _runtime_error("slices.Min: empty list")
    it = _slice_iter(sl)
    m = next(it)
    for e in it:
        if e < m:
            m = e
    return m


def slices_reverse(sl):
    if sl is NIL_SLICE:
        return
    arr = sl.array
    lo, hi = sl.offset, sl.offset + sl.length - 1
    while lo < hi:
        arr[lo], arr[hi] = arr[hi], arr[lo]
        lo += 1
        hi -= 1


def slices_equal(a, b):
    la = 0 if a is NIL_SLICE else a.length
    lb = 0 if b is NIL_SLICE else b.length
    if la != lb:
        return False
    for x, y in zip(_slice_iter(a), _slice_iter(b)):
        if x != y:
            return False
    return True


def slices_clone(sl):
    return _slice_lit([_copy_value(e) for e in _slice_iter(sl)])


def slices_compact(sl):
    """slices.Compact: drop each run of equal elements to its first, in place, and
    return the head that holds the kept elements, sharing the backing Go keeps."""
    n = 0 if sl is NIL_SLICE else sl.length
    if n == 0:
        return sl
    b, arr = sl.offset, sl.array
    k = 1
    for i in range(1, n):
        if arr[b + i] != arr[b + k - 1]:
            arr[b + k] = arr[b + i]
            k += 1
    return Slice(arr, b, k, sl.cap)


def slices_binary_search(sl, target):
    """slices.BinarySearch: the insertion index for target and whether it is
    present, over a sorted slice, Go's (int, bool) result."""
    n = 0 if sl is NIL_SLICE else sl.length
    b = 0 if sl is NIL_SLICE else sl.offset
    lo, hi = 0, n
    while lo < hi:
        mid = (lo + hi) // 2
        if sl.array[b + mid] < target:
            lo = mid + 1
        else:
            hi = mid
    return lo, (lo < n and sl.array[b + lo] == target)


_INF = float("inf")
_NEG_INF = float("-inf")


def math_abs(x):
    return math.fabs(x)


def math_ceil(x):
    return x if not math.isfinite(x) else float(math.ceil(x))


def math_floor(x):
    return x if not math.isfinite(x) else float(math.floor(x))


def math_trunc(x):
    return x if not math.isfinite(x) else float(math.trunc(x))


def math_round(x):
    # Go rounds a half away from zero, where Python's round rounds to even, so
    # nudge by a half toward the sign and take the floor or ceiling.
    if not math.isfinite(x):
        return x
    if x >= 0:
        return float(math.floor(x + 0.5))
    return float(math.ceil(x - 0.5))


def math_sqrt(x):
    # Go's Sqrt returns NaN for a negative operand where Python would raise.
    if x < 0:
        return float("nan")
    return math.sqrt(x)


def math_mod(x, y):
    # Go's Mod is the C fmod, the remainder with the sign of x. It is NaN when x
    # is infinite or y is zero, and x when y is infinite.
    if x != x or y != y or y == 0.0 or x in (_INF, _NEG_INF):
        return float("nan")
    if y in (_INF, _NEG_INF):
        return x
    return math.fmod(x, y)


def math_max(x, y):
    # Go's Max propagates NaN, lets a positive infinity dominate, and prefers a
    # positive zero over a negative one.
    if x != x or y != y:
        return float("nan")
    if x == _INF or y == _INF:
        return _INF
    if x == 0.0 and y == 0.0:
        if math.copysign(1.0, x) < 0 and math.copysign(1.0, y) < 0:
            return -0.0
        return 0.0
    return x if x > y else y


def math_min(x, y):
    if x != x or y != y:
        return float("nan")
    if x == _NEG_INF or y == _NEG_INF:
        return _NEG_INF
    if x == 0.0 and y == 0.0:
        if math.copysign(1.0, x) > 0 and math.copysign(1.0, y) > 0:
            return 0.0
        return -0.0
    return x if x < y else y


def math_dim(x, y):
    # Go's Dim is the positive difference, max(x-y, 0), and passes a NaN through.
    d = x - y
    if d != d:
        return d
    return d if d > 0 else 0.0


def math_copysign(x, y):
    return math.copysign(x, y)


def math_signbit(x):
    return math.copysign(1.0, x) < 0


def math_is_nan(x):
    return x != x


def math_is_inf(x, sign):
    if sign > 0:
        return x == _INF
    if sign < 0:
        return x == _NEG_INF
    return x == _INF or x == _NEG_INF


def math_nan():
    return float("nan")


def math_inf(sign):
    return _INF if sign >= 0 else _NEG_INF


def errors_unwrap(err):
    """errors.Unwrap: one level through an Unwrap() error, None for anything else.

    A multi-error Unwrap that returns a list is deliberately not followed, so a
    joined or multiply wrapped error unwraps to None here, matching Go's single
    errors.Unwrap.
    """
    u = getattr(err, "Unwrap", None)
    if u is None:
        return None
    result = u()
    if isinstance(result, list):
        return None
    return result


def _comparable_equal(a, b):
    """Report Go == equality between two error values, guarded for uncomparable ones."""
    try:
        return bool(a == b)
    except Exception:
        return False


def errors_is(err, target):
    """errors.Is: whether any error in err's chain matches target.

    At each node it matches on identity or Go == equality, then on an Is(error)
    method, then unwraps, following both the single Unwrap() error and the multi
    Unwrap() []error shapes, recursing into every branch of the latter.
    """
    if target is None:
        return err is None
    while err is not None:
        if err is target or _comparable_equal(err, target):
            return True
        is_method = getattr(err, "Is", None)
        if is_method is not None and is_method(target):
            return True
        nxt = getattr(err, "Unwrap", None)
        if nxt is None:
            return False
        u = nxt()
        if isinstance(u, list):
            return any(errors_is(e, target) for e in u)
        err = u
    return False


def errors_as(err, typ):
    """errors.As in comma-ok form: the first error in the chain of type typ, and ok.

    Go writes the match through a pointer target and returns a bool; Python has no
    output pointer, so the match is returned as a (value, ok) pair, the same shape
    a type assertion takes. It honors an As(type) method and walks the multi-error
    tree, stopping at the first match as Go does.
    """
    while err is not None:
        if isinstance(err, typ):
            return err, True
        as_method = getattr(err, "As", None)
        if as_method is not None:
            got, ok = as_method(typ)
            if ok:
                return got, True
        nxt = getattr(err, "Unwrap", None)
        if nxt is None:
            return None, False
        u = nxt()
        if isinstance(u, list):
            for e in u:
                got, ok = errors_as(e, typ)
                if ok:
                    return got, True
            return None, False
        err = u
    return None, False


def errors_join(*errs):
    """errors.Join: wrap the non-nil errors, or None when every argument is nil."""
    kept = [e for e in errs if e is not None]
    if not kept:
        return None
    return _JoinError(kept)


def _runtime_error(msg):
    """Return the GoPanic a runtime check raises, carrying a runtime error value.

    A failed bounds check or a nil pointer dereference panics the Go way: the
    returned GoPanic carries a _RuntimeError whose Error reads "runtime error: "
    and msg, so a recover sees Go's message and an unrecovered one prints it in the
    banner.
    """
    return GoPanic(_RuntimeError(msg))


def _nil_map_error():
    """Return the GoPanic a write to a nil map raises, a plain-message runtime panic."""
    return GoPanic(_PlainError("assignment to entry in nil map"))


def _recover():
    """Return the in-flight panic value, or None when no Go panic is being handled.

    It reads the exception currently being handled from sys.exc_info, and when that
    is a GoPanic not yet consumed it marks the panic recovered so the deferred-call
    harness stops the unwind and hands the value back. Called with no panic in
    flight, or on an already-consumed one, it returns None, matching Go's recover
    returning nil outside a panicking deferred call.
    """
    exc = sys.exc_info()[1]
    if isinstance(exc, GoPanic) and not exc.recovered:
        exc.recovered = True
        return exc.value
    return None


def _run_defers(defers):
    """Run the recorded deferred calls last-in-first-out, draining the list as it goes.

    Each call is popped before it runs, so a deferred call that itself panics leaves
    only the remaining, not-yet-run calls behind, and the handler that catches the
    new panic runs those and no more, matching Go where the rest of the deferred
    calls still run as a panic climbs the stack. Draining also makes a second run a
    no-op, so a normal return and a panic handler never double-run a call.
    """
    while defers:
        fn, args = defers.pop()
        fn(*args)


def panic_message(value):
    """Render a panic value the way Go's runtime prints it in the crash banner.

    A nil panic reads as Go 1.21's message, an error renders through Error, a Go
    string (Python bytes) decodes to its text, a Python string prints itself, a
    Stringer renders through String, and any other value falls back to Go's default
    format, so the banner line matches go run for the common panic kinds.
    """
    if value is None:
        return "panic called with nil argument"
    err = getattr(value, "Error", None)
    if callable(err):
        # Error hands back a Go string, which is bytes for a user error and a plain
        # str for a runtime error, so go_str decodes either into the banner text.
        return go_str(err())
    if isinstance(value, bytes):
        return value.decode("utf-8", "replace")
    if isinstance(value, str):
        return value
    s = getattr(value, "String", None)
    if callable(s):
        return go_str(s())
    return go_str(value)


def _crash(p):
    """Print the Go panic banner to stderr and exit with status 2.

    An unrecovered panic that runs off the top of main crashes the program the Go
    way: the banner reads "panic: " and the rendered value, a goroutine header
    follows, and the process exits with status 2. os._exit is deliberate so no
    finally or atexit handler runs, matching Go's abrupt crash rather than a
    catchable SystemExit. Standard output is flushed first, since os._exit skips
    the buffer flush and a deferred call that printed on the way out would
    otherwise lose its output.
    """
    sys.stdout.flush()
    sys.stderr.write("panic: " + panic_message(p.value) + "\n\n")
    sys.stderr.write("goroutine 1 [running]:\n")
    sys.stderr.flush()
    os._exit(2)


def _fatal(msg):
    """Print Go's fatal-error banner to stderr and exit with status 2.

    A fatal error, unlike a panic, cannot be recovered and runs no deferred calls,
    so it exits directly rather than raising: the banner reads "fatal error: " and
    the message, a goroutine header follows, and the process exits with status 2.
    The sync package raises one for an unlock of an unheld lock, which Go reports as
    a fatal error rather than a catchable panic.
    """
    sys.stdout.flush()
    sys.stderr.write("fatal error: " + msg + "\n\n")
    sys.stderr.write("goroutine 1 [running]:\n")
    sys.stderr.flush()
    os._exit(2)


# Goroutines. A Go goroutine is an independently scheduled call, and the compiled
# tier runs each on its own OS thread so a real stack backs local escapes and a
# blocking call blocks only its own thread. The thread is a daemon, so it is
# abandoned when main returns rather than joined, exactly as Go drops live
# goroutines when the main goroutine finishes.


def go(fn, *args):
    """Spawn fn(*args) on a new daemon OS thread, Go's go statement.

    A panic that runs off the top of a goroutine crashes the whole program, it
    does not just kill the one goroutine, so the body runs under a handler that
    turns an escaping panic into the same banner and exit 2 an unrecovered panic
    in main produces. A Python thread otherwise prints a traceback and lets the
    process live on, which would silently diverge from Go, so the fallback catch
    forces the crash for any escaping exception. The arguments are evaluated in
    the caller before go is entered, matching Go's evaluation of a go statement's
    call arguments at the point the statement runs.
    """

    def _run():
        try:
            fn(*args)
        except GoPanic as _p:
            _crash(_p)
        except BaseException as _exc:  # noqa: BLE001
            _crash(GoPanic(_exc))

    threading.Thread(target=_run, daemon=True).start()


def _sleep(ns):
    """Pause the current goroutine for a Duration, Go's time.Sleep.

    The argument is a Duration in nanoseconds, and Python's time.sleep takes
    seconds, so the count is scaled. A zero or negative Duration returns at once,
    the way Go's time.Sleep does, without touching the clock.
    """
    if ns > 0:
        time.sleep(ns / 1e9)


# Channels. A Go channel is not a queue: an unbuffered channel is a rendezvous, a
# direct handoff where the send completes only when a receiver takes the value in
# the same instant, which programs lean on as a happens-before edge. There is no
# standard-library primitive with that shape, so the runtime builds one class on
# one shared condition, _chan_cond, whose lock guards every channel's fields. One
# lock across all channels is what lets a select wait on several at once and be
# woken by an operation on any of them, and it removes the cross-channel ordering
# deadlock a lock per channel would risk.


# The one condition every channel and every select shares. A send, a receive, or
# a close notifies it, so a select parked on several channels wakes when any of
# them changes, and a channel operation and a select never take two locks in a
# different order.
_chan_cond = threading.Condition()


class Chan:
    """A Go channel, a typed conduit with rendezvous and close semantics.

    The buffer holds handed-off or buffered values, the capacity is zero for an
    unbuffered channel, and the zero factory builds the element's zero value for a
    receive on a closed channel, called fresh each time so a struct zero is not
    shared. The receiver count is the number of receivers parked on this channel,
    which is what makes a send to an unbuffered channel ready in a select. Every
    field is guarded by the shared _chan_cond, so a parked send or receive
    consumes nothing until another operation wakes it to re-check.
    """

    __slots__ = ("_cap", "_buf", "_closed", "_zero", "_recv_waiting")

    def __init__(self, cap, zero):
        self._cap = cap
        self._buf = []
        self._closed = False
        self._zero = zero
        self._recv_waiting = 0

    def send(self, value):
        with _chan_cond:
            self._send_locked(value)

    def _send_locked(self, value):
        """Send, holding _chan_cond. The plain send path, which for an unbuffered
        channel parks until a receiver takes the value, the rendezvous Go
        promises."""
        if self._closed:
            raise GoPanic("send on closed channel")
        if self._cap == 0:
            # Unbuffered: deposit the value and park until a receiver has taken
            # it, so the send returns only once the buffer is empty again.
            self._buf.append(value)
            _chan_cond.notify_all()
            while self._buf and not self._closed:
                _chan_cond.wait()
            if self._closed and self._buf:
                raise GoPanic("send on closed channel")
            return
        # Buffered: block only while the buffer is full, then deposit.
        while len(self._buf) >= self._cap and not self._closed:
            _chan_cond.wait()
        if self._closed:
            raise GoPanic("send on closed channel")
        self._buf.append(value)
        _chan_cond.notify_all()

    def recv(self):
        with _chan_cond:
            return self._recv_locked()

    def _recv_locked(self):
        """Receive, holding _chan_cond, counting this receiver while it is parked
        so a select send to an unbuffered channel can see a waiting receiver."""
        self._recv_waiting += 1
        try:
            while True:
                if self._buf:
                    value = self._buf.pop(0)
                    _chan_cond.notify_all()
                    return value, True
                if self._closed:
                    # A closed and drained channel is always ready and never
                    # blocks, which is what terminates a range and what makes a
                    # closed channel ready in a select.
                    return self._zero(), False
                _chan_cond.wait()
        finally:
            self._recv_waiting -= 1

    def close(self):
        with _chan_cond:
            if self._closed:
                raise GoPanic("close of closed channel")
            self._closed = True
            _chan_cond.notify_all()

    def _recv_ready(self):
        """Whether a receive would proceed without blocking: a buffered or
        handed-off value is waiting, or the channel is closed and so always
        ready. Called under _chan_cond by select."""
        return len(self._buf) > 0 or self._closed

    def _send_ready(self):
        """Whether a send would proceed without blocking: the buffer has room, a
        receiver is parked with no pending value already matched to it, or the
        channel is closed so the send is ready to panic the Go way. Called under
        _chan_cond by select."""
        return len(self._buf) < self._cap or self._recv_waiting > len(self._buf) or self._closed

    def _send_deposit(self, value):
        """Deposit a value for a select send whose readiness was just checked
        under the lock. It does not wait for the rendezvous to drain, so a select
        never blocks after committing to a case; the parked receiver that made
        the case ready takes the value on the next wake."""
        if self._closed:
            raise GoPanic("send on closed channel")
        self._buf.append(value)
        _chan_cond.notify_all()


def chan_send(ch, value):
    """Send on a channel, Go's ch <- value, blocking forever on a nil channel.

    A nil channel blocks both send and receive forever, a real idiom for
    disabling an operation, so the nil case parks on a condition nothing ever
    signals rather than raising.
    """
    if ch is None:
        _block_forever()
    ch.send(value)


def chan_recv(ch):
    """Receive one value from a channel, Go's v := <-ch, dropping the ok flag.

    A nil channel blocks forever. The comma-ok and range forms that need the ok
    flag route through their own helper.
    """
    if ch is None:
        _block_forever()
    value, _ok = ch.recv()
    return value


def chan_recv_ok(ch):
    """Receive a value and the ok flag, Go's v, ok := <-ch and the range form.

    ok is false when the channel is closed and drained, which is what ends a
    range over a channel. A nil channel blocks forever, so the flag never comes.
    """
    if ch is None:
        _block_forever()
    return ch.recv()


def chan_close(ch):
    """Close a channel, Go's close(ch). Closing a nil channel panics, and the
    close method panics on an already closed channel, matching Go."""
    if ch is None:
        raise GoPanic("close of nil channel")
    ch.close()


def chan_len(ch):
    """Number of values buffered in a channel, Go's len(ch). A nil channel is
    empty, so its length is zero. The buffer is read under the channel lock so the
    count is consistent with concurrent sends and receives."""
    if ch is None:
        return 0
    with _chan_cond:
        return len(ch._buf)


def chan_cap(ch):
    """Buffer capacity of a channel, Go's cap(ch). A nil channel has no buffer, so
    its capacity is zero, and an unbuffered channel's capacity is zero too."""
    if ch is None:
        return 0
    return ch._cap


# A select case is a tuple whose first element tags its direction, 0 for a
# receive and 1 for a send, followed by the channel and, for a send, the value.
# An integer tag keeps the case a plain tuple the lowering can build and avoids
# the Go-string-is-bytes mismatch a text tag would bring.
_SEL_RECV = 0


def _select_ready(case):
    """Whether a select case can proceed. A nil-channel case is never ready and is
    skipped, matching Go, so a select over only nil channels with no default
    blocks forever. A receive case is ready when a receive would not block, a send
    case when a send would not block."""
    ch = case[1]
    if ch is None:
        return False
    if case[0] == _SEL_RECV:
        return ch._recv_ready()
    return ch._send_ready()


def _select_fire(case, index):
    """Execute a ready select case and return the triple the caller dispatches on,
    the case index with the received value and ok flag for a receive, or the index
    with a placeholder pair for a send."""
    ch = case[1]
    if case[0] == _SEL_RECV:
        value, ok = ch._recv_locked()
        return index, value, ok
    ch._send_deposit(case[2])
    return index, None, False


def select(has_default, *cases):
    """Run Go's select: choose uniformly at random among the ready cases, take the
    default when none is ready and one is given, otherwise park until a case
    becomes ready and choose again.

    cases is the per-case tuples in source order, (0, ch) for a receive or
    (1, ch, value) for a send, and the returned index is the position of the
    chosen case among them, or minus one for the default. Go mandates the uniform
    choice among ready cases, so the picker is unbiased rather than first-ready.

    A parked select registers as a receiver on each of its receive cases so a send
    to an unbuffered channel can see it, waits on the shared condition so any
    channel operation wakes it, then deregisters and re-evaluates, since between
    the wake and the re-lock another goroutine may have taken the value.
    """
    with _chan_cond:
        while True:
            ready = [i for i in range(len(cases)) if _select_ready(cases[i])]
            if ready:
                index = random.choice(ready)
                return _select_fire(cases[index], index)
            if has_default:
                return -1, None, False
            for case in cases:
                if case[0] == _SEL_RECV and case[1] is not None:
                    case[1]._recv_waiting += 1
            _chan_cond.wait()
            for case in cases:
                if case[0] == _SEL_RECV and case[1] is not None:
                    case[1]._recv_waiting -= 1


def _block_forever():
    """Park the current goroutine on a condition that is never signaled.

    A goroutine blocked on a nil channel is stuck until the program exits,
    exactly as in Go, so it waits on a private condition no other code holds.
    """
    cond = threading.Condition()
    with cond:
        while True:
            cond.wait()


# The sync package. Most of Go's sync primitives map onto threading, but a
# read-write lock and a join-group have no threading equivalent, so the shim
# builds them on a Condition. Every sync value is a reference object the compiler
# constructs once and shares through a pointer, never copied, which matches Go
# where copying a used sync value is a vet error. The free functions below carry
# each operation so the crash guard can list the ones that panic the Go way.


class Mutex:
    """A mutual-exclusion lock, threading.Lock with Go's unlock-of-unlocked panic.

    threading.Lock is not owned by a thread, so a goroutine may unlock a mutex a
    different goroutine locked, which Go allows and threading.RLock would not.
    """

    __slots__ = ("_lock",)

    def __init__(self):
        self._lock = threading.Lock()

    # Lock, Unlock, and TryLock are also carried as methods so a Mutex used through
    # the sync.Locker interface, the way sync.NewCond takes it, answers the call; a
    # direct mu.Lock() still lowers to the free function so the crash guard names it.
    def Lock(self):
        mutex_lock(self)

    def Unlock(self):
        mutex_unlock(self)

    def TryLock(self):
        return mutex_try_lock(self)


def mutex_lock(m):
    m._lock.acquire()


def mutex_unlock(m):
    try:
        m._lock.release()
    except RuntimeError:
        _fatal("sync: unlock of unlocked mutex")


def mutex_try_lock(m):
    return m._lock.acquire(blocking=False)


class RWMutex:
    """A writer-preferring read-write lock, built on a Condition since threading
    has none. A waiting writer blocks new readers, which is Go's no-writer-
    starvation behavior."""

    __slots__ = ("_cond", "_readers", "_writer", "_waiting_writers")

    def __init__(self):
        self._cond = threading.Condition()
        self._readers = 0
        self._writer = False
        self._waiting_writers = 0

    # As with Mutex, the operations are also methods so an RWMutex passed as a
    # sync.Locker, whose Lock and Unlock are the write lock, answers through the
    # interface; a direct call still lowers to the free function.
    def Lock(self):
        rwmutex_lock(self)

    def Unlock(self):
        rwmutex_unlock(self)

    def RLock(self):
        rwmutex_rlock(self)

    def RUnlock(self):
        rwmutex_runlock(self)

    def TryLock(self):
        return rwmutex_try_lock(self)

    def TryRLock(self):
        return rwmutex_try_rlock(self)


def rwmutex_rlock(m):
    with m._cond:
        while m._writer or m._waiting_writers > 0:
            m._cond.wait()
        m._readers += 1


def rwmutex_runlock(m):
    with m._cond:
        if m._readers == 0:
            _fatal("sync: RUnlock of unlocked RWMutex")
        m._readers -= 1
        if m._readers == 0:
            m._cond.notify_all()


def rwmutex_lock(m):
    with m._cond:
        m._waiting_writers += 1
        while m._writer or m._readers > 0:
            m._cond.wait()
        m._waiting_writers -= 1
        m._writer = True


def rwmutex_unlock(m):
    with m._cond:
        if not m._writer:
            _fatal("sync: Unlock of unlocked RWMutex")
        m._writer = False
        m._cond.notify_all()


def rwmutex_try_lock(m):
    with m._cond:
        if m._writer or m._readers > 0 or m._waiting_writers > 0:
            return False
        m._writer = True
        return True


def rwmutex_try_rlock(m):
    with m._cond:
        if m._writer or m._waiting_writers > 0:
            return False
        m._readers += 1
        return True


class WaitGroup:
    """A join-group counter, a count plus a Condition since threading has no
    join-group. Wait parks until the count reaches zero."""

    __slots__ = ("_cond", "_count")

    def __init__(self):
        self._cond = threading.Condition()
        self._count = 0


def waitgroup_add(wg, delta):
    with wg._cond:
        wg._count += delta
        if wg._count < 0:
            raise GoPanic("sync: negative WaitGroup counter")
        if wg._count == 0:
            wg._cond.notify_all()


def waitgroup_done(wg):
    waitgroup_add(wg, -1)


def waitgroup_wait(wg):
    with wg._cond:
        while wg._count > 0:
            wg._cond.wait()


class Once:
    """A do-once guard, a flag double-checked under a lock so the fast path after
    the first call is a plain flag read."""

    __slots__ = ("_done", "_lock")

    def __init__(self):
        self._done = False
        self._lock = threading.Lock()


def once_do(o, fn):
    if o._done:
        return
    with o._lock:
        if not o._done:
            try:
                fn()
            finally:
                o._done = True


class Cond:
    """A condition variable over a caller-held Locker, sync.Cond.

    Go's Cond hands Wait a Locker the caller already holds, releases it while the
    goroutine sleeps, and re-takes it on wake. The runtime keeps its own internal
    Condition, distinct from the user Locker, so a Signal that races a Wait cannot
    slip in during the gap between releasing the user lock and starting to sleep,
    which is the lost wakeup Go's Cond does not have. L is the Locker, reached the
    Go way as c.L.
    """

    __slots__ = ("L", "_cond")

    def __init__(self, locker):
        self.L = locker
        self._cond = threading.Condition(threading.Lock())


def NewCond(locker):
    """Build the Cond sync.NewCond returns over a Locker, in practice a Mutex."""
    return Cond(locker)


def cond_wait(c):
    """Wait for a signal, Go's Cond.Wait, called with c.L held.

    The internal lock is taken before c.L is released, so a Signal or Broadcast,
    which must take that same internal lock to notify, cannot run between the
    release and the sleep, and no wakeup is lost. On wake the internal lock is
    dropped and c.L is re-acquired, leaving the caller holding c.L again as Go
    promises.
    """
    c._cond.acquire()
    c.L.Unlock()
    try:
        c._cond.wait()
    finally:
        c._cond.release()
        c.L.Lock()


def cond_signal(c):
    """Wake one waiter, Go's Cond.Signal. A signal with no waiter is dropped, since
    Go does not save it, which the notify under the internal lock reproduces."""
    with c._cond:
        c._cond.notify()


def cond_broadcast(c):
    """Wake every waiter, Go's Cond.Broadcast."""
    with c._cond:
        c._cond.notify_all()


class SyncMap:
    """A concurrent map, sync.Map, a dict behind a lock.

    Go's sync.Map trades a plain map plus a Mutex for less contention on some
    workloads, but its observable behavior is a locked map, which is what the
    runtime provides: every operation takes the lock, and Range walks a snapshot so
    the callback may store or delete without deadlocking or skipping.
    """

    __slots__ = ("_d", "_lock")

    def __init__(self):
        self._d = {}
        self._lock = threading.Lock()


def syncmap_store(m, k, v):
    with m._lock:
        m._d[k] = v


def syncmap_load(m, k):
    """Load in comma-ok form, Go's value, ok := m.Load(k): (value, True) on a hit,
    (None, False) on a miss, so a present nil is told apart from an absent key."""
    with m._lock:
        if k in m._d:
            return m._d[k], True
        return None, False


def syncmap_load_or_store(m, k, v):
    """Load the existing value or store and return the new one, Go's LoadOrStore:
    (actual, True) when the key was present, (v, False) when it was just stored."""
    with m._lock:
        if k in m._d:
            return m._d[k], True
        m._d[k] = v
        return v, False


def syncmap_load_and_delete(m, k):
    """Load and delete in one step, Go's LoadAndDelete: (value, True) when the key
    was present and is now removed, (None, False) when it was absent."""
    with m._lock:
        if k in m._d:
            return m._d.pop(k), True
        return None, False


def syncmap_delete(m, k):
    with m._lock:
        m._d.pop(k, None)


def syncmap_range(m, fn):
    """Call fn for each entry until it returns False, Go's Range.

    A snapshot is taken under the lock and then walked with the lock released, so
    the callback may call Store or Delete without deadlocking, and a concurrent
    change does not raise the way mutating a dict mid-iteration would. Go promises
    no more than that a Range sees each key present for the whole call at most once.
    """
    with m._lock:
        items = list(m._d.items())
    for k, v in items:
        if not fn(k, v):
            break


class Pool:
    """A free list of reusable values, sync.Pool.

    Go's Pool is a best-effort cache with no liveness promise: Get returns a pooled
    value or, when the pool is empty, New(), and Put offers a value back. The
    runtime keeps a plain locked list, which honors that contract without modeling
    the per-P sharding or GC eviction, neither of which a program may rely on.
    """

    __slots__ = ("_items", "_lock", "_new")

    def __init__(self, new=None):
        self._items = []
        self._lock = threading.Lock()
        self._new = new


def pool_get(p):
    """Return a pooled value, or New() when the pool is empty, or None when it is
    empty and New is nil, matching Go's Pool.Get. New runs outside the lock so a
    slow constructor does not block another Get."""
    with p._lock:
        if p._items:
            return p._items.pop()
    if p._new is not None:
        return p._new()
    return None


def pool_put(p, x):
    with p._lock:
        p._items.append(x)


# The sync/atomic value types. Go's atomic.Int64 and its siblings are structs with
# atomic Load, Store, Add, Swap, and CompareAndSwap, which the runtime models as a
# locked cell: even a load takes the lock, since the free-threaded build gives no
# tear-free read for free, and an Add wraps at the type's width the way the fixed
# width integer helpers do. The free-function forms, atomic.AddInt64 over a pointer,
# wait on the pointer-to-scalar slice.


class _Atomic:
    """A locked integer cell, the shared body of the atomic integer types.

    The mask is the width helper for the type, so Int32 sign-extends at 32 bits and
    Uint64 masks at 64, and every operation runs under the lock so a concurrent Add
    and Load never interleave a half-updated value.
    """

    __slots__ = ("_v", "_lock", "_mask")

    def __init__(self, mask):
        self._mask = mask
        self._v = mask(0)
        self._lock = threading.Lock()


def AtomicInt32():
    return _Atomic(_i32)


def AtomicInt64():
    return _Atomic(_i64)


def AtomicUint32():
    return _Atomic(_u32)


def AtomicUint64():
    return _Atomic(_u64)


class _AtomicBool:
    """A locked boolean cell, atomic.Bool. It has no Add, so it carries no width
    mask, only Load, Store, Swap, and CompareAndSwap under the lock."""

    __slots__ = ("_v", "_lock")

    def __init__(self):
        self._v = False
        self._lock = threading.Lock()


def AtomicBool():
    return _AtomicBool()


def atomic_load(a):
    with a._lock:
        return a._v


def atomic_store(a, v):
    with a._lock:
        a._v = v


def atomic_add(a, delta):
    """Add delta and return the new value, Go's atomic Add, wrapping at the type's
    width so an Int32 overflow rolls over exactly as Go's does."""
    with a._lock:
        a._v = a._mask(a._v + delta)
        return a._v


def atomic_swap(a, new):
    """Store new and return the previous value, Go's atomic Swap."""
    with a._lock:
        old = a._v
        a._v = new
        return old


def atomic_cas(a, old, new):
    """Store new only if the cell still holds old, returning whether it swapped,
    Go's atomic CompareAndSwap."""
    with a._lock:
        if a._v == old:
            a._v = new
            return True
        return False


# String helpers. A Go string is Python bytes, so byte indexing, length, and
# comparison need no helper, but ranging over a string decodes UTF-8 one rune at
# a time, yielding the byte index of each rune and the rune itself. The decoder
# reproduces Go's rules exactly, including the accept ranges for each leading
# byte and the U+FFFD replacement for an invalid or truncated sequence, so a
# for range over a string steps rune by rune the way Go does.


def _decode_rune(s, i):
    """Decode the UTF-8 rune at byte offset i, returning the rune and its width.

    An invalid or truncated sequence returns the replacement rune U+FFFD with a
    width of one byte, exactly as Go's range over a string and utf8.DecodeRune do.
    """
    b0 = s[i]
    if b0 < 0x80:
        return b0, 1
    n = len(s)
    if 0xC2 <= b0 < 0xE0:
        if i + 1 < n and 0x80 <= s[i + 1] < 0xC0:
            return ((b0 & 0x1F) << 6) | (s[i + 1] & 0x3F), 2
        return 0xFFFD, 1
    if 0xE0 <= b0 < 0xF0:
        lo = 0xA0 if b0 == 0xE0 else 0x80
        hi = 0x9F if b0 == 0xED else 0xBF
        if i + 2 < n and lo <= s[i + 1] <= hi and 0x80 <= s[i + 2] < 0xC0:
            return ((b0 & 0x0F) << 12) | ((s[i + 1] & 0x3F) << 6) | (s[i + 2] & 0x3F), 3
        return 0xFFFD, 1
    if 0xF0 <= b0 <= 0xF4:
        lo = 0x90 if b0 == 0xF0 else 0x80
        hi = 0x8F if b0 == 0xF4 else 0xBF
        if (
            i + 3 < n
            and lo <= s[i + 1] <= hi
            and 0x80 <= s[i + 2] < 0xC0
            and 0x80 <= s[i + 3] < 0xC0
        ):
            return (
                ((b0 & 0x07) << 18)
                | ((s[i + 1] & 0x3F) << 12)
                | ((s[i + 2] & 0x3F) << 6)
                | (s[i + 3] & 0x3F)
            ), 4
        return 0xFFFD, 1
    return 0xFFFD, 1


# Map helpers. A Go map is a Python dict, but three behaviors need the runtime: a
# read of a missing key returns the value type's zero rather than raising, a
# range takes a snapshot so a delete during iteration is safe the way Go's is,
# and a nil map reads as empty yet panics on write. The nil map is a distinct
# sentinel so those behaviors are exact, since a bare empty dict would allow the
# write Go forbids.


class _NilMap:
    """The nil map sentinel: reads as empty, yields nothing, panics on write.

    A Go nil map returns the value type's zero on every read, has length zero, and
    yields no iterations, all of which this object provides, and it raises Go's
    "assignment to entry in nil map" panic on any write, so a read-only use is fine
    and a write stops loudly, exactly as Go.
    """

    __slots__ = ()

    def __len__(self):
        return 0

    def __contains__(self, key):
        return False

    def get(self, key, default=None):
        return default

    def __iter__(self):
        return iter(())

    def items(self):
        return ()

    def keys(self):
        return ()

    def __setitem__(self, key, value):
        raise _nil_map_error()


NIL_MAP = _NilMap()


def _map_index(m, k, zero):
    """Return m[k], or the value type's zero when the key is missing, matching Go.

    Go returns the zero value on a miss rather than raising, so the read defaults
    to the caller-supplied zero, and a nil map reads as empty so every key misses.
    """
    if m is NIL_MAP:
        return zero
    return m.get(k, zero)


def _map_lookup(m, k, zero):
    """Return the comma-ok pair (value, present) for m[k], Go's v, ok := m[k].

    On a hit the stored value and True come back, and on a miss the value type's
    zero and False, so ok distinguishes a present zero from an absent key. A nil
    map misses every key.
    """
    if m is NIL_MAP:
        return (zero, False)
    if k in m:
        return (m[k], True)
    return (zero, False)


def _assert_pass(v, typ, structural):
    """Report whether v's dynamic type satisfies a type assertion x.(T) to typ.

    A concrete target needs an exact dynamic-type match, so type(v) is typ, which
    also keeps a Go bool from passing an int assertion since a Python bool is an
    int subtype that isinstance would wave through. An interface target needs
    structural satisfaction, so isinstance against its runtime_checkable Protocol.
    A nil interface, None, holds no dynamic type and satisfies neither.
    """
    if v is None:
        return False
    if structural:
        return isinstance(v, typ)
    return type(v) is typ


def _type_assert(v, typ, structural, iface_name, target_name, methods):
    """Go's one-result assertion x.(T): return the value when it holds a T, else panic.

    The panic carries a TypeAssertionError whose message matches Go's, so a recover
    reads the same text and an unrecovered failure crashes with Go's exit status.
    """
    if _assert_pass(v, typ, structural):
        return v
    raise GoPanic(TypeAssertionError(_assert_fail_message(v, structural, iface_name, target_name, methods)))


def _type_assert_ok(v, typ, structural, zero):
    """Go's comma-ok assertion v, ok := x.(T): (value, True) on a hit, (zero, False) on a miss."""
    if _assert_pass(v, typ, structural):
        return (v, True)
    return (zero, False)


def _assert_fail_message(v, structural, iface_name, target_name, methods):
    """Build a failed assertion's message as bytes, a Go string like every other.

    Go names the source interface, the dynamic type it held, and the target that
    did not match. A concrete target that does not match reads "is T, not U". An
    interface target the value fails to implement reads "is not U: missing method M",
    naming the first method of the target the value lacks, so the message ends the
    way Go's does for a structural miss.
    """
    if v is None:
        # A nil interface carries no dynamic type, so Go names the source with the
        # bare word interface here rather than the static source type.
        return b"interface conversion: interface is nil, not " + target_name
    got = _go_type_name(v)
    if structural:
        missing = _first_missing_method(v, methods)
        return b"interface conversion: " + got + b" is not " + target_name + b": missing method " + missing
    return b"interface conversion: " + iface_name + b" is " + got + b", not " + target_name


def _first_missing_method(v, methods):
    """Return the first interface method name, as bytes, the value does not provide."""
    for name in methods:
        if not callable(getattr(v, name.decode("utf-8"), None)):
            return name
    return b""


def _go_type_name(v):
    """Best-effort Go type name for a dynamic value, used only in a failed assertion's message."""
    if isinstance(v, bool):
        return b"bool"
    if isinstance(v, int):
        return b"int"
    if isinstance(v, float):
        return b"float64"
    if isinstance(v, bytes):
        return b"string"
    return type(v).__name__.encode("utf-8")


def _map_delete(m, k):
    """Delete key k from map m, a no-op when the key or the map is absent.

    Go's delete of a missing key does nothing, which pop with a default matches,
    and delete on a nil map is allowed and does nothing.
    """
    if m is not NIL_MAP:
        m.pop(k, None)


def _map_clear(m):
    """Remove every entry from map m, matching Go 1.21 clear, a no-op on a nil map."""
    if m is not NIL_MAP:
        m.clear()


def _map_items(m):
    """Return a snapshot list of the map's key-value pairs for a range to walk.

    Ranging the snapshot rather than the live map lets the body delete entries the
    way a Go range can, since mutating a dict mid-iteration raises in Python, and a
    nil map yields nothing.
    """
    if m is NIL_MAP:
        return []
    return list(m.items())


def _map_keys(m):
    """Return a snapshot list of the map's keys for a key-only range to walk.

    Like the item snapshot this lets the body delete during the range, and a nil
    map yields nothing.
    """
    if m is NIL_MAP:
        return []
    return list(m.keys())


class FieldPtr:
    """A Go pointer into a struct field, &s.Field, that reads and writes through.

    A plain snapshot would break aliasing, since a write through the pointer must
    be visible in the struct, so the pointer holds the struct object and the field
    name and reads or writes the live attribute. Copying the pointer shares the
    same struct, exactly as copying a Go pointer shares the pointee.
    """

    __slots__ = ("obj", "name")

    def __init__(self, obj, name):
        self.obj = obj
        self.name = name

    def get(self):
        return getattr(self.obj, self.name)

    def set(self, v):
        setattr(self.obj, self.name, v)

    def __eq__(self, other):
        # Go pointer equality is same-address, so two field pointers are equal when
        # they name the same field of the same object, not when they are the same
        # Python object, which is why &s.F == &s.F holds even from fresh pointers.
        if not isinstance(other, FieldPtr):
            return NotImplemented
        return self.obj is other.obj and self.name == other.name

    def __hash__(self):
        return hash((id(self.obj), self.name))


class IndexPtr:
    """A Go pointer into an array or slice element, &a[i], that reads and writes.

    Like FieldPtr it holds the container and the index and goes through to the live
    element, so a write through the pointer is seen in the array or slice, and a
    read sees a later write to the element. The container is the same Python list
    or slice header the element lives in, so no copy is taken.
    """

    __slots__ = ("seq", "idx")

    def __init__(self, seq, idx):
        self.seq = seq
        self.idx = idx

    def get(self):
        return self.seq[self.idx]

    def set(self, v):
        self.seq[self.idx] = v

    def __eq__(self, other):
        # Same-address equality again, so &a[i] == &a[i] holds for the same
        # container and index even though the two pointer objects are distinct.
        if not isinstance(other, IndexPtr):
            return NotImplemented
        return self.seq is other.seq and self.idx == other.idx

    def __hash__(self):
        return hash((id(self.seq), self.idx))


class Cell:
    """A Go pointer to a boxed local, the one-slot storage &x names.

    Python has no address-of, so a local whose address is taken is boxed into a
    Cell up front, and every read and write of the local goes through get and set.
    Taking the address is then just naming the cell, so a copy of the pointer shares
    the same cell, and two pointers are equal when they are the same cell, which is
    Python object identity and exactly Go's same-address rule.
    """

    __slots__ = ("value",)

    def __init__(self, value):
        self.value = value

    def get(self):
        return self.value

    def set(self, v):
        self.value = v


class _NilPtr:
    """The nil pointer sentinel, the zero value a pointer variable takes.

    It compares equal only to itself, so p == nil is a plain identity test, and a
    dereference raises Go's nil pointer panic rather than a Python AttributeError,
    so a program that reads through a nil pointer stops the Go way.
    """

    __slots__ = ()

    def get(self):
        raise _runtime_error("invalid memory address or nil pointer dereference")

    def set(self, v):
        raise _runtime_error("invalid memory address or nil pointer dereference")


NIL_PTR = _NilPtr()
