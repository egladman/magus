package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHookWiringSubjectNamesEveryDocumentedHost walks the paths the four guide pages
// name, in the three spellings a host actually sends: workspace-relative, absolute inside
// the checkout, and absolute in the user's home directory, which is where three of the
// four hosts keep theirs.
func TestHookWiringSubjectNamesEveryDocumentedHost(t *testing.T) {
	for _, path := range []string{
		".claude/settings.json",
		".claude/settings.local.json",
		".claude/hooks/magus-guard-command.sh",
		"/repo/.claude/settings.json",
		"/Users/dev/.claude/hooks/magus-guard-path.sh",
		".cursor/hooks.json",
		"/repo/.cursor/hooks/cursor-guard.sh",
		".codex/hooks.json",
		"/Users/dev/.codex/hooks.json",
		".opencode/plugins/magus-guard.ts",
		"/Users/dev/.config/opencode/plugins/magus-guard.ts",
	} {
		assert.NotEmpty(t, hookWiringSubject(path), "%q is host guard wiring", path)
	}
}

// TestHookWiringSubjectIsSilentEverywhereElse pins the false positives. A rule that fires
// on a neighbouring file teaches a reader to skip it, and the deny arm below would then be
// blocking ordinary work under every lease.
func TestHookWiringSubjectIsSilentEverywhereElse(t *testing.T) {
	for _, path := range []string{
		"",
		".claude/skills/magus-run/SKILL.md",
		".claude/worktrees/adj-guard/cmd/magus/guard.go",
		"cmd/magus/guard_wiring.go",
		"docs/guides/integrations/agents/claude-code.md",
		"settings.json",
		"internal/hooks.json",
		"console/src/hooks/useThing.ts",
	} {
		assert.Empty(t, hookWiringSubject(path), "%q is not host guard wiring", path)
	}
}

// TestGradeHookWiringWriteDeniesUnderALease is D4: the write a declared boundary cannot
// make legitimate, because what it edits is whether the boundary is checked at all.
func TestGradeHookWiringWriteDeniesUnderALease(t *testing.T) {
	g := gradeHookWiringWrite("harness/wiring", ".claude/settings.json")

	require.Equal(t, "deny", g.Decision)
	assert.Contains(t, g.Reason, "harness/wiring", "the denial must name the lease it refused")
	assert.Contains(t, g.Reason, ".claude/settings.json", "the denial must name the file")
	assert.Contains(t, g.Reason, "hook wiring", "the denial must say what the file IS")
	assert.Contains(t, g.Reason, "Your orchestrator can", "the denial must name the actor")
	assert.NotContains(t, g.Reason, "magus_ledger",
		"a worker told to reach for the ledger tool reaches for it; that is the mistake this rewrite answers")
}

// TestGradeHookWiringWriteAdvisesUnboundSessions covers the other half of the asymmetry:
// an orchestrator and a person in their own checkout both legitimately rewire a host, and
// what they are owed is the sentence naming the file.
func TestGradeHookWiringWriteAdvisesUnboundSessions(t *testing.T) {
	g := gradeHookWiringWrite("", ".cursor/hooks.json")

	require.Equal(t, "advise", g.Decision)
	assert.Equal(t, advisoryHookWiring, g.Kind, "a standing fact is said once per session")
	assert.Contains(t, g.Context, ".cursor/hooks.json")
	assert.Contains(t, g.Context, "next session start", "the advisory must say when the edit takes effect")

	assert.Empty(t, gradeHookWiringWrite("harness/wiring", "cmd/magus/guard.go").Decision,
		"every other path is somebody else's rule")
}

// TestHookCmdDeniesAWiringWriteUnderALease proves the WIRING, the way the gate rule's own
// test does: a rule nothing calls never fires however well it is tested. It also pins the
// rank, since the lease ledger speaks first on this surface and a lane that happens to
// contain the file must not clear it.
func TestHookCmdDeniesAWiringWriteUnderALease(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	lease := narrowLease()
	lease.WritePaths = []string{".claude/**", "cmd/magus/**"}
	ctx, _ := fleetFixture(t, lease)

	var denied bytes.Buffer
	err := hookCmd(ctx, strings.NewReader(".claude/settings.json"), &denied,
		[]string{"--path", "--lease", lease.ID, "-o", "name"})
	require.Error(t, err, "a deny that exits 0 blocks nothing")
	assert.Equal(t, "deny\n", denied.String())

	var unbound bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(".claude/settings.json"), &unbound,
		[]string{"--path", "-o", "name"}))
	assert.Equal(t, "advise\n", unbound.String(), "an unbound session rewires its own hosts")
}
