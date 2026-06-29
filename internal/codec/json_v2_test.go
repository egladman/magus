//go:build goexperiment.jsonv2

package codec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDurationMarshalsAsString pins the v2 representation: json/v2 has no default
// for time.Duration (go.dev/issue/71631), so the codec marshals it as its string
// form ("6h0m0s") rather than erroring or emitting a raw nanosecond count.
func TestDurationMarshalsAsString(t *testing.T) {
	b, err := Marshal(struct {
		TTL time.Duration `json:"ttl"`
	}{6 * time.Hour})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ttl":"6h0m0s"}`, string(b))
}
