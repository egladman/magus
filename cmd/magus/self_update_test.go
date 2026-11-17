//go:build !noselfupdate

package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSelfUpdate_ConfirmYes proves --yes skips the prompt entirely: no output, no read.
func TestSelfUpdate_ConfirmYes(t *testing.T) {
	var out bytes.Buffer
	err := confirmInstall(&out, strings.NewReader(""), true, true, "v0.4.0", "example.com", "/usr/local/bin/magus")
	require.NoError(t, err)
	assert.Empty(t, out.String(), "no prompt should be printed when --yes is set")
}

// TestSelfUpdate_ConfirmNonInteractiveWithoutYes proves a non-terminal stdin without
// --yes is refused with the real flag names, not the generated struct field names
// (--uf.Yes / --uf.Force do not exist as flags; --yes and -y do).
func TestSelfUpdate_ConfirmNonInteractiveWithoutYes(t *testing.T) {
	var out bytes.Buffer
	err := confirmInstall(&out, strings.NewReader(""), false, false, "v0.4.0", "example.com", "/usr/local/bin/magus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--"+flaggen.FlagSelfUpdateYes)
	assert.Contains(t, err.Error(), "-"+flaggen.FlagSelfUpdateY)
	assert.NotContains(t, err.Error(), "uf.")
}

// TestSelfUpdate_ConfirmPromptNamesHostBeforeAsking pins the prompt's exact shape:
// it names the version, the host the release index came from, and the target path,
// all before the "[y/N]" question: a declined or unreachable-index install used to
// leave the host unstated, so MAGUS_UPDATE_URL silently swapping the source had no
// visible trace in the one place a human reads before approving it.
func TestSelfUpdate_ConfirmPromptNamesHostBeforeAsking(t *testing.T) {
	var out bytes.Buffer
	err := confirmInstall(&out, strings.NewReader("y\n"), true, false, "v0.4.0", "eli.gladman.cc", "/usr/local/bin/magus")
	require.NoError(t, err)

	prompt := out.String()
	assert.Contains(t, prompt, "v0.4.0")
	assert.Contains(t, prompt, "/usr/local/bin/magus")
	hostIdx := strings.Index(prompt, "eli.gladman.cc")
	askIdx := strings.Index(prompt, "[y/N]")
	require.NotEqual(t, -1, hostIdx, "prompt must name the host")
	require.NotEqual(t, -1, askIdx, "prompt must ask [y/N]")
	assert.Less(t, hostIdx, askIdx, "the host must be named before the prompt asks")
}

// TestSelfUpdate_ConfirmDeclinedExitsNonzero proves a declined prompt is a non-nil
// error, not the old "print aborted, return nil" behavior. selfUpdateCommand forwards
// this error straight to its caller, and any non-nil, non-usage error maps to a
// nonzero process exit (mapExitCode in main.go), so a script driving `magus self
// update` sees failure whenever nothing was installed.
func TestSelfUpdate_ConfirmDeclinedExitsNonzero(t *testing.T) {
	for _, answer := range []string{"n\n", "no\n", "\n", ""} {
		t.Run(fmt.Sprintf("answer=%q", answer), func(t *testing.T) {
			var out bytes.Buffer
			err := confirmInstall(&out, strings.NewReader(answer), true, false, "v0.4.0", "example.com", "/usr/local/bin/magus")
			require.Error(t, err, "a non-y answer must be treated as declined, not silently accepted")
			assert.Contains(t, err.Error(), "v0.4.0")
		})
	}
}

// TestSelfNounSurvivesNoSelfUpdate is the property narrowing the tag bought: a
// subcommand with nothing to do with updating must stay reachable in a build that
// compiled the updater out, and the usage must not offer `update` where it would
// only refuse. A distro-packaged magus, where the package manager owns the binary,
// still installs the mgs shorthand.
//
// Properties rather than the exact string: this list grows, and pinning it would
// fail on every addition without catching anything.
func TestSelfNounSurvivesNoSelfUpdate(t *testing.T) {
	got := selfSubcommands()
	for _, want := range []string{"install-shorthand"} {
		if !strings.Contains(got, want) {
			t.Errorf("selfSubcommands() = %q, missing %q; it does not depend on the updater", got, want)
		}
	}
	if strings.Contains(got, "update") != selfUpdateCompiled {
		t.Errorf("selfSubcommands() = %q, but selfUpdateCompiled = %v", got, selfUpdateCompiled)
	}
}
