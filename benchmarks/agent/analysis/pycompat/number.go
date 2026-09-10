package pycompat

import (
	"fmt"
	"strconv"
	"strings"
)

// Number is a JSON number the way CPython's json reads it: an int when the
// literal carries neither a fraction nor an exponent, a float otherwise. The
// two print differently (3 against 3.0) and arithmetic keeps the distinction
// the way Python's does, so a value passed through from an input file, or a
// median picked out of a list of counts, comes back out in the form it went in.
type Number struct {
	isFloat bool
	i       int64
	f       float64
}

// Int is a Python int.
func Int(i int64) Number { return Number{i: i} }

// Float is a Python float.
func Float(f float64) Number { return Number{isFloat: true, f: f} }

// IsFloat reports whether the value is a float rather than an int.
func (n Number) IsFloat() bool { return n.isFloat }

// Int64 is the int's value; a float reports false.
func (n Number) Int64() (int64, bool) {
	if n.isFloat {
		return 0, false
	}
	return n.i, true
}

// Float64 is float(n).
func (n Number) Float64() float64 {
	if n.isFloat {
		return n.f
	}
	return float64(n.i)
}

// IsZero is `not n`.
func (n Number) IsZero() bool { return n.Float64() == 0 }

// Less orders numerically, as Python compares an int against a float.
func (n Number) Less(o Number) bool {
	if !n.isFloat && !o.isFloat {
		return n.i < o.i
	}
	return n.Float64() < o.Float64()
}

// Add is n + o; int + int stays int.
func (n Number) Add(o Number) Number {
	if !n.isFloat && !o.isFloat {
		return Int(n.i + o.i)
	}
	return Float(n.Float64() + o.Float64())
}

// Sub is n minus o; an int minus an int stays int.
func (n Number) Sub(o Number) Number {
	if !n.isFloat && !o.isFloat {
		return Int(n.i - o.i)
	}
	return Float(n.Float64() - o.Float64())
}

// MulInt is n * k for an int k. The product passes through rounded so the
// compiler cannot fuse it into the add that follows in a caller.
func (n Number) MulInt(k int64) Number {
	if !n.isFloat {
		return Int(n.i * k)
	}
	return Float(rounded(n.f * float64(k)))
}

// rounded returns x stored as a double. A call the compiler cannot inline is
// the barrier that keeps a product from being fused with the add after it
// into one differently rounded operation, which CPython never does and the
// byte-identity bar cannot absorb.
//
//go:noinline
func rounded(x float64) float64 { return x }

// TrueDiv is n / k, Python's true division: always a float.
func (n Number) TrueDiv(k int64) float64 {
	return n.Float64() / float64(k)
}

// String is repr(n).
func (n Number) String() string {
	if n.isFloat {
		return FloatRepr(n.f)
	}
	return strconv.FormatInt(n.i, 10)
}

// MarshalJSON emits repr(n), which is also what json.dumps writes.
func (n Number) MarshalJSON() ([]byte, error) { return []byte(n.String()), nil }

// UnmarshalJSON classifies the literal the way json.loads does.
func (n *Number) UnmarshalJSON(data []byte) error {
	v, err := ParseNumber(string(data))
	if err != nil {
		return err
	}
	*n = v
	return nil
}

// ParseNumber classifies a JSON number literal: a fraction or an exponent
// makes it a float, anything else an int.
func ParseNumber(literal string) (Number, error) {
	if strings.ContainsAny(literal, ".eE") {
		f, err := strconv.ParseFloat(literal, 64)
		if err != nil {
			return Number{}, fmt.Errorf("number %q: %w", literal, err)
		}
		return Float(f), nil
	}
	i, err := strconv.ParseInt(literal, 10, 64)
	if err != nil {
		return Number{}, fmt.Errorf("number %q: %w", literal, err)
	}
	return Int(i), nil
}

// FloatRepr is repr(f) for a Python float: the shortest digits that round
// trip, positional between 1e-4 and 1e16, exponent form outside, and always a
// fraction or an exponent so the text reads back as a float.
func FloatRepr(f float64) string {
	sci := strconv.FormatFloat(f, 'e', -1, 64)
	sign := ""
	if sci[0] == '-' {
		sign = "-"
		sci = sci[1:]
	}
	switch sci {
	case "NaN":
		return "nan"
	case "+Inf":
		return "inf"
	}
	if sign == "-" && sci == "Inf" {
		return "-inf"
	}
	mant, expText, _ := strings.Cut(sci, "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mant, ".", "", 1)
	decpt := exp + 1
	if decpt > -4 && decpt <= 16 {
		switch {
		case decpt <= 0:
			return sign + "0." + strings.Repeat("0", -decpt) + digits
		case decpt >= len(digits):
			return sign + digits + strings.Repeat("0", decpt-len(digits)) + ".0"
		default:
			return sign + digits[:decpt] + "." + digits[decpt:]
		}
	}
	out := sign + digits[:1]
	if len(digits) > 1 {
		out += "." + digits[1:]
	}
	expSign := "+"
	if exp < 0 {
		expSign = "-"
		exp = -exp
	}
	return fmt.Sprintf("%se%s%02d", out, expSign, exp)
}
