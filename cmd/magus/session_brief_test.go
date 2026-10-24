package main

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A brief lands in a model's context window through a hook. Two properties make it
// usable there and neither is visible by reading the code: no escape sequences (a
// hook's stdout is never a terminal, so a control byte is cost with no reader), and
// a bounded line (a lease goal or a validation command is as long as its author
// made it).
func assertBriefIsContextSafe(t *testing.T, text string) {
	t.Helper()
	assert.NotContains(t, text, "\x1b", "the brief carries an escape sequence; a hook's reader is a model, not a terminal")
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		assert.LessOrEqual(t, len(line), briefTextWidth,
			"a brief line runs past briefTextWidth, so a long goal or path list is spending the window this lands in:\n%s", line)
	}
}

func TestSessionBriefSurfacesOneRecurringFeedbackReview(t *testing.T) {
	brief := sessionBrief{Feedback: []trail.GuardFeedback{
		{Rule: "raw-tool", Denied: 3, Sessions: 1, FollowedSessions: 1},
		{Rule: "output-pipe", Denied: 2, Sessions: 2},
	}}
	text := brief.Text()
	assert.Contains(t, text, "improvement review: raw-tool denied 3 times")
	assert.Contains(t, text, "recurring-guard-denials")
	assert.NotContains(t, text, "output-pipe", "rehydration gets one bounded review, not a table")
	assertBriefIsContextSafe(t, text)
}

func TestSessionBriefTextCarriesEverySection(t *testing.T) {
	t.Parallel()

	b := sessionBrief{
		Workspace: "/checkout",
		Branch:    "w-compact-hook",
		Revision:  "a41984c9c",
		Unpushed:  &briefUnpushed{Base: "origin/main", Count: 3},
		Tree: briefTree{
			Reported: true, Dirty: 3, Classified: true,
			Sources:   []string{"cmd/magus/session_brief.go"},
			Outputs:   []string{"MAGUS.md"},
			Unclaimed: []string{"scratch.txt"},
		},
		Leases: []briefLease{{
			ID:    "f2-guard",
			State: string(types.StateRunning),
			Exec:  hint.JobExec.With("f2-guard"),
			// Longer than a line on purpose: the clip is part of the contract.
			Criteria:   strings.Repeat("hold the boundary ", 20),
			Validation: "magus run test internal/ledger",
		}},
		Failures: []briefFailure{{
			Target: "test", Project: "internal/ledger",
			Ref: "abc123", Inspect: "magus query output abc123", At: "2026-09-10 12:03:04",
		}},
		GuardWiring: []string{".claude/settings.json"},
		Rules:       []string{"AGENTS.md", "harnesses/example.json"},
		PromptCache: briefClock(),
		Attention: &briefAttention{
			Open:    307,
			Oldest:  &briefAttentionRow{ID: "att-old", Source: "guard/stall", OpenedMs: time.Now().Add(-9 * 24 * time.Hour).UnixMilli()},
			SinceMs: time.Now().Add(-3 * time.Hour).UnixMilli(),
			Since: []briefAttentionRow{
				{ID: "att-new", Source: "magus/job-release", Outcome: "waiting", OpenedMs: time.Now().Add(-time.Hour).UnixMilli()},
			},
			SinceMore: 4,
		},
	}

	text := b.Text()
	for _, want := range []string{
		"branch: w-compact-hook  revision: a41984c9c",
		"unpushed: 3 commit(s) not on origin/main",
		"tree: 3 changed file(s): 1 source, 1 output, 1 unclaimed",
		"leases live here:",
		"exec: " + hint.JobExec.With("f2-guard"),
		"validation: magus run test internal/ledger",
		"the last recorded run failed:",
		"magus query output abc123",
		"guard wiring: .claude/settings.json",
		"rules live in AGENTS.md, harnesses/example.json",
		"prompt cache: last tool call here 7m ago",
		"closed: Anthropic default",
		"open: Anthropic 1h opt-in",
		"attention: 307 open",
		"oldest: att-old guard/stall, raised 9 days ago",
		"att-new magus/job-release (waiting)",
		"and 4 more",
	} {
		assert.Contains(t, text, want, "the brief dropped a section a rehydrating session reads")
	}
	assertBriefIsContextSafe(t, text)
	// The paths themselves are one `git status` away; listing them here would spend
	// the context window on what the model can read for itself.
	assert.NotContains(t, text, "cmd/magus/session_brief.go")
}

