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
