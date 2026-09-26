package main

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

func TestLockfilesAreSorted(t *testing.T) {
	clean := fstest.MapFS{
		"versions.lock":        {Data: []byte("# tools\na 1\nb 2\n\n# a new block restarts the order\na 3\n")},
		"magus.lock":           {Data: []byte("z\na\n")},
		"proto/buf.lock":       {Data: []byte("z\na\n")},
		"gen/out.lock":         {Data: []byte("z\na\n")},
		"node_modules/x.lock":  {Data: []byte("z\na\n")},
		".magus/state.lock":    {Data: []byte("z\na\n")},
		"docs/not-a-lock.yaml": {Data: []byte("z\na\n")},
	}
	assert.Empty(t, lockfilesAreSorted(clean))

	seeded := fstest.MapFS{"docs/active.urls.lock": {Data: []byte("https://b\nhttps://a\nhttps://a\n")}}
	assert.Equal(t, []finding{
		{path: "docs/active.urls.lock", line: 2, problem: "lockfile entry out of byte order (or duplicated) within its block: https://a follows https://b",
			fix: "Move the entry to its sorted place; a comment line starts a new block."},
		{path: "docs/active.urls.lock", line: 3, problem: "lockfile entry out of byte order (or duplicated) within its block: https://a follows https://a",
			fix: "Move the entry to its sorted place; a comment line starts a new block."},
	}, lockfilesAreSorted(seeded))
}
