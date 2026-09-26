package yaml

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestPositions(t *testing.T) {
	got, err := Positions(t.Context(), "jobs:\n  build:\n    steps:\n      - uses: a@v1\n        with: {cache: false}\n\"a/b~c\": 1\nref: *x\n")
	require.Error(t, err, "an undefined alias is invalid YAML")
	assert.Empty(t, got.Lines)

	got, err = Positions(t.Context(), "jobs:\n  build:\n    steps:\n      - uses: a@v1\n        with: {cache: false}\n\"a/b~c\": 1\n")
	require.NoError(t, err)
	assert.Equal(t, types.YAMLPositions{
		Lines: map[string]int{
			"": 1, "/jobs": 2, "/jobs/build": 3, "/jobs/build/steps": 4, "/jobs/build/steps/0": 4,
			"/jobs/build/steps/0/uses": 4, "/jobs/build/steps/0/with": 5, "/jobs/build/steps/0/with/cache": 5,
			"/a~1b~0c": 6,
		},
		Columns: map[string]int{
			"": 1, "/jobs": 3, "/jobs/build": 5, "/jobs/build/steps": 7, "/jobs/build/steps/0": 9,
			"/jobs/build/steps/0/uses": 15, "/jobs/build/steps/0/with": 15, "/jobs/build/steps/0/with/cache": 23,
			"/a~1b~0c": 10,
		},
	}, got)

	got, err = Positions(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, types.YAMLPositions{Lines: map[string]int{"": 0}, Columns: map[string]int{"": 0}}, got,
		"an empty document has only its root")
}
