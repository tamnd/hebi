"""hebi compiled-tier runtime shim.

Emitted Python imports this module for the small pieces of Go's object model
that Python does not provide directly. At M0 that is Go-style value formatting
and the println path that fmt.Println lowers to. The module grows one helper at
a time as later milestones add language surface.
"""

import decimal
import os
import struct
import sys


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


# Float helpers. Go float64 is Python's float, so float64 arithmetic is native,
# but Go float32 is single precision and Python has no 32-bit float, so a
# float32 result is round-tripped back through a 4-byte IEEE single after every
# producing operation, the float analog of the integer width masks. Formatting
# also needs care: fmt prints a float with the shortest round-tripping decimal
# and switches to exponent notation at thresholds that differ from Python's.


def _f32(v):
    """Round a value to IEEE single precision, matching Go's float32."""
    return struct.unpack("f", struct.pack("f", v))[0]


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
        return err()
    if isinstance(value, bytes):
        return value.decode("utf-8", "replace")
    if isinstance(value, str):
        return value
    s = getattr(value, "String", None)
    if callable(s):
        return s()
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
