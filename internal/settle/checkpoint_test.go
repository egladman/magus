package settle

import (
	"testing"

	typesvcs "github.com/egladman/magus/types/vcs"
	"github.com/stretchr/testify/assert"
)

// TestCheckpointLineCarriesThePreservedHandle covers the promise --preserve makes in its
// own usage text. The default rendering used to be byte-identical with and without the
// flag while one of the two spellings minted an object in the user's repository, so the
// handle that reaches it appeared nowhere a caller was told to look.
func TestCheckpointLineCarriesThePreservedHandle(t *testing.T) {
	cp := typesvcs.Checkpoint{Revision: "abc123", Branch: "main", Dirty: true, PatchDigest: "d1d1"}

	assert.Equal(t, "abc123 main dirty d1d1", CheckpointLine(cp),
		"a checkpoint that preserved nothing gained a field")

	cp.Preserved = "e5e5e5e5"
	assert.Equal(t, "abc123 main dirty d1d1 preserved e5e5e5e5", CheckpointLine(cp))
}
