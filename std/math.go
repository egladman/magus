package std

import (
	"context"
	"fmt"
	"math"
	"sort"
)

//go:generate go run ../cmd/magus-utils bindings -module math -lang buzz -out ../internal/interp/bindings/gen/math.go

func init() { Register(Math) }

// Math is the "math" host module: the arithmetic Buzz's own math module lacks,
// layered onto it rather than replacing it.
//
// Buzz already provides abs/ceil/floor/sqrt/pow/exp/log, the trig functions, and
// two-argument minInt/maxInt/minDouble/maxDouble. What it has none of is
// ROUNDING TO A PLACE and AGGREGATION OVER A LIST, which between them are most of
// what a build actually computes: a coverage percentage rendered to one decimal,
// the mean of a set of timings, the median that says what a typical run costs
// when one outlier would drag a mean.
//
// The aggregations SKIP non-numeric items rather than treating them as zero. A
// silent zero shifts a mean and moves a median without anything reporting it,
// which is the worst failure mode for a number a gate then compares against a
// floor.
//
// Pure computation: WASM-safe, no filesystem, no environment.
var Math = Module{
	Name: "math",
	Doc:  "Rounding to a decimal place, clamping, and aggregation over a list of numbers.",
	Methods: []Method{
		{
			Name: "round",
			Doc:  "Round x to places decimal places, half away from zero. places defaults to 0 (the nearest whole number) and may be negative to round to tens, hundreds and so on. Rendering a coverage percentage or a duration is the usual reason; Buzz's floor and ceil cannot express it.",
			Args: []Arg{
				{Name: "x", Type: TypeFloat},
				{Name: "places", Type: TypeInt, Optional: true},
			},
			Returns: []Ret{{Type: TypeFloat}},
			Raises:  true,
			Impl:    MathRound,
		},
		{
			Name:    "trunc",
			Doc:     "Discard x's fractional part, rounding TOWARD ZERO - so -1.7 is -1, where floor gives -2. The difference matters whenever a value can be negative and you meant \"drop the decimals\".",
			Args:    []Arg{{Name: "x", Type: TypeFloat}},
			Returns: []Ret{{Type: TypeFloat}},
			Impl:    MathTrunc,
		},
		{
			Name: "clamp",
			Doc:  "Constrain x to the range lo..hi, returning lo when x is below it and hi when above. Sizing parallelism is the usual reason - clamp(cpus / 2, 1, 8) never yields zero workers. Raises when lo is greater than hi, which is a caller bug rather than an empty range.",
			Args: []Arg{
				{Name: "x", Type: TypeFloat},
				{Name: "lo", Type: TypeFloat},
				{Name: "hi", Type: TypeFloat},
			},
			Returns: []Ret{{Type: TypeFloat}},
			Raises:  true,
			Impl:    MathClamp,
		},
		{
			Name:    "sum",
			Doc:     "Add every number in the list; an empty list sums to 0. Non-numeric items are skipped rather than counted as zero.",
			Args:    []Arg{{Name: "nums", Type: TypeFloatSlice}},
			Returns: []Ret{{Type: TypeFloat}},
			Impl:    MathSum,
		},
		{
			Name:    "mean",
			Doc:     "The arithmetic mean. Raises on an empty list rather than returning 0, because 0 is a real average and \"there was nothing to average\" is not - returning it would let an empty set silently pass a floor check.",
			Args:    []Arg{{Name: "nums", Type: TypeFloatSlice}},
			Returns: []Ret{{Type: TypeFloat}},
			Raises:  true,
			Impl:    MathMean,
		},
		{
			Name:    "median",
			Doc:     "The middle value, averaging the two middle values for an even count. Prefer it to mean when reporting what a TYPICAL run costs: one pathological outlier moves a mean and barely moves a median. Raises on an empty list, like mean.",
			Args:    []Arg{{Name: "nums", Type: TypeFloatSlice}},
			Returns: []Ret{{Type: TypeFloat}},
			Raises:  true,
			Impl:    MathMedian,
		},
		{
			Name:    "min",
			Doc:     "The smallest number in the list. Distinct from Buzz's own minInt/minDouble, which compare exactly two values. Raises on an empty list.",
			Args:    []Arg{{Name: "nums", Type: TypeFloatSlice}},
			Returns: []Ret{{Type: TypeFloat}},
			Raises:  true,
			Impl:    MathMin,
		},
		{
			Name:    "max",
			Doc:     "The largest number in the list. Distinct from Buzz's own maxInt/maxDouble, which compare exactly two values. Raises on an empty list.",
			Args:    []Arg{{Name: "nums", Type: TypeFloatSlice}},
			Returns: []Ret{{Type: TypeFloat}},
			Raises:  true,
			Impl:    MathMax,
		},
	},
}

