package sandbox

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

var errDeclined = errors.New("declined")

func adoption(floor, have types.SandboxMode) Adoption {
	return Adoption{
		Floor:     floor,
		Workspace: func() (types.SandboxMode, error) { return have, nil },
		Declined:  errDeclined,
	}
}

// TestAdoptionOnlyStrengthens: the forwarded --sandbox bound into the server's globalCfg and
// reached nothing, so a confined client's work ran there under whatever the server's workspace
// said. A client asking for less is refused; a client under a mode this workspace does not meet
// is declined, and runs the work itself.
func TestAdoptionOnlyStrengthens(t *testing.T) {
	off, best := types.SandboxModeOff, types.SandboxModeBestEffort
	req := types.SandboxModeRequired

	require.NoError(t, adoption("", off).Check([]string{"build", "."}), "nothing asked, nothing checked")
	require.NoError(t, adoption("", best).Check([]string{"build", "."}))
	require.NoError(t, adoption("", off).Check([]string{"build", "--sandbox=off"}), "off and asked to stay off")
	require.NoError(t, adoption(best, best).Check([]string{"build"}), "a sandboxed client, a sandboxing workspace")
	require.NoError(t, adoption(best, best).Check([]string{"build", "--sandbox", "best-effort"}))

	require.ErrorIs(t, adoption(best, off).Check([]string{"build"}), errDeclined)
	require.ErrorIs(t, adoption(req, best).Check([]string{"build"}), errDeclined,
		"a required client is not served by a best-effort workspace")
	require.ErrorIs(t, adoption("", off).Check([]string{"build", "--sandbox=best-effort"}), errDeclined)
	require.ErrorIs(t, adoption(best, off).Check([]string{"build", "--", "--sandbox=off"}), errDeclined,
		"a tool's own argument after -- is not the flag")

	require.ErrorIs(t, adoption("", best).Check([]string{"build", "--sandbox=off"}), types.SandboxWeakened)
	require.ErrorIs(t, adoption(req, off).Check([]string{"build", "-sandbox=best-effort"}), types.SandboxWeakened)
}

func TestAdoptionReportsAWorkspaceThatCannotSay(t *testing.T) {
	broken := errors.New("landlock unavailable")
	a := Adoption{Floor: types.SandboxModeRequired, Workspace: func() (types.SandboxMode, error) { return "", broken }}
	require.ErrorIs(t, a.Check([]string{"build"}), broken)
}

func TestFlag(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want types.SandboxMode
		set  bool
	}{
		{nil, "", false},
		{[]string{"build", "."}, "", false},
		{[]string{"--sandbox=required"}, types.SandboxModeRequired, true},
		{[]string{"-sandbox", "off"}, types.SandboxModeOff, true},
		{[]string{"--sandbox=required", "--sandbox=best-effort"}, types.SandboxModeBestEffort, true},
		{[]string{"--", "--sandbox=off"}, "", false},
		{[]string{"--sandbox-extra=off"}, "", false},
		{[]string{"--sandbox=on"}, "", false},
		{[]string{"--sandbox"}, "", false},
	} {
		want, set := Flag(tc.args)
		assert.Equal(t, tc.want, want, "%q", tc.args)
		assert.Equal(t, tc.set, set, "%q", tc.args)
	}
}

// TestRefuseInheritedWeakened: a magus a sandboxed run started may not pass a weaker
// --sandbox to shed its parent's mode, and may pass a stronger one.
func TestRefuseInheritedWeakened(t *testing.T) {
	req, best, off := types.SandboxModeRequired, types.SandboxModeBestEffort, types.SandboxModeOff
	require.ErrorIs(t, RefuseInheritedWeakened(off, true, req), types.SandboxWeakened)
	require.ErrorIs(t, RefuseInheritedWeakened(best, true, req), types.SandboxWeakened)
	require.NoError(t, RefuseInheritedWeakened(req, true, req))
	require.NoError(t, RefuseInheritedWeakened(req, true, best), "a nested run may strengthen")
	require.NoError(t, RefuseInheritedWeakened(off, false, req), "at the top level the person's flag is the authority")
	require.NoError(t, RefuseInheritedWeakened(off, true, ""), "a parent that ran unsandboxed passes nothing down")
}
