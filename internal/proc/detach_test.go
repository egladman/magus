package proc

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit"
)

// TestDetachedChildEnvDropsInheritedInvocationState pins that A BACKGROUND PROCESS
// DESCENDS FROM NOBODY. A run is what starts the broker, so without the scrub its
// environment permanently records that one run's ancestry, and a server started from
// inside a run would read those refs as its own, excusing claims from an invocation that
// ended hours ago and judging an unstamped run to be a nested magus that lost its
// ancestry. The same rule submitJob already applies to a job's context.
func TestDetachedChildEnvDropsInheritedInvocationState(t *testing.T) {
	t.Setenv("MAGUS_PROC_SOCKET", "unix:///tmp/parent.sock")
	t.Setenv("MAGUS_PROC_TOKEN", "parent-secret")
	t.Setenv("MAGUS_INVOCATION_ANCESTORS", "3217:inv-parent")
	t.Setenv("MAGUS_LEVEL", "1")
	t.Setenv("MAGUS_KEEP_ME", "yes")

	got := map[string]string{}
	for _, kv := range detachedChildEnv() {
		if name, value, ok := strings.Cut(kv, "="); ok {
			got[name] = value
		}
	}
	assert.NotContains(t, got, "MAGUS_PROC_SOCKET", "a child inheriting it binds no socket of its own")
	assert.NotContains(t, got, "MAGUS_PROC_TOKEN", "the parent socket's secret leaves with its address")
	assert.NotContains(t, got, "MAGUS_INVOCATION_ANCESTORS", "a background process is nobody's descendant")
	assert.NotContains(t, got, "MAGUS_LEVEL", "nor is it nested inside the run that happened to start it")
	assert.Equal(t, "yes", got["MAGUS_KEEP_ME"], "everything else is inherited as before")
}

// TestDetachedCmdReExecsTheGivenPathWithoutResolvingIt pins the fix for the v0.4.1 windows
// release, where auto-starting a background process failed with
//
//	exec: "C:\hostedtoolcache\windows\magus\bin\magus": executable file not found in %PATH%
//
// exec.Command resolves an absolute path on Windows through lookExtensions, which needs a
// PATHEXT sibling; setup-magus installs an extensionless `magus`, so the running binary
// could not re-exec itself. Nothing here should consult PATH: the caller passes
// os.Executable().
//
// The assertion is contract-shaped rather than reproducing the failure, which needs a
// Windows runtime. exec.Command sets Err on exactly this input there and nowhere else, so
// the case is stated on every platform and enforced where it bites.
func TestDetachedCmdReExecsTheGivenPathWithoutResolvingIt(t *testing.T) {
	for _, tc := range []struct{ name, exe string }{
		{"extensionless, as setup-magus installs it", filepath.Join(t.TempDir(), "magus")},
		{"with the windows extension", filepath.Join(t.TempDir(), "magus.exe")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := DetachedCommand(tc.exe, []string{"broker"})

			require.NoError(t, cmd.Err, "re-execing a known absolute path performs no lookup that could fail")
			assert.Equal(t, tc.exe, cmd.Path, "the path is used verbatim, never a PATHEXT sibling")
			assert.Equal(t, []string{tc.exe, "broker"}, cmd.Args,
				"argv[0] is the executable, with the caller's arguments after it")
		})
	}
}

// TestDetachedCmdIgnoresPATH proves the resolution is absent rather than merely succeeding:
// a name that also exists on PATH must not be picked up in place of the path given.
func TestDetachedCmdIgnoresPATH(t *testing.T) {
	decoyWS := testkit.NewWorkspace(t)
	decoyWS.WriteExecutable("magus", "#!/bin/sh\n")
	decoy := decoyWS.Root()
	t.Setenv("PATH", decoy)

	want := filepath.Join(t.TempDir(), "magus")
	cmd := DetachedCommand(want, nil)

	assert.Equal(t, want, cmd.Path, "the decoy on PATH is not consulted")
	assert.NotEqual(t, filepath.Join(decoy, "magus"), cmd.Path)
}

// TestDetachedChildEnvDropsTheInheritedSocket pins the scrub. A child that inherits
// MAGUS_PROC_SOCKET decides it is already adopted, binds no socket of its own, and then
// reports the parent's, leaving a server `server stop` cannot find.
func TestDetachedChildEnvDropsTheInheritedSocket(t *testing.T) {
	t.Setenv("MAGUS_PROC_SOCKET", "/tmp/magus-parent.sock")
	t.Setenv("MAGUS_KEEP_ME", "1")

	env := detachedChildEnv()

	for _, kv := range env {
		assert.False(t, strings.HasPrefix(kv, "MAGUS_PROC_SOCKET="), "child inherited %q", kv)
	}
	assert.Contains(t, env, "MAGUS_KEEP_ME=1", "the scrub must drop one variable, not the environment")
}

// TestWithoutDetachFlagStripsEverySpelling pins the loop guard on --detach. The detached
// child runs the rest of the argv, so a --detach left in it would detach again, forever.
func TestWithoutDetachFlagStripsEverySpelling(t *testing.T) {
	got := WithoutDetachFlag([]string{"ci", "-detach", "docs", "--detach", "--detach=true", "--detach-me"})
	assert.Equal(t, []string{"ci", "docs", "--detach-me"}, got,
		"every spelling the flag package accepts is dropped; a different flag that merely starts the same is not")

	assert.Equal(t, []string{"ci", "."}, WithoutDetachFlag([]string{"ci", "."}),
		"an argv without the flag is unchanged")

	assert.Equal(t, []string{"ci", "--", "--detach"}, WithoutDetachFlag([]string{"ci", "--", "--detach"}),
		"a --detach past the -- separator belongs to the forwarded tool and must survive verbatim")
}
