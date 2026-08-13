package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeGuardCanaryStub writes root/magus as a stub binary standing in for the
// real one: the canary only cares that `magus hook -o name` prints a decision
// on stdout and exits accordingly, never about actual guard rules.
func writeGuardCanaryStub(t *testing.T, root, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte(body), 0o755))
}

const denyingCanaryStub = "#!/bin/sh\necho deny\nexit 2\n"

// testCanaryBudget is far above the shipped guardCanaryBudget on purpose.
// These subtests exec a real child process, and this repo is developed across
// many concurrent worktrees - a machine busy enough to push a trivial exec
// past the production budget would make this suite flaky for a reason that
// says nothing about the check. The production value stays 5s; only the test
// waits longer.
const testCanaryBudget = 60 * time.Second

func TestCheckGuardWiring(t *testing.T) {
	t.Run("no binary resolves at all -> fail", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("PATH", t.TempDir()) // empty: no magus anywhere
		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		assert.Contains(t, c.Message, "no ./magus and no magus on PATH")
	})

	t.Run("canary does not return a deny -> fail with observed output", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, "#!/bin/sh\nexit 0\n")

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		assert.Contains(t, c.Message, "did not return a deny")
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, "rebuild: magus run build .")
	})

	t.Run("canary passes, no config anywhere -> advice naming the guide", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorAdvice, c.Status)
		assert.Contains(t, c.Message, "no agent-host hook config found")
		assert.Contains(t, strings.Join(c.Details, "\n"), "docs/guides/integrations/agents.md")
	})

	t.Run("canary passes, config mentions magus and hook with no template reference -> ok", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		settingsDir := filepath.Join(root, ".claude")
		require.NoError(t, os.MkdirAll(settingsDir, 0o755))
		settingsPath := filepath.Join(settingsDir, "settings.json")
		require.NoError(t, os.WriteFile(settingsPath,
			[]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"magus hook -o json"}]}]}}`), 0o600))

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorOK, c.Status)
		assert.Contains(t, c.Details, settingsPath)
	})

	t.Run("canary passes, referenced template carries a stale marker -> fail naming path and fix", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		tmplDir := filepath.Join(root, "docs", "guides", "integrations", "agents")
		require.NoError(t, os.MkdirAll(tmplDir, 0o755))
		staleVersion := agent.GuardTemplateVersion - 1
		tmplPath := filepath.Join(tmplDir, "magus-guard-command.sh")
		require.NoError(t, os.WriteFile(tmplPath,
			[]byte(fmt.Sprintf("#!/usr/bin/env sh\n# %s %d\n", agent.GuardTemplateMarker, staleVersion)), 0o644))

		settingsDir := filepath.Join(root, ".claude")
		require.NoError(t, os.MkdirAll(settingsDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(settingsDir, "settings.json"),
			[]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"sh docs/guides/integrations/agents/magus-guard-command.sh"}]}]}}`), 0o600))

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, tmplPath)
		assert.Contains(t, joined, fmt.Sprintf("template version %d", staleVersion))
		assert.Contains(t, joined, "re-download it")
	})

	t.Run("canary passes, referenced template file is missing -> fail naming the missing path", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		settingsDir := filepath.Join(root, ".claude")
		require.NoError(t, os.MkdirAll(settingsDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(settingsDir, "settings.json"),
			[]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"sh docs/guides/integrations/agents/magus-guard-command.sh"}]}]}}`), 0o600))
		// No template file written at all: the config names one that does not exist.

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, "magus-guard-command.sh")
		assert.Contains(t, joined, "does not exist")
	})

	// The case measured on a real machine: an installed plugin predating the
	// marker, still calling a subcommand magus removed, graded healthy because
	// "no marker" was read as "nothing to compare". No marker means older than
	// versioning, which is the copy most likely to be judging nothing at all.
	t.Run("a template carrying no marker at all is a finding, not a pass", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		pluginsDir := filepath.Join(root, ".opencode", "plugins")
		require.NoError(t, os.MkdirAll(pluginsDir, 0o755))
		pluginPath := filepath.Join(pluginsDir, "magus-guard.ts")
		require.NoError(t, os.WriteFile(pluginPath,
			[]byte("// forwards to `magus agent hook`\nBun.spawn([magus, \"agent\", \"hook\", \"--\", command]);\n"), 0o644))

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, pluginPath)
		assert.Contains(t, joined, "predates template versioning")
	})

	t.Run("self-contained template discovered in a plugins directory checks its own marker", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		pluginsDir := filepath.Join(root, ".opencode", "plugins")
		require.NoError(t, os.MkdirAll(pluginsDir, 0o755))
		staleVersion := agent.GuardTemplateVersion - 1
		pluginPath := filepath.Join(pluginsDir, "guard.ts")
		require.NoError(t, os.WriteFile(pluginPath,
			[]byte(fmt.Sprintf("// magus hook\n// %s %d\n", agent.GuardTemplateMarker, staleVersion)), 0o644))

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, pluginPath)
	})
}