// The three absences a reader must not misread: a tree nobody could ask about is not
// a clean one, a dirty tree nothing classified is not a tree nothing claims, and a
// checkout with no hook config is not a guarded one.
func TestSessionBriefNamesWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	unread := sessionBrief{Workspace: "/checkout"}.Text()
	assert.Contains(t, unread, "tree: no VCS answered here")
	assert.Contains(t, unread, "guard wiring: none in this checkout")
	assertBriefIsContextSafe(t, unread)

	unclassified := sessionBrief{Workspace: "/checkout", Tree: briefTree{Reported: true, Dirty: 4}}.Text()
	assert.Contains(t, unclassified, "tree: 4 changed file(s), unclassified")
	assertBriefIsContextSafe(t, unclassified)
}

// A queue the store would not yield is not an empty one, and a checkout with one host
// session has no earlier one to measure "since" from.
func TestSessionBriefAttentionNamesWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	unread := sessionBrief{Attention: &briefAttention{Unread: "permission denied"}}.Text()
	assert.Contains(t, unread, "attention: the queue could not be read (permission denied)")
	assert.NotContains(t, unread, "none open")
	assertBriefIsContextSafe(t, unread)

	noEarlier := sessionBrief{Attention: &briefAttention{Open: 2, Oldest: &briefAttentionRow{ID: "att-a", OpenedMs: time.Now().UnixMilli()}}}.Text()
	assert.Contains(t, noEarlier, "no earlier session recorded in this checkout")
	assert.Contains(t, sessionBrief{Attention: &briefAttention{}}.Text(), "attention: none open")
	assert.NotContains(t, sessionBrief{}.Text(), "attention:", "an ungathered brief says nothing about the queue")
}

// The rows a brief marks as new are those raised after the previous host session last
// acted in this checkout, read out of the store `session ls` counts from.
func TestSessionBriefAttentionMarksRowsSinceThePreviousSession(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := t.TempDir()
	elsewhere := filepath.Join(root, "elsewhere")
	dir, err := sessions.Dir(root)
	require.NoError(t, err)

	raise := func(source, message string) {
		t.Helper()
		_, opened, err := sessions.OpenRequest(dir, "host-1", sessions.AttentionOpen{Outcome: "waiting", Source: source, Message: message},
			sessions.InvocationStart{Workspace: elsewhere})
		require.NoError(t, err)
		require.True(t, opened)
	}
	runIn := func(session, inv string) {
		t.Helper()
		start := sessions.InvocationStart{Workspace: root, Command: "run test"}
		start.Session = session
		h := sessions.NewFactHandler(root, start)
		require.NotNil(t, h)
		emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: inv, Target: "test", Project: ".", Status: journal.StatusPass})
	}
	pause := func() { time.Sleep(3 * time.Millisecond) }

	raise("guard/stall", "before the previous session")
	pause()
	runIn("earlier", "invEarlier")
	pause()
	runIn("current", "invCurrent")
	pause()
	raise("magus/job-release", "after it")

	got := briefAttentionQueue(root)

	require.Empty(t, got.Unread)
	assert.Equal(t, 2, got.Open)
	require.NotNil(t, got.Oldest)
	assert.Equal(t, "guard/stall", got.Oldest.Source)
	assert.NotZero(t, got.SinceMs)
	require.Len(t, got.Since, 1, "only the row raised after the previous session's last activity is new")
	assert.Equal(t, "magus/job-release", got.Since[0].Source)
}

