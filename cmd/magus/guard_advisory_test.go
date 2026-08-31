package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// hookGate stands up a gate over a temporary cache base, so a test never marks the
// checkout's real advisory state.
func hookGate(t *testing.T, session string) advisoryGate {
	t.Helper()
	return newAdvisoryGate(t.TempDir(), session)
}

// TestAdvisoryGateHoldsAnEnrolledKindToOneFiring is the whole rule in one assertion pair:
// the same fact, twice in one session, said once.
func TestAdvisoryGateHoldsAnEnrolledKindToOneFiring(t *testing.T) {
	g := hookGate(t, "session-1")
	assert.Equal(t, "the notice", g.once(advisoryCodeSearch, "the notice"))
	assert.Empty(t, g.once(advisoryCodeSearch, "the notice"), "the second firing of one kind is the one nobody reads")

	// Per KIND, not per session: silencing one advisory must not silence the rest.
	assert.Equal(t, "other", g.once(advisoryStageClassify, "other"))
}

// TestAdvisoryGateSpeaksAgainInAFreshSession pins the other half. A rule held forever is
// a rule the next session never learns.
func TestAdvisoryGateSpeaksAgainInAFreshSession(t *testing.T) {
	base := t.TempDir()
	assert.Equal(t, "n", newAdvisoryGate(base, "session-1").once(advisoryCodeSearch, "n"))
	assert.Empty(t, newAdvisoryGate(base, "session-1").once(advisoryCodeSearch, "n"),
		"a hook is a short-lived process, so the marker on disk is the only thing that carries the session")
	assert.Equal(t, "n", newAdvisoryGate(base, "session-2").once(advisoryCodeSearch, "n"))
}

// TestAdvisoryGateExpiresTheAnonymousMarker covers the surface with no session identity.
// A host that reports none leaves nothing to tell this run from the next, so that marker
// expires on a clock instead - otherwise the first session on such a host would silence
// every session after it, permanently.
func TestAdvisoryGateExpiresTheAnonymousMarker(t *testing.T) {
	base := t.TempDir()
	g := newAdvisoryGate(base, "")
	require.Equal(t, "n", g.once(advisoryCodeSearch, "n"))
	require.Empty(t, g.once(advisoryCodeSearch, "n"))

	marker := g.markerPath(advisoryCodeSearch)
	aged := time.Now().Add(-advisoryAnonWindow - time.Minute)
	require.NoError(t, os.Chtimes(marker, aged, aged))
	assert.Equal(t, "n", g.once(advisoryCodeSearch, "n"), "a marker past its window is a session that ended")
	assert.Empty(t, g.once(advisoryCodeSearch, "n"), "and the firing that reported it starts the window again")
}

// TestAdvisoryGateSpeaksWhenItCannotRemember pins the fail-open direction. State magus
// cannot write is not a reason to go quiet: a notice repeated is a smaller failure than a
// notice nobody ever gets.
func TestAdvisoryGateSpeaksWhenItCannotRemember(t *testing.T) {
	g := newAdvisoryGate("", "session-1")
	assert.Equal(t, "n", g.once(advisoryCodeSearch, "n"))
	assert.Equal(t, "n", g.once(advisoryCodeSearch, "n"))
}

// TestAdvisoryGateSpendsNothingOnSilence: a rule with nothing to say has not used up the
// one time it may speak. Without this, an advisory whose condition was not met on the
// first write would be suppressed on the write where it finally applied.
func TestAdvisoryGateSpendsNothingOnSilence(t *testing.T) {
	g := hookGate(t, "session-1")
	require.Empty(t, g.once(advisoryCodeSearch, ""))
	assert.Equal(t, "n", g.once(advisoryCodeSearch, "n"))
}

// TestAdvisoryGateLeavesUnenrolledKindsAlone. An empty kind MEANS "speaks every time";
// reading it as a key would give every such advisory one shared marker and silence all of
// them the moment any one of them fired.
func TestAdvisoryGateLeavesUnenrolledKindsAlone(t *testing.T) {
	g := hookGate(t, "session-1")
	assert.Equal(t, "n", g.once("", "n"))
	assert.Equal(t, "n", g.once("", "n"))
}

