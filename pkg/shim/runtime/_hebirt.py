"""hebi compiled-tier runtime shim.

Emitted Python imports this module for the small pieces of Go's object model
that Python does not provide directly. At M0 that is Go-style value formatting
and the println path that fmt.Println lowers to. The module grows one helper at
a time as later milestones add language surface.
"""

import decimal
import math
import os
import random
import struct
import sys
import threading
import time


def go_str(value):
    """Return Go's fmt default string for a value.

    Go prints booleans as true and false where Python prints True and False, so
    those two are special-cased. Floats are formatted the Go way, which differs
    from Python's str: Go uses the shortest round-tripping form and switches to
    exponent notation at different thresholds. Everything else defers to
    Python's str, which already matches Go for the integers and strings hebi
    covers.
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
        return "[" + " ".join(go_str(e) for e in value) + "]"
    if isinstance(value, Slice):
        # A Go slice prints the same bracket form as an array, walking the header
        # so only the visible length is shown and the backing beyond it stays
        # hidden, matching fmt's view of a slice through its length.
        return "[" + " ".join(go_str(value.array[value.offset + k]) for k in range(value.length)) + "]"
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
        return "map[" + " ".join(go_str(k) + ":" + go_str(v) for k, v in items) + "]"
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
