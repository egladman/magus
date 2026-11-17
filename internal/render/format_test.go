package render

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/clikit/output"
)

func TestWriteFormattedJSON(t *testing.T) {
	var b bytes.Buffer
	require.NoError(t, WriteFormatted(&b, output.Options{Format: output.JSON}, struct {
		X int `json:"x"`
	}{X: 7}))
	assert.Contains(t, b.String(), `"x": 7`)
}

func TestTemplateFuncsExcludesTheNonHermetic(t *testing.T) {
	funcs := TemplateFuncs()
	for _, name := range excludedSprigFuncs {
		assert.NotContains(t, funcs, name)
	}
	assert.Contains(t, funcs, "join")
}

func TestTemplateJoinTakesAnySlice(t *testing.T) {
	assert.Equal(t, "", templateJoin(nil, ","))
	assert.Equal(t, "a,b", templateJoin([]string{"a", "b"}, ","))
	assert.Equal(t, "1,2", templateJoin([]any{1, 2}, ","))
	assert.Equal(t, "x", templateJoin("x", ","))
}
