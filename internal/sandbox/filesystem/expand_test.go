package filesystem

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandUserRule_AbsPath(t *testing.T) {
	r, err := ExpandUserRule("/tmp/mydir", true, false)
	require.NoError(t, err)
	// user-allow paths default to Exec=true.
	assert.Equal(t, Rule{Path: "/tmp/mydir", Read: true, Write: false, Exec: true}, r)
}

func TestExpandUserRule_EnvVar(t *testing.T) {
	t.Setenv("TEST_EXPAND_PATH", "/var/test")
	r, err := ExpandUserRule("$TEST_EXPAND_PATH", true, false)
	require.NoError(t, err)
	assert.Equal(t, "/var/test", r.Path)
}
