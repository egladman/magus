package json

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestJSONStringify(t *testing.T) {
	ctx := context.Background()
	val := map[string]any{"a": 1.0}

	// No indent: compact (single line).
	compact, err := JSONStringify(ctx, val, "")
	require.NoError(t, err)
	assert.NotContains(t, compact, "\n", "no-indent output should be compact")

	// A non-empty indent: pretty, multi-line with that indent.
	tabbed, err := JSONStringify(ctx, val, "\t")
	require.NoError(t, err)
	assert.Contains(t, tabbed, "\n", "indented output should be multi-line")
	assert.Contains(t, tabbed, "\t", "indented output should be tab-indented")
}
