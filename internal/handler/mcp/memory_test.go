package mcp

import (
	"testing"

	"github.com/egladman/magus/internal/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMemoryRefs(t *testing.T) {
	// One ref per line, split on the FIRST colon so a target with its own colons or
	// commas (a query expression, a namespaced node ID) survives intact.
	refs, err := parseMemoryRefs("query: kind:op depends cache\nnode: file:internal/hash/hasher.go\n\noutput: out1a2b3c")
	require.NoError(t, err)
	assert.Equal(t, []memory.Ref{
		{Kind: "query", Target: "kind:op depends cache"},
		{Kind: "node", Target: "file:internal/hash/hasher.go"},
		{Kind: "output", Target: "out1a2b3c"},
	}, refs)

	empty, err := parseMemoryRefs("   \n\n")
	require.NoError(t, err)
	assert.Empty(t, empty, "blank lines yield no refs")

	_, err = parseMemoryRefs("this line has no colon")
	assert.Error(t, err, "a ref without a kind: prefix is rejected")
}

func TestSplitCommaList(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, splitCommaList(" a, b ,c"))
	assert.Empty(t, splitCommaList("  ,  , "))
	assert.Empty(t, splitCommaList(""))
}