// TestHookCmdAdvisesOncePerSession is the same rule through the command the host actually
// runs. The graph-beats-grep hint is the measured case: it fired dozens of times in one
// session with byte-identical text.
func TestHookCmdAdvisesOncePerSession(t *testing.T) {
	base, root := t.TempDir(), t.TempDir()
	ctx := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{base: base, workspace: root})
	run := func(session string) string {
		var out strings.Builder
		// The display flags live on a package global, so one case must not leak into the
		// next; TestHookCmd resets the same way and for the same reason.
		global = globalFlags{}
		require.NoError(t, hookCmd(ctx, strings.NewReader("rg needle"), &out, []string{"--session", session}))
		return out.String()
	}

	first := run("session-1")
	require.True(t, strings.HasPrefix(first, "advise: "))
	assert.Contains(t, first, "knowledge graph")

	assert.Equal(t, "pass\n", run("session-1"),
		"the repeat is a pass, not an advise carrying empty context: a host renders what it is handed")
	assert.Contains(t, run("session-2"), "knowledge graph", "a fresh session is owed the fact once")
}

// TestHookCmdScopesSearchAdviceFromManifest pins the wiring from the knowledge
// manifest to the advisory text: a search pointed at a project directory gets a
// project=-scoped query suggestion, and a workspace with no manifest gets the
// unscoped advice it always got.
func TestHookCmdScopesSearchAdviceFromManifest(t *testing.T) {
	run := func(t *testing.T, base, command string) string {
		t.Helper()
		ctx := context.WithValue(t.Context(), hookActivityLocationKey{},
			hookActivityLocation{base: base, workspace: t.TempDir()})
		var out strings.Builder
		global = globalFlags{}
		require.NoError(t, hookCmd(ctx, strings.NewReader(command), &out, []string{"--session", "session-1"}))
		return out.String()
	}

	t.Run("manifest projects scope the suggestion", func(t *testing.T) {
		base := t.TempDir()
		man := fmt.Sprintf(`{"schema_version":%d,"shards":{"docs":{},".":{},"@runtime":{},"docs@symbols":{}}}`,
			types.KnowledgeSchemaVersion)
		require.NoError(t, os.MkdirAll(filepath.Join(base, "knowledge"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(base, "knowledge", "manifest.json"), []byte(man), 0o644))

		got := run(t, base, "grep -rn Foo docs/")
		require.True(t, strings.HasPrefix(got, "advise: "))
		assert.Contains(t, got, `magus query Foo 'project=~^docs(/|$)'`)
	})

	t.Run("no manifest stays unscoped", func(t *testing.T) {
		got := run(t, t.TempDir(), "grep -rn Foo docs/")
		require.True(t, strings.HasPrefix(got, "advise: "))
		assert.Contains(t, got, "magus query Foo")
		assert.NotContains(t, got, "project=docs")
	})
}

// TestHookCmdRepeatsEveryDenial is the exemption, and it is the more important half. A
// refusal explains itself every time it refuses - it is the one verdict the caller cannot
// see past, and a second identical `git stash` blocked with no reason is a dead end.
func TestHookCmdRepeatsEveryDenial(t *testing.T) {
	base, root := t.TempDir(), t.TempDir()
	ctx := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{base: base, workspace: root})
	run := func() string {
		var out strings.Builder
		global = globalFlags{}
		err := hookCmd(ctx, strings.NewReader("git stash"), &out, []string{"--session", "session-1"})
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.exitCode)
		return out.String()
	}
	assert.Equal(t, run(), run(), "a denial repeats its reason verbatim, however many times it fires")
}

// TestHookCmdRoutesAnAgentSurfaceWrite pins the WIRING, not the rule: a rule that is
// correct and never called is the failure mode this repository has shipped before. It also
// pins the dedupe on the path surface, where a suppressed advisory must leave silence
// rather than let the next rung speak into it.
func TestHookCmdRoutesAnAgentSurfaceWrite(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cmd", "magus"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755))

	ctx := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{base: t.TempDir(), workspace: root})
	run := func() string {
		var out strings.Builder
		global = globalFlags{}
		require.NoError(t, hookCmd(ctx, strings.NewReader("internal/agent/skills/magus-run/SKILL.md"),
			&out, []string{"--path", "--session", "session-1"}))
		return out.String()
	}

	assert.Contains(t, run(), "magus-skill-authoring")
	assert.Equal(t, "pass\n", run(),
		"the repeat is silence, not the new-directory advisory stepping into the gap this rule left")
}
