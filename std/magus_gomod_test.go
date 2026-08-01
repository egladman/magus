package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveGoModReplaceArgs(t *testing.T) {
	mods := []goMod{
		{
			Dir:  "/repo",
			Path: "example.com/root",
			Require: []goModRequire{
				{Path: "example.com/diag"},
				{Path: "example.com/buzz"},
				{Path: "example.com/external"},
			},
			Replace: []goModReplace{
				{OldPath: "example.com/diag", NewPath: "./wrong"},
				{OldPath: "example.com/stale", NewPath: "./libs/stale"},
				{OldPath: "example.com/external", NewPath: "../fork/external"},
			},
		},
		{Dir: "/repo/libs/diag", Path: "example.com/diag"},
		{Dir: "/repo/libs/buzz", Path: "example.com/buzz"},
		{Dir: "/repo/libs/stale", Path: "example.com/stale"},
	}

	got, err := deriveGoModReplaceArgs("/repo", mods)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"-dropreplace=example.com/diag",
		"-dropreplace=example.com/stale",
		"-replace=example.com/buzz=./libs/buzz",
		"-replace=example.com/diag=./libs/diag",
	}, got)
}

func TestDeriveGoModReplaceArgsAcceptsCorrectParentPath(t *testing.T) {
	mods := []goMod{
		{Dir: "/repo/libs/buzz", Path: "example.com/buzz", Require: []goModRequire{{Path: "example.com/diag"}}, Replace: []goModReplace{{OldPath: "example.com/diag", NewPath: "../diag"}}},
		{Dir: "/repo/libs/diag", Path: "example.com/diag"},
	}

	got, err := deriveGoModReplaceArgs("/repo/libs/buzz", mods)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestDeriveGoModReplaceArgsUsesParentPath(t *testing.T) {
	mods := []goMod{
		{Dir: "/repo/libs/buzz", Path: "example.com/buzz", Require: []goModRequire{{Path: "example.com/diag"}}},
		{Dir: "/repo/libs/diag", Path: "example.com/diag"},
	}

	got, err := deriveGoModReplaceArgs("/repo/libs/buzz", mods)
	require.NoError(t, err)
	assert.Equal(t, []string{"-replace=example.com/diag=../diag"}, got)
}