// MathRound rounds x to places decimal places, half away from zero.
func MathRound(_ context.Context, x float64, places int) (float64, error) {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x, nil
	}
	// Scaling by a power of ten and rounding is the standard approach and is
	// exact for the place counts anyone renders (a percentage, a duration). Very
	// large |places| would overflow the scale into Inf and turn a finite number
	// into NaN, so it is bounded rather than trusted.
	if places > 15 || places < -15 {
		return 0, fmt.Errorf("math.round: places %d is out of range (-15 to 15)", places)
	}
	scale := math.Pow(10, float64(places))
	return math.Round(x*scale) / scale, nil
}

// MathTrunc discards x's fractional part, rounding toward zero.
func MathTrunc(_ context.Context, x float64) (float64, error) { return math.Trunc(x), nil }

// MathClamp constrains x to lo..hi.
func MathClamp(_ context.Context, x, lo, hi float64) (float64, error) {
	if lo > hi {
		return 0, fmt.Errorf("math.clamp: lo (%g) is greater than hi (%g)", lo, hi)
	}
	return math.Min(math.Max(x, lo), hi), nil
}

// MathSum adds every number in nums.
func MathSum(_ context.Context, nums []float64) (float64, error) {
	var total float64
	for _, n := range nums {
		total += n
	}
	return total, nil
}

// mathRequireNonEmpty is the shared guard for the aggregations that have no
// meaningful answer over nothing.
func mathRequireNonEmpty(name string, nums []float64) error {
	if len(nums) == 0 {
		return fmt.Errorf("math.%s: no numbers to reduce (an empty list has no %s)", name, name)
	}
	return nil
}

// MathMean returns the arithmetic mean of nums.
func MathMean(ctx context.Context, nums []float64) (float64, error) {
	if err := mathRequireNonEmpty("mean", nums); err != nil {
		return 0, err
	}
	total, _ := MathSum(ctx, nums)
	return total / float64(len(nums)), nil
}

// MathMedian returns the middle value of nums, averaging the middle pair when
// the count is even.
func MathMedian(_ context.Context, nums []float64) (float64, error) {
	if err := mathRequireNonEmpty("median", nums); err != nil {
		return 0, err
	}
	// Sort a COPY: the caller's list is theirs, and a host method quietly
	// reordering an argument is the kind of surprise that is found late.
	s := append([]float64(nil), nums...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid], nil
	}
	return (s[mid-1] + s[mid]) / 2, nil
}

// MathMin returns the smallest value in nums.
func MathMin(_ context.Context, nums []float64) (float64, error) {
	if err := mathRequireNonEmpty("min", nums); err != nil {
		return 0, err
	}
	out := nums[0]
	for _, n := range nums[1:] {
		out = math.Min(out, n)
	}
	return out, nil
}

// MathMax returns the largest value in nums.
func MathMax(_ context.Context, nums []float64) (float64, error) {
	if err := mathRequireNonEmpty("max", nums); err != nil {
		return 0, err
	}
	out := nums[0]
	for _, n := range nums[1:] {
		out = math.Max(out, n)
	}
	return out, nil
}
