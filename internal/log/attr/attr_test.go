package attr

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A handler matches these by key and value kind, so both are part of the contract.
func TestAttrKeysAndKinds(t *testing.T) {
	cases := []struct {
		attr slog.Attr
		key  string
		kind slog.Kind
	}{
		{Component("knowledge"), ComponentKey, slog.KindString},
		{Why("stale symbols mislead"), WhyKey, slog.KindString},
		{Elapsed(90 * time.Second), ElapsedKey, slog.KindDuration},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.key, tc.attr.Key)
		assert.Equal(t, tc.kind, tc.attr.Value.Kind(), tc.key)
	}
	assert.Equal(t, 90*time.Second, Elapsed(90*time.Second).Value.Duration())
}
