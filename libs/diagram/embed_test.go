package diagram

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagramSourceEmbedsBothFiles(t *testing.T) {
	for _, name := range []string{"diagram.buzz", "flow.buzz"} {
		b, err := Source.ReadFile(name)
		require.NoError(t, err, name)
		assert.NotEmpty(t, b, name)
	}
}