// The gather, against a checkout on disk. Every section it fills here is read out of
// a file this test wrote, which is the property the whole command rests on: a brief
// that could be produced without the checkout would be a summary again.
func TestSessionBriefReadsTheCheckout(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	w := testkit.NewWorkspace(t)
	root := w.Root()
	// The guard-wiring section runs the stand-in written below, which answers at
	// once; the shipped probe deadline would measure only how loaded the machine is.
	ctx := agent.ContextWithProbeTimeout(context.Background(), time.Hour)

	// The root magusfile wires "brief-host" with magus\harness.provider, and the fixture
	// loader answers for it the way the spell's harness contract functions would.
	w.Magusfile("import \"magus\";\nimport \"spells/brief-host\" as host;\nmagus\\harness.provider(host);\n")
	w.Write("spells/brief-host/spell.buzz", "import \"magus/spell\";\n"+
		"export fun mgs_getName() > str { return \"brief-host\"; }\n"+
		"export fun mgs_getHarnessConfig() > HarnessConfig { return HarnessConfig{path = \"host/hooks.json\"}; }\n")
	w.Write("AGENTS.md", "# rules\n")
	fixture.WireHarness(t, agent.HarnessDescriptor{
		SchemaVersion: 2,
		ID:            "brief-host",
		Display:       agent.HarnessDisplay{Name: "Brief Host"},
		Config:        types.HarnessConfig{Path: "host/hooks.json"},
		Skills:        types.HarnessSkills{Paths: []string{".agents/skills"}, Form: types.SkillFormBoth},
		ManagedEntries: []types.ManagedEntries{{
			Path: []string{"hooks", "before"},
			Entries: []map[string]any{{
				"match":    "run",
				"commands": []any{map[string]any{"type": "command", "command": "./magus buzz -s magus-command.buzz"}},
			}},
		}},
	})
	ws, err := magus.Inspect(ctx, root, magus.WithSpawnRule(magus.TierCeiling))
	require.NoError(t, err)
	w.Write("host/hooks.json",
		`{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"./magus buzz -s magus-command.buzz"}]}]}}`)
	// VerifyHarness actually runs the wired command (internal/agent's
	// harness_probe.go) instead of trusting its mere presence in the config, so
	// the brief's guard-wiring section needs something real behind it: a stand-in
	// for magus running the command glue, answering as a working guard would.
	w.WriteExecutable("magus", "#!/bin/sh\ncat >/dev/null\nprintf 'deny'\n")

	store, err := openJobs(t.Context(), root, jobOpen{})
	require.NoError(t, err)
	guardRow := types.Job{
		ID:           "f2-guard",
		State:        types.StateRunning,
		Criteria:     "hold the boundary\nsecond line nobody reads here",
		Validation:   "magus run test internal/ledger",
		WritePaths:   []string{"internal/ledger"},
		CheckoutRoot: root,
	}
	_, err = store.Update(ctx, guardRow.ID, func(cur *types.Job) { *cur = guardRow })
	require.NoError(t, err)

	// One session, one failing target: the run history the brief reads back.
	handlers := withInvocationJournal(ctx, nil, root, "run", []string{"test"})
	require.Len(t, handlers, 1)
	emitJournalEvent(t, handlers[0], journal.Event{
		Kind: journal.KindResult, Inv: "invBrief", Target: "test",
		Project: "internal/ledger", Status: journal.StatusFail, Ref: "ref-1",
	})

	brief := gatherSessionBrief(ctx, root, ws)

	assert.Contains(t, brief.Rules, "AGENTS.md")
	require.Len(t, brief.Failures, 1)
	require.NotNil(t, brief.Attention, "an empty queue is reported as none open, not left out")
	assert.Equal(t, &briefAttention{}, brief.Attention)
	assert.Equal(t, sessionBrief{
		Workspace: root,
		// The checkout's own VCS, console, trail and transcript state is not what this test sets up.
		Branch:      brief.Branch,
		Revision:    brief.Revision,
		Unpushed:    brief.Unpushed,
		Tree:        brief.Tree,
		Attention:   brief.Attention,
		Console:     brief.Console,
		PromptCache: brief.PromptCache,
		Feedback:    brief.Feedback,
		Recent:      brief.Recent,
		Rules:       brief.Rules, // asserted by name above
		// A lease's goal reads as one line here; the rest is `magus describe job`.
		Leases: []briefLease{{
			ID:         "f2-guard",
			State:      string(types.StateRunning),
			Exec:       hint.JobExec.With("f2-guard"),
			Criteria:   "hold the boundary",
			Validation: "magus run test internal/ledger",
		}},
		Failures: []briefFailure{{
			Target:  "test",
			Project: "internal/ledger",
			Ref:     "ref-1",
			Inspect: "magus query output ref-1",
			At:      brief.Failures[0].At,
		}},
		GuardWiring: []string{filepath.Join("host", "hooks.json")},
	}, brief)

	text := brief.Text()
	assert.Contains(t, text, "magus query output ref-1")
	assert.Contains(t, text, hint.JobExec.With("f2-guard"))
	assertBriefIsContextSafe(t, text)
}

