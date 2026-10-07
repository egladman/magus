package vcs

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolFloorIsTheReleaseThatAddedTheFlag(t *testing.T) {
	assert.Equal(t, "2.54", toolFloors["git"].min)
	assert.Equal(t, "0.22", toolFloors["jj"].min)
	assert.Equal(t, "4.5", toolFloors["hg"].min)
	assert.Equal(t, "0.2.20230523", toolFloors["sl"].min)
	for bin, floor := range toolFloors {
		assert.True(t, spells.ValidBound(floor.min), bin)
		assert.NotEmpty(t, floor.flag, bin)
		assert.NotEmpty(t, floor.probe, bin)
	}
}

func TestVersionErrorRejectsTheReleaseBeforeTheFlag(t *testing.T) {
	cases := []struct {
		bin, output string
		old         bool
	}{
		{"git", "git version 2.39.5 (Apple Git-155)", true},
		{"git", "git version 2.53.0", true},
		{"git", "git version 2.54.0", false},
		{"git", "git version 2.55.0", false},
		{"jj", "jj 0.21.0", true},
		{"jj", "jj 0.22.0", false},
		{"jj", "jj 0.45.1", false},
		{"hg", "Mercurial Distributed SCM (version 4.4.2)", true},
		{"hg", "Mercurial Distributed SCM (version 4.5)", false},
		{"hg", "Mercurial Distributed SCM (version 7.2.4)\nCopyright (C) 2005-2026", false},
		{"sl", "Sapling 0.2.20230426-092610", true},
		{"sl", "Sapling 0.2.20230426-145232", true},
		{"sl", "Sapling 0.2.20230523", false},
		{"sl", "Sapling 0.2.20230523-092610", false},
		{"sl", "Sapling 0.2.20260811-150444", false},
		{"git", "not a version", false},
	}
	for _, tc := range cases {
		err := versionError(tc.bin, tc.output)
		if !tc.old {
			require.NoError(t, err, "%s %q", tc.bin, tc.output)
			continue
		}
		require.ErrorIs(t, err, types.ToolTooOld, "%s %q", tc.bin, tc.output)
		assert.ErrorContains(t, err, toolFloors[tc.bin].flag)
		assert.ErrorContains(t, err, toolFloors[tc.bin].min)
	}
}

func TestNoteToolVersionKeepsAPriorError(t *testing.T) {
	prior := errors.New("checkout tampered")
	cmd := exec.Command("git")
	cmd.Err = prior
	noteToolVersion(t.Context(), cmd, "git")
	assert.ErrorIs(t, cmd.Err, prior)
}

// A too-old verdict is not remembered: the binary on PATH can be upgraded
// while a server runs, and the next command has to see the new one.
func TestCachedToolVersionForgetsATooOldVerdict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "git")
	write := func(version string) {
		require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\necho 'git version "+version+"'\n"), 0o755))
	}
	t.Setenv("PATH", dir)
	toolProbes.Delete("git")
	t.Cleanup(func() { toolProbes.Delete("git") })

	write("2.53.0")
	require.ErrorIs(t, cachedToolVersion(t.Context(), "git"), types.ToolTooOld)
	write("2.54.0")
	require.NoError(t, cachedToolVersion(t.Context(), "git"))
	write("2.53.0")
	require.NoError(t, cachedToolVersion(t.Context(), "git"), "a passing verdict is remembered")
}

var installedVCSFloor = flag.Bool("installed-vcs-floor", false, "fail when an installed git, hg, sl or jj is older than its floor")

// This checks the machine, not the code, so it runs only when asked, by passing
// -installed-vcs-floor with -run TestInstalledVCSMeetsItsFloor.
func TestInstalledVCSMeetsItsFloor(t *testing.T) {
	if !*installedVCSFloor {
		t.Skip("pass -installed-vcs-floor to check the installed binaries")
	}
	for _, bin := range []string{"git", "hg", "sl", "jj"} {
		t.Run(bin, func(t *testing.T) {
			if _, err := exec.LookPath(bin); err != nil {
				t.Skip(bin + " not installed")
			}
			cmd := exec.Command(bin)
			noteToolVersion(t.Context(), cmd, bin)
			require.NoError(t, cmd.Err)
		})
	}
}
