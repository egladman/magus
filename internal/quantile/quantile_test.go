package quantile

import (
	"math"
	"testing"
)

func inf() float64 { return math.Inf(+1) }

func TestQuantile(t *testing.T) {
	cases := []struct {
		name    string
		q       float64
		buckets []Bucket
		want    float64
		wantNaN bool
	}{
		{
			name:    "empty buckets",
			q:       0.5,
			buckets: nil,
			wantNaN: true,
		},
		{
			name:    "single finite bucket without +Inf overflow",
			q:       0.5,
			buckets: []Bucket{{UpperBound: 10, CumulativeCount: 5}},
			wantNaN: true,
		},
		{
			name:    "q NaN yields NaN",
			q:       math.NaN(),
			buckets: []Bucket{{UpperBound: 1, CumulativeCount: 0}, {UpperBound: inf(), CumulativeCount: 10}},
			wantNaN: true,
		},
		{
			name:    "q above 1 yields +Inf",
			q:       1.5,
			buckets: []Bucket{{UpperBound: 1, CumulativeCount: 0}, {UpperBound: inf(), CumulativeCount: 10}},
			want:    inf(),
		},
		{
			name:    "q below 0 yields -Inf",
			q:       -0.1,
			buckets: []Bucket{{UpperBound: 1, CumulativeCount: 0}, {UpperBound: inf(), CumulativeCount: 10}},
			want:    math.Inf(-1),
		},
		{
			name: "interpolation midpoint",
			q:    0.5,
			buckets: []Bucket{
				{UpperBound: 1, CumulativeCount: 0},
				{UpperBound: 2, CumulativeCount: 10},
				{UpperBound: inf(), CumulativeCount: 10},
			},
			want: 1.5,
		},
		{
			name: "q at 1.0 reaches upper bound",
			q:    1.0,
			buckets: []Bucket{
				{UpperBound: 1, CumulativeCount: 0},
				{UpperBound: 2, CumulativeCount: 10},
				{UpperBound: inf(), CumulativeCount: 10},
			},
			want: 2.0,
		},
		{
			name: "q below first bound interpolates from zero",
			q:    0.1,
			buckets: []Bucket{
				{UpperBound: 1, CumulativeCount: 2},
				{UpperBound: 2, CumulativeCount: 10},
				{UpperBound: inf(), CumulativeCount: 10},
			},
			want: 0.5,
		},
		{
			name: "rank in +Inf bucket clamps to last finite bound",
			q:    0.9,
			buckets: []Bucket{
				{UpperBound: 1, CumulativeCount: 2},
				{UpperBound: 2, CumulativeCount: 4},
				{UpperBound: inf(), CumulativeCount: 10},
			},
			want: 2.0,
		},
		{
			name: "zero observations yields NaN",
			q:    0.5,
			buckets: []Bucket{
				{UpperBound: 1, CumulativeCount: 0},
				{UpperBound: inf(), CumulativeCount: 0},
			},
			wantNaN: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Quantile(tc.q, tc.buckets)
			if tc.wantNaN {
				if !math.IsNaN(got) {
					t.Fatalf("Quantile(%v) = %v, want NaN", tc.q, got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("Quantile(%v) = %v, want %v", tc.q, got, tc.want)
			}
		})
	}
}

// TestQuantileDoesNotMutateInput confirms the caller's slice order survives a call
// even when the input is given out of ascending order.
func TestQuantileDoesNotMutateInput(t *testing.T) {
	in := []Bucket{
		{UpperBound: inf(), CumulativeCount: 10},
		{UpperBound: 2, CumulativeCount: 10},
		{UpperBound: 1, CumulativeCount: 0},
	}
	_ = Quantile(0.5, in)
	if in[0].UpperBound != inf() || in[1].UpperBound != 2 || in[2].UpperBound != 1 {
		t.Fatalf("Quantile mutated its input slice: %+v", in)
	}
}
