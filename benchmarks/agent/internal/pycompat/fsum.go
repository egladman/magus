package pycompat

import (
	"errors"
	"math"
)

// FSum is math.fsum: Shewchuk partials, so the result is the correctly rounded
// exact sum and statistics.fmean's mean matches to the last bit. Every product
// is rounded through an explicit conversion because Go may otherwise fuse it
// into the following add, which CPython never does.
func FSum(values []float64) (float64, error) {
	var partials []float64
	var specialSum, infSum float64
	for _, x := range values {
		xsave := x
		i := 0
		for _, y := range partials {
			if math.Abs(x) < math.Abs(y) {
				x, y = y, x
			}
			hi := x + y
			yr := hi - x
			lo := y - yr
			if lo != 0.0 {
				partials[i] = lo
				i++
			}
			x = hi
		}
		partials = partials[:i]
		if x != 0.0 {
			if math.IsInf(x, 0) || math.IsNaN(x) {
				if !math.IsInf(xsave, 0) && !math.IsNaN(xsave) {
					return 0, errors.New("fsum: intermediate overflow")
				}
				if math.IsInf(xsave, 0) {
					infSum += xsave
				}
				specialSum += xsave
				partials = partials[:0]
			} else {
				partials = append(partials, x)
			}
		}
	}
	if specialSum != 0.0 {
		if math.IsNaN(infSum) {
			return 0, errors.New("fsum: -inf + inf")
		}
		return specialSum, nil
	}
	hi := 0.0
	n := len(partials)
	if n > 0 {
		n--
		hi = partials[n]
		var lo float64
		for n > 0 {
			x := hi
			n--
			y := partials[n]
			hi = x + y
			yr := hi - x
			lo = y - yr
			if lo != 0.0 {
				break
			}
		}
		// Half-even rounding across partials, CPython's own fixup: without it
		// fsum([1e-16, 1, 1e16]) rounds the last digit down instead of up.
		if n > 0 && ((lo < 0.0 && partials[n-1] < 0.0) || (lo > 0.0 && partials[n-1] > 0.0)) {
			y := float64(lo * 2.0)
			x := hi + y
			yr := x - hi
			if y == yr {
				hi = x
			}
		}
	}
	return hi, nil
}
