package mcp

import (
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var nextFixture = []hint.Next{
	{ID: "query-explain", Run: "magus explain spell:go", Argv: []string{"magus", "explain", "spell:go"}},
}

// The splice is additive and structural: one key on an object, nothing at all on a
// payload with no key to add, and never a second `next` beside one the payload
// already declares.
func TestMergeNextSplicesOneKey(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"a record":        {`{"query":"kind=spell"}`, `{"query":"kind=spell","next":[{"id":"query-explain","run":"magus explain spell:go","argv":["magus","explain","spell:go"]}]}`},
		"an empty object": {`{}`, `{"next":[{"id":"query-explain","run":"magus explain spell:go","argv":["magus","explain","spell:go"]}]}`},
		"a list":          {`[1,2]`, `[1,2]`},
		"a bare string":   {`"text"`, `"text"`},
		"nothing at all":  {``, ``},
		"a payload that already carries next": {`{"next":[{"id":"mine"}]}`, `{"next":[{"id":"mine"}]}`},
	} {
		assert.Equal(t, tc.want, string(mergeNext([]byte(tc.raw), nextFixture)), name)
	}

	assert.Equal(t, `{"a":1}`, string(mergeNext([]byte(`{"a":1}`), nil)), "nothing to suggest splices nothing")
}

// A payload that will not marshal is returned as itself, so the tool's own failure is
// reported where it always was rather than as a missing field.
func TestDataWithNextLeavesAnUnmarshalablePayloadAlone(t *testing.T) {
	broken := map[string]any{"fn": func() {}}
	assert.Equal(t, any(broken), dataWithNext(broken, nextFixture))

	raw, ok := dataWithNext(map[string]string{"a": "b"}, nextFixture).(json.RawMessage)
	require.True(t, ok, "a record-shaped payload comes back pre-encoded")
	assert.Contains(t, string(raw), `"next":[`)
}

// A zero filter serves everything and records nothing, which is what a tool built
// without a workspace behind it should do.
func TestNextFilterWithoutAWorkspaceServesEverything(t *testing.T) {
	assert.Equal(t, nextFixture, nextFilter{}.served(nextFixture))
}
