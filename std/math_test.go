package std

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMathRound(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		x      float64
		places int
		want   float64
	}{
		{1.4, 0, 1},
		{1.5, 0, 2},
		{-1.5, 0, -2}, // half AWAY from zero
		{87.6543, 1, 87.7},
		{87.6543, 2, 87.65},
		{1234, -2, 1200}, // negative places round to hundreds
	} {
		got, err := MathRound(ctx, tc.x, tc.places)
		require.NoError(t, err)
		assert.InDelta(t, tc.want, got, 1e-9, "round(%g, %d)", tc.x, tc.places)
	}

	// Out-of-range places would overflow the scale to Inf and turn a finite
	// number into NaN, so it raises instead.
	_, err := MathRound(ctx, 1.5, 400)
	require.Error(t, err)

	// Non-finite input passes through rather than becoming a confusing number.
	got, err := MathRound(ctx, math.NaN(), 2)
	require.NoError(t, err)
	assert.True(t, math.IsNaN(got))
}

func TestMathTruncRoundsTowardZero(t *testing.T) {
	ctx := context.Background()
	got, err := MathTrunc(ctx, -1.7)
	require.NoError(t, err)
	assert.Equal(t, -1.0, got, "trunc rounds toward zero where floor would give -2")

	got, err = MathTrunc(ctx, 1.7)
	require.NoError(t, err)
	assert.Equal(t, 1.0, got)
}

func TestMathClamp(t *testing.T) {
	ctx := context.Background()

	got, err := MathClamp(ctx, 5, 1, 8)
	require.NoError(t, err)
	assert.Equal(t, 5.0, got)

	got, err = MathClamp(ctx, 0, 1, 8)
	require.NoError(t, err)
	assert.Equal(t, 1.0, got, "below the range clamps up - never zero workers")

	got, err = MathClamp(ctx, 99, 1, 8)
	require.NoError(t, err)
	assert.Equal(t, 8.0, got)

	// An inverted range is a caller bug, not an empty range.
	_, err = MathClamp(ctx, 5, 8, 1)
	require.Error(t, err)
}

func TestMathAggregations(t *testing.T) {
	ctx := context.Background()
	nums := []float64{4, 1, 3, 2}

	sum, err := MathSum(ctx, nums)
	require.NoError(t, err)
	assert.Equal(t, 10.0, sum)

	mean, err := MathMean(ctx, nums)
	require.NoError(t, err)
	assert.Equal(t, 2.5, mean)

	min, err := MathMin(ctx, nums)
	require.NoError(t, err)
	assert.Equal(t, 1.0, min)

	max, err := MathMax(ctx, nums)
	require.NoError(t, err)
	assert.Equal(t, 4.0, max)
}

func TestMathMedian(t *testing.T) {
	ctx := context.Background()

	// Odd count: the middle value.
	got, err := MathMedian(ctx, []float64{3, 1, 2})
	require.NoError(t, err)
	assert.Equal(t, 2.0, got)

	// Even count: the middle pair averaged.
	got, err = MathMedian(ctx, []float64{4, 1, 3, 2})
	require.NoError(t, err)
	assert.Equal(t, 2.5, got)

	// The reason to prefer median: one outlier moves the mean, not the median.
	timings := []float64{10, 11, 12, 11, 900}
	med, err := MathMedian(ctx, timings)
	require.NoError(t, err)
	mean, err := MathMean(ctx, timings)
	require.NoError(t, err)
	assert.Equal(t, 11.0, med)
	assert.Greater(t, mean, 100.0)
}

func TestMathMedianDoesNotReorderTheInput(t *testing.T) {
	// A host method quietly reordering an argument is found late.
	in := []float64{3, 1, 2}
	_, err := MathMedian(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, []float64{3, 1, 2}, in)
}

func TestMathEmptyListBehaviour(t *testing.T) {
	ctx := context.Background()

	// sum of nothing is 0, which is the identity and unambiguous.
	got, err := MathSum(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 0.0, got)

	// The rest RAISE. 0 is a real average, so returning it for "there was
	// nothing to average" would let an empty set silently pass a floor check.
	for name, fn := range map[string]func(context.Context, []float64) (float64, error){
		"mean": MathMean, "median": MathMedian, "min": MathMin, "max": MathMax,
	} {
		_, err := fn(ctx, nil)
		assert.Errorf(t, err, "%s of an empty list must raise", name)
	}
}
