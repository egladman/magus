package buzzengine

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestImportingLinksTheEngine(t *testing.T) {
	assert.True(t, interp.Available())
}
