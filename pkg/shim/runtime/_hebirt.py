"""hebi compiled-tier runtime shim.

Emitted Python imports this module for the small pieces of Go's object model
that Python does not provide directly. At M0 that is Go-style value formatting
and the println path that fmt.Println lowers to. The module grows one helper at
a time as later milestones add language surface.
"""

import decimal
import struct


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
