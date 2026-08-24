//go:build linux

package mem

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSwapUsedMeminfo(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want int64
	}{
		{
			"total minus free",
			"MemTotal:       16316200 kB\nSwapTotal:       2097148 kB\nSwapFree:        1048576 kB\n",
			(2097148 - 1048576) * 1024,
		},
		{
			"swap disabled reports nothing rather than zero-used",
			"MemTotal:       16316200 kB\nSwapTotal:             0 kB\nSwapFree:              0 kB\n",
			0,
		},
		{
			// The guard that matters: a total without a free would otherwise report the
			// whole swap device as in use, which reads as a machine about to die.
			"a truncated read is UNKNOWN",
			"MemTotal:       16316200 kB\nSwapTotal:       2097148 kB\n",
			0,
		},
		{"no swap lines", "MemTotal:       16316200 kB\n", 0},
		{"empty", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseSwapUsedMeminfo(strings.NewReader(tc.in)))
		})
	}
}
