package sessions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatDur(t *testing.T) {
	assertDur := func(name string, d time.Duration, want string) {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, FormatDur(d))
		})
	}

	assertDur("zero", 0, "")
	assertDur("negative", -time.Second, "")
	assertDur("sub-second", 300*time.Millisecond, "0.3s")
	assertDur("under 10s", 3*time.Second+400*time.Millisecond, "3.4s")
	assertDur("10s boundary", 10*time.Second, "10s")
	assertDur("under 1m", 47*time.Second, "47s")
	assertDur("1m boundary", time.Minute, "1m0s")
	assertDur("minutes+seconds", 2*time.Minute+17*time.Second, "2m17s")
	assertDur("1h boundary", time.Hour, "1h0m")
	assertDur("hours+minutes", 3*time.Hour+5*time.Minute, "3h5m")
}
