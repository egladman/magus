package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDialect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want Dialect
	}{
		{"", DialectBash},
		{"bash", DialectBash},
		{"POSIX", DialectPosix},
		{"zsh", DialectZsh},
	} {
		got, err := ParseDialect(tc.in)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
	_, err := ParseDialect("fish")
	require.Error(t, err)
}

func TestParseCommandsDialectRejectsBashOnlyConstructUnderPOSIX(t *testing.T) {
	t.Parallel()
	_, ok := ParseCommandsDialect("declare -A foo=()", DialectPosix)
	assert.False(t, ok)
	_, ok = ParseCommandsDialect("declare -A foo=()", DialectBash)
	assert.True(t, ok)
}

func TestParseCommandsDialectPeelsShellWrappers(t *testing.T) {
	t.Parallel()
	cmds, ok := ParseCommandsDialect(`dash -c 'echo hi'`, DialectBash)
	require.True(t, ok)
	require.Len(t, cmds, 1)
	assert.Equal(t, "echo", cmds[0].Name)

	cmds, ok = ParseCommandsDialect(`zsh -c 'print hi'`, DialectBash)
	require.True(t, ok)
	require.Len(t, cmds, 1)
	assert.Equal(t, "print", cmds[0].Name)
}

func TestShellDialectFromRules(t *testing.T) {
	t.Parallel()
	assert.Empty(t, shellDialectFromRules(nil))
	assert.Equal(t, Dialect("zsh"), shellDialectFromRules([]WorkspaceShellRule{
		{Dialect: "bash"},
		{Dialect: "zsh"},
	}))
}
