package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// fakeABI answers kernelABI with abi and err for the rest of the test.
func fakeABI(t *testing.T, abi int, err error) {
	t.Helper()
	saved := kernelABI
	kernelABI = func() (int, error) { return abi, err }
	t.Cleanup(func() { kernelABI = saved })
}

// Best-effort takes the kernel layer wherever there is one and runs without it where
// there is not; required refuses below RequiredABI, where truncation is not denied.
func TestKernelConfinesFollowsTheMode(t *testing.T) {
	best := &Policy{Mode: types.SandboxModeBestEffort}
	required := &Policy{Mode: types.SandboxModeRequired, Workspace: "/ws"}
	confines := func(p *Policy) bool {
		t.Helper()
		ok, err := p.KernelConfines()
		require.NoError(t, err)
		return ok
	}

	var off *Policy
	assert.False(t, confines(off), "a nil policy confines nothing")

	fakeABI(t, 0, ErrUnsupported)
	assert.False(t, confines(best))
	_, err := required.KernelConfines()
	require.ErrorIs(t, err, types.SandboxRequired)
	assert.ErrorContains(t, err, "/ws")

	fakeABI(t, 2, nil)
	assert.True(t, confines(best), "best-effort takes an old kernel's partial confinement")
	_, err = required.KernelConfines()
	require.ErrorIs(t, err, types.SandboxRequired)
	assert.ErrorContains(t, err, "ABI is 2")

	fakeABI(t, RequiredABI, nil)
	assert.True(t, confines(required))
}

// A rule the building magus resolved onto its own /proc entries is the child's to
// grant itself; left in the ruleset it would hand the child magus's descriptors.
func TestProcSelfRulesMoveToTheLauncher(t *testing.T) {
	rules := []filesystem.Rule{
		{Path: "/proc/42/fd", Read: true, Write: true},
		{Path: "/proc/42", Read: true},
		{Path: "/proc/420/status", Read: true},
		{Path: "/usr", Read: true, Exec: true},
	}
	self, rest := procSelfRules(rules, 42)
	assert.Equal(t, []filesystem.Rule{
		{Path: "/proc/self/fd", Read: true, Write: true},
		{Path: "/proc/self", Read: true},
	}, self)
	assert.Equal(t, rules[2:], rest, "another pid's entries are not this process's")

	got, err := decodeRules(encodeRules(self))
	require.NoError(t, err)
	assert.Equal(t, self, got)
	empty, err := decodeRules(encodeRules(nil))
	require.NoError(t, err)
	assert.Empty(t, empty)
	_, err = decodeRules("rw:relative")
	assert.Error(t, err)
}
