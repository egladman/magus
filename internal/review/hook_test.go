package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrePushHookIsPOSIXShAndAlwaysExitsZero(t *testing.T) {
	t.Parallel()

	hook := PrePushHook
	lines := strings.Split(strings.TrimRight(hook, "\n"), "\n")

	assert.Equal(t, "#!/bin/sh", lines[0])
	assert.Equal(t, "exit 0", lines[len(lines)-1], "the last statement is an unconditional success")
	assert.Contains(t, hook, `magus diff --unread --rev "$base...$lsha" </dev/null >&2`)
	assert.NotContains(t, hook, "set -e", "a failing command must not end the hook early")
	for _, banned := range []string{"[[", "local ", "function ", "<<<"} {
		assert.NotContains(t, hook, banned, "bashisms do not run under sh")
	}
}

func TestPrePushHookRunsUnreadOnTheRangeBeingPushed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "pre-push")
	require.NoError(t, os.WriteFile(script, []byte(PrePushHook), 0o755))
	// A stand-in magus that records its arguments and fails, so the test also proves a
	// failing report never changes the hook's exit status.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus"), []byte("#!/bin/sh\necho \"$@\" >> "+log+"\nexit 7\n"), 0o755))

	const (
		zeros = "0000000000000000000000000000000000000000"
		old   = "1111111111111111111111111111111111111111"
		head  = "2222222222222222222222222222222222222222"
	)
	stdin := strings.Join([]string{
		"refs/heads/topic " + head + " refs/heads/topic " + old,
		"(delete) " + zeros + " refs/heads/gone " + old,
	}, "\n") + "\n"

	cmd := exec.Command("sh", script)
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()

	require.NoError(t, err, string(out))
	calls, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, "diff --unread --rev "+old+"..."+head+"\n", string(calls), "one call, for the push; the deletion is skipped")
}

func TestPrePushHookIsSilentWithoutMagus(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := filepath.Join(dir, "pre-push")
	require.NoError(t, os.WriteFile(script, []byte(PrePushHook), 0o755))

	cmd := exec.Command("/bin/sh", script)
	cmd.Env = []string{"PATH=" + dir}
	cmd.Stdin = strings.NewReader("refs/heads/topic 2222 refs/heads/topic 1111\n")
	out, err := cmd.CombinedOutput()

	require.NoError(t, err)
	assert.Empty(t, string(out))
}
