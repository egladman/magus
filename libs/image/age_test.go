package image

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSetCreated(t *testing.T) {
	t.Parallel()

	var unknown Layout
	setCreated(&unknown, nil)
	assert.Empty(t, unknown.Created)

	created := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	var known Layout
	setCreated(&known, &created)
	assert.Equal(t, "2026-10-20T12:00:00Z", known.Created)
}
