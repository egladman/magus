package spellruntime_test

import (
	"testing"

	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The end-to-end check the enum was adopted for: a built-in spell writes
// `VersionKey{upTo = VersionComponent.patch}` in Buzz, and it must arrive in Go as
// VersionPatch. An enum case is a heap object rather than a string, so before the
// adapter unwrapped it this decoded as absent - silently, which is exactly the failure
// a bare string invited and the enum was meant to end.
func TestBuiltinSpellsDecodeVersionKeyFromEnum(t *testing.T) {
	reg := spellruntime.Builtins()

	goSpell, ok := reg["go"]
	require.True(t, ok, "go spell not registered")
	assert.Equal(t, spells.VersionPatch, goSpell.VersionKey.UpTo,
		"`go version` prints the host platform; patch extraction is what sheds it")
	assert.Equal(t, spells.VersionPatch, goSpell.VersionKeys["golangci-lint"].UpTo,
		"golangci-lint pads its version line with a commit and a build timestamp")

	// govulncheck declares nothing on purpose: its verdict comes from the vulnerability
	// database, whose date is in the probe output and would not survive extraction.
	assert.True(t, goSpell.VersionKeys["govulncheck"].IsZero(),
		"govulncheck must key on its whole output")

	for _, name := range []string{"docker", "rust"} {
		s, ok := reg[name]
		require.True(t, ok, "%s spell not registered", name)
		assert.Equal(t, spells.VersionPatch, s.VersionKey.UpTo, "%s", name)
	}

	// A spell that declares nothing keeps the whole-output default.
	if bash, ok := reg["bash"]; ok {
		assert.True(t, bash.VersionKey.IsZero())
	}
}