// A refusal ran nothing, so it has no output ref; the code is the whole of what the brief
// can say about it. Another worktree's later failure must not stand in for this checkout's.
func TestSessionBriefFailuresAreThisCheckoutsAndCarryTheRefusalCode(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	refused := sessions.NewFactHandler(root, sessions.InvocationStart{Workspace: root, Command: "affected ci"})
	require.NotNil(t, refused)
	emitJournalEvent(t, refused, journal.Event{
		Kind: journal.KindResult, Inv: "invRefused", Target: "ci", Project: "pkg",
		Status: journal.StatusFail, DiagnosticCode: string(types.MachineBudgetExhausted),
	})
	time.Sleep(2 * time.Millisecond) // the other checkout's run is the newer one
	other := sessions.NewFactHandler(root, sessions.InvocationStart{Workspace: filepath.Join(root, "elsewhere"), Command: "run test"})
	require.NotNil(t, other)
	emitJournalEvent(t, other, journal.Event{
		Kind: journal.KindResult, Inv: "invOther", Target: "test", Project: "other", Status: journal.StatusFail, Ref: "ref-other",
	})

	failures := lastRunFailures(root)
	require.Len(t, failures, 1)
	require.NotEmpty(t, failures[0].At)
	assert.Equal(t, briefFailure{Target: "ci", Project: "pkg", DiagnosticCode: "MGS3009", At: failures[0].At}, failures[0],
		"a refusal carries its code and no ref to inspect")

	var text strings.Builder
	sessionBrief{Failures: failures}.writeFailures(&text)
	assert.Contains(t, text.String(), "ci pkg [MGS3009] (")
}

// A terminal lease has stopped competing for its paths, so a rehydrating session
// must not be told to bind it.
func TestSessionBriefSkipsLeasesThatAreDone(t *testing.T) {
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	ctx := context.Background()

	store, err := openJobs(t.Context(), root, jobOpen{})
	require.NoError(t, err)
	landed := types.Job{ID: "landed", State: types.StatePass, CheckoutRoot: root}
	_, err = store.Update(ctx, landed.ID, func(cur *types.Job) { *cur = landed })
	require.NoError(t, err)
	running := types.Job{ID: "running", State: types.StateRunning, CheckoutRoot: root}
	_, err = store.Update(ctx, running.ID, func(cur *types.Job) { *cur = running })
	require.NoError(t, err)

	brief := gatherSessionBrief(ctx, root, nil)
	require.Len(t, brief.Leases, 1)
	assert.Equal(t, "running", brief.Leases[0].ID)
	assert.Zero(t, brief.OtherLeases)
}

func TestSessionBriefListsEachProvedClaim(t *testing.T) {
	t.Parallel()

	claims := claimLines([]types.JobClaim{{
		Path: "internal/guard/render.go", Evidence: types.ClaimEvidence{Kind: types.ClaimRefs, Subject: "hint.Render"},
		Proof: "internal/guard/render.go references hint.Render, which the job's diff changes in internal/hint/render.go",
	}})
	assert.Equal(t, []string{"internal/guard/render.go (refs:hint.Render): internal/guard/render.go references hint.Render, which the job's diff changes in internal/hint/render.go"}, claims)
	assert.Nil(t, claimLines(nil), "a job with no claims lists none")

	b := sessionBrief{Leases: []briefLease{{ID: "render-change", State: string(types.StateRunning), Exec: hint.JobExec.With("render-change"), Claims: claims}}}
	assert.Contains(t, b.Text(), "    claimed: internal/guard/render.go (refs:hint.Render): internal/guard/render.go references hint.Render")
}

