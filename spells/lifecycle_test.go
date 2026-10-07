package spells

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLifecycleCycleOf(t *testing.T) {
	golang := Lifecycle{Key: "go", Cycles: []ReleaseCycle{
		{Cycle: "1.2", EOL: "2014-06-01"},
		{Cycle: "1.25", EOL: "2026-08-19"},
		{Cycle: "1.26"},
	}}
	nodejs := Lifecycle{Key: "nodejs", Cycles: []ReleaseCycle{{Cycle: "22"}, {Cycle: "2"}}}

	for _, tc := range []struct {
		name    string
		l       Lifecycle
		version string
		want    string
		found   bool
	}{
		{"a probed canonical version", golang, "v1.25.3", "1.25", true},
		{"a component boundary, not a string prefix", golang, "1.2.2", "1.2", true},
		{"1.25 is not in the 1.2 line", golang, "1.25.0", "1.25", true},
		{"a window floor naming the cycle itself", golang, "1.26", "1.26", true},
		{"the longest match wins", nodejs, "v22.3.0", "22", true},
		{"no line carries it", golang, "1.27.1", "", false},
		{"an empty version matches nothing", golang, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.l.FindCycle(tc.version)
			assert.Equal(t, tc.found, ok)
			assert.Equal(t, tc.want, got.Cycle)
		})
	}
}

func TestLifecycleSupportOf(t *testing.T) {
	day := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	golang := Lifecycle{Key: "go", Cycles: []ReleaseCycle{
		{Cycle: "1.25", EOL: "2026-08-19"},
		{Cycle: "1.26", EOL: "2027-02-10"},
	}}

	c, s := golang.PlaceVersion("v1.25.3", day)
	assert.Equal(t, "1.25", c.Cycle)
	assert.Equal(t, SupportEOL, s)

	c, s = golang.PlaceVersion("v1.26.5", day)
	assert.Equal(t, "1.26", c.Cycle)
	assert.Equal(t, SupportSupported, s)

	c, s = golang.PlaceVersion("v1.99.0", day)
	assert.Equal(t, ReleaseCycle{}, c)
	assert.Equal(t, SupportUnknown, s, "a version no cycle carries is unknown, never supported")
}

func TestReleaseCycleSupportOn(t *testing.T) {
	day := time.Date(2026, 9, 29, 23, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		eol  string
		want Support
	}{
		{"an end date ahead", "2027-02-10", SupportSupported},
		{"an end date behind", "2026-08-19", SupportEOL},
		{"EOL on the day itself", "2026-09-29", SupportEOL},
		{"no end date announced", "", SupportUnannounced},
		{"an end date that is not a date", "soon", SupportUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ReleaseCycle{Cycle: "1.26", EOL: tc.eol}.SupportOn(day))
		})
	}
}
