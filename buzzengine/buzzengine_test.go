package buzzengine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/interp"
)

func TestImportingLinksTheEngine(t *testing.T) {
	assert.True(t, interp.Available())
}