// The job store is the repository's, shared by every worktree. A rehydrating session can
// act only under the leases taken in its checkout or bound to it, so those are named and
// every other live lease is one count line with the command that lists them.
func TestSessionBriefNamesOnlyTheLeasesThatBindThisCheckout(t *testing.T) {
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	ctx := context.Background()

	store, err := openJobs(t.Context(), root, jobOpen{})
	require.NoError(t, err)
	// Existing checkouts: a running row whose checkout is gone is ended on read.
	for _, row := range []types.Job{
		{ID: "here", State: types.StateRunning, CheckoutRoot: root},
		{ID: "bound", State: types.StateDeclared},
		{ID: "elsewhere-1", State: types.StateRunning, CheckoutRoot: t.TempDir()},
		{ID: "elsewhere-2", State: types.StateExited, CheckoutRoot: t.TempDir()},
		{ID: "declared-unbound", State: types.StateDeclared},
		{ID: "done-here", State: types.StatePass, CheckoutRoot: root},
	} {
		_, err = store.Update(ctx, row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	// Bound after the rows are written: the store refuses a bound session's write to a
	// row it did not create.
	t.Setenv(trail.EnvBaggage, "magus.lease=bound")

	brief := gatherSessionBrief(ctx, root, nil)
	ids := make([]string, len(brief.Leases))
	for i, l := range brief.Leases {
		ids[i] = l.ID
	}
	assert.ElementsMatch(t, []string{"here", "bound"}, ids)
	assert.Equal(t, 3, brief.OtherLeases)
	text := brief.Text()
	assert.Contains(t, text, "\n3 other leases: `"+hint.LsJobs.String()+"`\n")
	assert.NotContains(t, text, "elsewhere")
	assertBriefIsContextSafe(t, text)

	// With nothing bound here, the leases section is that one line.
	var only strings.Builder
	sessionBrief{OtherLeases: 3}.writeLeases(&only)
	assert.Equal(t, "3 other leases: `"+hint.LsJobs.String()+"`\n", only.String())
}

// A checkout every job was once exec'd in holds many exited rows that are still "live"
// for the write guard, but the brief must not dump every one into a compacted context
// window.
func TestSessionBriefCapsLiveLeases(t *testing.T) {
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	ctx := context.Background()

	store, err := openJobs(t.Context(), root, jobOpen{})
	require.NoError(t, err)
	for i := 0; i < briefMaxLeases+5; i++ {
		row := types.Job{ID: "exited-" + strconv.Itoa(i), State: types.StateExited, CheckoutRoot: root}
		_, err = store.Update(ctx, row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	running := types.Job{ID: "running", State: types.StateRunning, CheckoutRoot: root}
	_, err = store.Update(ctx, running.ID, func(cur *types.Job) { *cur = running })
	require.NoError(t, err)

	brief := gatherSessionBrief(ctx, root, nil)
	require.Len(t, brief.Leases, briefMaxLeases)
	assert.Equal(t, "running", brief.Leases[0].ID, "editing jobs are named before exited holders")
	assert.Equal(t, 6, brief.OtherLeases)
	text := brief.Text()
	assert.Contains(t, text, "6 other leases: `"+hint.LsJobs.String()+"`")
	assertBriefIsContextSafe(t, text)
}

// briefClock is a session idle long enough that one window is behind it and the rest
// are not, which is the only state where the two-group split says something.
func briefClock() sessions.PromptCacheClock {
	last := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	return sessions.PromptCacheAt("s1", last, last.Add(7*time.Minute))
}

// Finished branches off the base are counted against the fork thresholds, and an unread
// count says why rather than reading as none waiting.
func TestSessionBriefCountsFinishedBranchesOffTheBase(t *testing.T) {
	t.Parallel()

	var below strings.Builder
	sessionBrief{Unintegrated: &briefUnintegrated{Count: 2, Branches: []string{"done-a", "done-b"}, AdviseAt: 5, RefuseAt: 10}}.writeUnintegrated(&below)
	assert.Equal(t, "finished branches off the base: 2 (a fork advises at 5, refuses at 10): done-a, done-b\n", below.String())

	var at strings.Builder
	sessionBrief{Unintegrated: &briefUnintegrated{Count: 1, Branches: []string{"done-a"}, AdviseAt: 1, RefuseAt: 2}}.writeUnintegrated(&at)
	assert.Contains(t, at.String(), "integrate them: `"+hint.JobFork.With("--stdin", "--checkout", "<dir>")+"`")

	var unknown strings.Builder
	sessionBrief{Unintegrated: &briefUnintegrated{Unknown: "version control is disabled here"}}.writeUnintegrated(&unknown)
	assert.Equal(t, "finished branches off the base: unknown, version control is disabled here\n", unknown.String())

	var none strings.Builder
	sessionBrief{}.writeUnintegrated(&none)
	assert.Empty(t, none.String())
}

// A compacted root session has lost which jobs it forked and where each stands.
func TestSessionBriefListsTheJobTreeForkedFromThisCheckout(t *testing.T) {
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	ctx := context.Background()
	work := t.TempDir()

	store, err := openJobs(t.Context(), root, jobOpen{})
	require.NoError(t, err)
	for _, row := range []types.Job{
		{ID: "plan", State: types.StateRunning, Worktree: &types.JobWorktree{Root: work, Source: root}},
		{ID: "plan/exited", Parent: "plan", State: types.StateExited, Result: &types.JobResult{UnresolvedRisks: []string{"merge is the root's", "second"}}},
		{ID: "plan/waiting", Parent: "plan", State: types.StateDeclared, DependsOn: []string{"plan/exited"}},
		{ID: "plan/failed", Parent: "plan", State: types.StateFail},
		{ID: "landed", State: types.StatePass, Worktree: &types.JobWorktree{Root: t.TempDir(), Source: root, Removed: 1}},
		{ID: "elsewhere", State: types.StateRunning, Worktree: &types.JobWorktree{Root: t.TempDir(), Source: t.TempDir()}},
	} {
		_, err = store.Update(ctx, row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
		if row.Worktree != nil {
			_, err = store.RecordWorktree(ctx, row.ID, *row.Worktree)
			require.NoError(t, err)
		}
	}

	brief := gatherSessionBrief(ctx, root, nil)
	require.NotNil(t, brief.Jobs)
	assert.Empty(t, brief.Jobs.Root)
	ids := make([]string, len(brief.Jobs.Jobs))
	for i, j := range brief.Jobs.Jobs {
		ids[i] = j.ID
	}
	assert.Equal(t, []string{"plan", "plan/exited", "plan/waiting", "plan/failed"}, ids,
		"a finished tree and another checkout's are not this session's")
	assert.Zero(t, brief.OtherLeases, "the tree replaces the other-leases count")
	assert.Equal(t, []string{"plan/exited"}, brief.Jobs.Uncollected)
	assert.Equal(t, briefJob{ID: "plan", State: "running", Checkout: work}, brief.Jobs.Jobs[0])
	assert.Equal(t, briefJob{
		ID: "plan/waiting", State: "declared", Depth: 1,
		DependsOn: []briefDependency{{ID: "plan/exited", State: "exited"}},
		Next:      "waits on plan/exited (exited)",
	}, brief.Jobs.Jobs[2])

	// A CI gate's TMPDIR puts work past briefTextWidth, where briefLine clips it.
	var planLine strings.Builder
	briefLine(&planLine, "  plan (running) in %s", work)
	text := brief.Text()
	for _, want := range []string{
		"jobs forked from this checkout:",
		planLine.String(),
		"    plan/exited (exited) no checkout yet",
		"      risk: merge is the root's",
		"      next: collect it: " + hint.JobWait.With("plan/exited"),
		"      depends on: plan/exited (exited, unsatisfied)",
		"      next: read why: " + hint.DescribeJob.With("plan/failed"),
		"exited and never collected, so each ends as no_return unless waited on:\n  " + hint.JobWait.With("plan/exited") + "\n",
	} {
		assert.Contains(t, text, want)
	}
	assert.NotContains(t, text, "other leases")
	assertBriefIsContextSafe(t, text)
}

// A worker session's tree is what its own job forked.
func TestSessionBriefListsTheForksOfTheJobActedUnder(t *testing.T) {
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	ctx := context.Background()

	store, err := openJobs(t.Context(), root, jobOpen{})
	require.NoError(t, err)
	for _, row := range []types.Job{
		{ID: "worker", State: types.StateRunning},
		{ID: "worker/a", Parent: "worker", State: types.StateDeclared},
		{ID: "worker/a/b", Parent: "worker/a", State: types.StatePass},
		{ID: "sibling", State: types.StateRunning},
	} {
		_, err = store.Update(ctx, row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	t.Setenv(trail.EnvBaggage, "magus.lease=worker")

	brief := gatherSessionBrief(ctx, root, nil)
	assert.Equal(t, &briefJobTree{Root: "worker", Jobs: []briefJob{
		{ID: "worker/a", State: "declared", Next: "take it: " + hint.JobExec.With("worker/a")},
		{ID: "worker/a/b", State: "pass", Depth: 1},
	}}, brief.Jobs)
	assert.Contains(t, brief.Text(), "jobs forked under worker:\n  worker/a (declared) no checkout yet\n")
}

// An unread store must not read as a session with no jobs.
func TestSessionBriefJobTreeCapsAndNamesWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	unread := sessionBrief{Jobs: &briefJobTree{Unread: "permission denied"}}.Text()
	assert.Contains(t, unread, "jobs: the store could not be read (permission denied); `"+hint.LsJobs.String()+"`")
	assert.NotContains(t, sessionBrief{}.Text(), "jobs", "a session that owns no jobs prints no tree")

	unpushed := &briefUnpushed{Base: "origin/main", Count: 2}
	assert.Equal(t, "gate it in its checkout ("+hint.Affected.With("ci")+"), then push",
		briefNextStep(nil, types.Job{ID: "done", State: types.StatePass}, unpushed))
	assert.Empty(t, briefNextStep(nil, types.Job{ID: "done", State: types.StatePass}, &briefUnpushed{Base: "origin/main"}))
	assert.Equal(t, "waits on gone, which no job declares",
		briefNextStep(nil, types.Job{ID: "w", State: types.StateDeclared, DependsOn: []string{"gone"}}, nil))

	b := sessionBrief{Workspace: "/checkout", Jobs: &briefJobTree{
		Jobs: []briefJob{{ID: "done", State: "pass", Checkout: "/checkout", Branch: "done", Head: "abc1234", Unpushed: unpushed}},
		More: 3,
	}}
	text := b.Text()
	assert.Contains(t, text, "  done (pass) on done at abc1234 in this checkout, 2 commit(s) not on origin/main\n")
	assert.Contains(t, text, "  and 3 more: `"+hint.LsJobs.String()+"`\n")
	assertBriefIsContextSafe(t, text)
}

// An exited job nobody waits on ends as no_return, so each one is named with its wait line,
// past the cap too; one a wait already rejected has been collected.
func TestSessionBriefNamesEveryExitedJobNobodyCollected(t *testing.T) {
	t.Parallel()

	rows := []types.Job{{ID: "root", State: types.StateRunning}}
	for i := range briefMaxJobs + 2 {
		rows = append(rows, types.Job{ID: "root/" + strconv.Itoa(i), Parent: "root", State: types.StateExited})
	}
	rows = append(rows, types.Job{ID: "root/rejected", Parent: "root", State: types.StateExited, Rejection: &types.JobRejection{Violations: []string{"check failed"}}})

	tree := sessionJobTree(t.Context(), rows, "root", t.TempDir(), types.VCSOptions{})
	require.NotNil(t, tree)
	assert.Len(t, tree.Jobs, briefMaxJobs)
	assert.Equal(t, 3, tree.More)
	require.Len(t, tree.Uncollected, briefMaxJobs+2)
	assert.NotContains(t, tree.Uncollected, "root/rejected")

	text := sessionBrief{Jobs: tree}.Text()
	assert.Contains(t, text, "exited and never collected, so each ends as no_return unless waited on:\n")
	assert.Contains(t, text, "\n  "+hint.JobWait.With("root/13")+"\n", "a job past the cap is still named")
	assertBriefIsContextSafe(t, text)
}
