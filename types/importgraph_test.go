package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

// Figures and the console read these keys; an empty map stays a map, since absence is
// Indexed's to say.
func TestImportGraphJSONKeys(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(ImportGraph{
		Indexed:   true,
		Packages:  map[string][]string{"cmd/app": {"internal/server"}},
		Languages: map[string]string{"cmd/app": "go", "internal/server": "go"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"indexed": true,
		"packages": {"cmd/app": ["internal/server"]},
		"languages": {"cmd/app": "go", "internal/server": "go"}
	}`, string(b))

	b, err = json.Marshal(ImportGraph{Packages: map[string][]string{}, Languages: map[string]string{}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"indexed": false, "packages": {}, "languages": {}}`, string(b))
}
