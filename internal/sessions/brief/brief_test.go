package brief

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	_ "github.com/egladman/magus/buzzengine" // Inspect fails without the engine linked
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/codec"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain isolates the environment the workspaces and stores these tests open read.
func TestMain(m *testing.M) { testkit.Main(m) }

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
	brief := Payload{Feedback: []trail.GuardFeedback{
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

	b := Payload{
		Workspace: "/checkout",
		Branch:    "w-compact-hook",
		Revision:  "a41984c9c",
		Unpushed:  &briefUnpushed{Base: "origin/main", Count: 3},
		Tree: briefTree{
			Reported: true, Dirty: 3, Classified: true,
			Sources:   []string{"internal/sessions/brief/brief.go"},
			Outputs:   []string{"MAGUS.md"},
			Unclaimed: []string{"scratch.txt"},
		},
		Leases: []briefLease{{
			ID:    "f2-guard",
			State: string(jobtypes.StateRunning),
			Exec:  hint.JobExec.With("f2-guard"),
			// Longer than a line on purpose: the clip is part of the contract.
			Instructions: strings.Repeat("hold the boundary ", 20),
			Validation:   "magus run test internal/ledger",
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
	assert.NotContains(t, text, "internal/sessions/brief/brief.go")
}

// The three absences a reader must not misread: a tree nobody could ask about is not
// a clean one, a dirty tree nothing classified is not a tree nothing claims, and a
// checkout with no hook config is not a guarded one.
func TestSessionBriefNamesWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	unread := Payload{Workspace: "/checkout"}.Text()
	assert.Contains(t, unread, "tree: no VCS answered here")
	assert.Contains(t, unread, "guard wiring: none in this checkout")
	assertBriefIsContextSafe(t, unread)

	unclassified := Payload{Workspace: "/checkout", Tree: briefTree{Reported: true, Dirty: 4}}.Text()
	assert.Contains(t, unclassified, "tree: 4 changed file(s), unclassified")
	assertBriefIsContextSafe(t, unclassified)
}

// A queue the store would not yield is not an empty one, and a checkout with one host
// session has no earlier one to measure "since" from.
func TestSessionBriefAttentionNamesWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	unread := Payload{Attention: &briefAttention{Unread: "permission denied"}}.Text()
	assert.Contains(t, unread, "attention: the queue could not be read (permission denied)")
	assert.NotContains(t, unread, "none open")
	assertBriefIsContextSafe(t, unread)

	noEarlier := Payload{Attention: &briefAttention{Open: 2, Oldest: &briefAttentionRow{ID: "att-a", OpenedMs: time.Now().UnixMilli()}}}.Text()
	assert.Contains(t, noEarlier, "no earlier session recorded in this checkout")
	assert.Contains(t, Payload{Attention: &briefAttention{}}.Text(), "attention: none open")
	assert.NotContains(t, Payload{}.Text(), "attention:", "an ungathered brief says nothing about the queue")
}

// The rows a brief marks as new are those raised after the previous host session last
// acted in this checkout, read out of the store `session ls` counts from.
func TestSessionBriefAttentionMarksRowsSinceThePreviousSession(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	elsewhere := filepath.Join(root, "elsewhere")
	dir, err := sessions.Dir(root)
	require.NoError(t, err)

	raise := func(source, message string) {
		t.Helper()
		_, opened, err := sessions.OpenRequest(t.Context(), dir, "host-1", sessions.AttentionOpen{Outcome: "waiting", Source: source, Message: message},
			session.InvocationStart{Workspace: elsewhere})
		require.NoError(t, err)
		require.True(t, opened)
	}
	runIn := func(hostSession, inv string) {
		t.Helper()
		start := session.InvocationStart{Workspace: root, Command: "run test"}
		start.Session = hostSession
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

	got := briefAttentionQueue(t.Context(), testReaders(t, root), root)

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
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	root := w.Root()
	// The guard-wiring section runs the stand-in written below, which answers at
	// once; the shipped probe deadline would measure only how loaded the machine is.
	ctx := agent.ContextWithProbeTimeout(context.Background(), time.Hour)

	wireBriefHost(t, w)
	ws, err := magus.Inspect(ctx, root, magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err)
	w.Write("host/hooks.json",
		`{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"./magus buzz -s magus-command.buzz"}]}]}}`)
	// VerifyHarness actually runs the wired command (internal/agent's
	// harness_probe.go) instead of trusting its mere presence in the config, so
	// the brief's guard-wiring section needs something real behind it: a stand-in
	// for magus running the command glue, answering as a working guard would.
	w.WriteExecutable("magus", "#!/bin/sh\ncat >/dev/null\nprintf 'deny'\n")

	seedBriefLease(t, root)
	// One session, one failing target: the run history the brief reads back.
	seedFailingRun(t, root, "ref-1")

	brief := Gather(ctx, root, ws, testReaders(t, root))

	assert.Contains(t, brief.Rules, "AGENTS.md")
	require.Len(t, brief.Failures, 1)
	require.NotNil(t, brief.Attention, "an empty queue is reported as none open, not left out")
	assert.Equal(t, &briefAttention{}, brief.Attention)
	assert.Equal(t, Payload{
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
			ID:           "f2-guard",
			State:        string(jobtypes.StateRunning),
			Exec:         hint.JobExec.With("f2-guard"),
			Instructions: "hold the boundary",
			Validation:   "magus run test internal/ledger",
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

// wireBriefHost wires the "brief-host" harness into w: the root magusfile names it with
// magus\harness.provider, and the fixture loader answers for it the way the spell's
// harness contract functions would.
func wireBriefHost(t *testing.T, w *testkit.Workspace) {
	t.Helper()
	w.Magusfile("import \"magus\";\nimport \"spells/brief-host\" as host;\nmagus\\harness.provider(host);\n")
	w.Write("spells/brief-host/spell.buzz", "import \"magus/spell\";\n"+
		"export fun mgs_getName() > str { return \"brief-host\"; }\n"+
		"export fun mgs_getHarness() > Harness { return Harness{config = HarnessConfig{path = \"host/hooks.json\"}, guard = \"\"}; }\n"+
		"export fun mgs_renderPrompts(humanOnly: {str: str}) > [Prompt] { return []; }\n")
	w.Write("AGENTS.md", "# rules\n")
	fixture.WireHarness(t, agent.HarnessDescriptor{
		APIVersion: agent.HarnessAPIVersion,
		ID:         "brief-host",
		Display:    agent.HarnessDisplay{Name: "Brief Host"},
		Config:     types.HarnessConfig{Path: "host/hooks.json"},
		Skills:     types.HarnessSkills{Paths: []string{".agents/skills"}, Form: types.SkillFormBoth},
		ManagedEntries: []types.ManagedEntries{{
			Path: []string{"hooks", "before"},
			Entries: []map[string]any{{
				"match":    "run",
				"commands": []any{map[string]any{"type": "command", "command": "./magus buzz -s magus-command.buzz"}},
			}},
		}},
	})
}

// seedBriefLease records one running lease on root's checkout, its goal two lines long.
func seedBriefLease(t *testing.T, root string) {
	t.Helper()
	store, err := testReaders(t, root).OpenJobs(t.Context(), root)
	require.NoError(t, err)
	guardRow := jobtypes.Job{
		ID:           "f2-guard",
		State:        jobtypes.StateRunning,
		Agent:        jobtypes.Instructed("hold the boundary\nsecond line nobody reads here"),
		Validation:   "magus run test internal/ledger",
		Paths:        jobtypes.Paths{Write: []string{"internal/ledger"}},
		CheckoutRoot: root,
	}
	_, err = store.Update(t.Context(), guardRow.ID, func(cur *jobtypes.Job) { *cur = guardRow })
	require.NoError(t, err)
}

// seedFailingRun journals one session in root whose one target failed with ref.
func seedFailingRun(t *testing.T, root, ref string) {
	t.Helper()
	handler := sessions.NewFactHandler(root, session.InvocationStart{Workspace: root, Command: "run test"})
	require.NotNil(t, handler)
	emitJournalEvent(t, handler, journal.Event{
		Kind: journal.KindResult, Inv: "invBrief", Target: "test",
		Project: "internal/ledger", Status: journal.StatusFail, Ref: ref,
	})
}

// A refusal ran nothing, so it has no output ref; the code is the whole of what the brief
// can say about it. Another worktree's later failure must not stand in for this checkout's.
func TestSessionBriefFailuresAreThisCheckoutsAndCarryTheRefusalCode(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	refused := sessions.NewFactHandler(root, session.InvocationStart{Workspace: root, Command: "affected ci"})
	require.NotNil(t, refused)
	emitJournalEvent(t, refused, journal.Event{
		Kind: journal.KindResult, Inv: "invRefused", Target: "ci", Project: "pkg",
		Status: journal.StatusFail, DiagnosticCode: string(types.MachineBudgetExhausted),
	})
	time.Sleep(2 * time.Millisecond) // the other checkout's run is the newer one
	other := sessions.NewFactHandler(root, session.InvocationStart{Workspace: filepath.Join(root, "elsewhere"), Command: "run test"})
	require.NotNil(t, other)
	emitJournalEvent(t, other, journal.Event{
		Kind: journal.KindResult, Inv: "invOther", Target: "test", Project: "other", Status: journal.StatusFail, Ref: "ref-other",
	})

	failures := lastRunFailures(t.Context(), testReaders(t, root), root)
	require.Len(t, failures, 1)
	require.NotEmpty(t, failures[0].At)
	assert.Equal(t, briefFailure{Target: "ci", Project: "pkg", DiagnosticCode: "MGS3009", At: failures[0].At}, failures[0],
		"a refusal carries its code and no ref to inspect")

	var text strings.Builder
	Payload{Failures: failures}.writeFailures(&text)
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

	store, err := testReaders(t, root).OpenJobs(t.Context(), root)
	require.NoError(t, err)
	landed := jobtypes.Job{ID: "landed", State: jobtypes.StatePass, CheckoutRoot: root}
	_, err = store.Update(ctx, landed.ID, func(cur *jobtypes.Job) { *cur = landed })
	require.NoError(t, err)
	running := jobtypes.Job{ID: "running", State: jobtypes.StateRunning, CheckoutRoot: root}
	_, err = store.Update(ctx, running.ID, func(cur *jobtypes.Job) { *cur = running })
	require.NoError(t, err)

	brief := Gather(ctx, root, nil, testReaders(t, root))
	require.Len(t, brief.Leases, 1)
	assert.Equal(t, "running", brief.Leases[0].ID)
	assert.Zero(t, brief.OtherLeases)
}

func TestSessionBriefListsEachProvedClaim(t *testing.T) {
	t.Parallel()

	claims := claimLines([]jobtypes.Claim{{
		Path: "internal/guard/render.go", Evidence: jobtypes.ClaimEvidence{Kind: jobtypes.ClaimRefs, Subject: "hint.Render"},
		Proof: "internal/guard/render.go references hint.Render, which the job's diff changes in internal/hint/render.go",
	}})
	assert.Equal(t, []string{"internal/guard/render.go (refs:hint.Render): internal/guard/render.go references hint.Render, which the job's diff changes in internal/hint/render.go"}, claims)
	assert.Nil(t, claimLines(nil), "a job with no claims lists none")

	b := Payload{Leases: []briefLease{{ID: "render-change", State: string(jobtypes.StateRunning), Exec: hint.JobExec.With("render-change"), Claims: claims}}}
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

	store, err := testReaders(t, root).OpenJobs(t.Context(), root)
	require.NoError(t, err)
	// Existing checkouts: a running row whose checkout is gone is ended on read.
	for _, row := range []jobtypes.Job{
		{ID: "here", State: jobtypes.StateRunning, CheckoutRoot: root},
		{ID: "bound", State: jobtypes.StateDeclared},
		{ID: "elsewhere-1", State: jobtypes.StateRunning, CheckoutRoot: t.TempDir()},
		{ID: "elsewhere-2", State: jobtypes.StateExited, CheckoutRoot: t.TempDir()},
		{ID: "declared-unbound", State: jobtypes.StateDeclared},
		{ID: "done-here", State: jobtypes.StatePass, CheckoutRoot: root},
	} {
		_, err = store.Update(ctx, row.ID, func(cur *jobtypes.Job) { *cur = row })
		require.NoError(t, err)
	}
	// Bound after the rows are written: the store refuses a bound session's write to a
	// row it did not create.
	t.Setenv(trail.EnvBaggage, "magus.lease=bound")

	brief := Gather(ctx, root, nil, testReaders(t, root))
	ids := make([]string, len(brief.Leases))
	for i, l := range brief.Leases {
		ids[i] = l.ID
	}
	assert.ElementsMatch(t, []string{"here", "bound"}, ids)
	assert.Equal(t, 3, brief.OtherLeases)
	text := brief.Text()
	assert.Contains(t, text, fmt.Sprintf("\n3 other leases: `%s`\n", hint.LsJobs.String()))
	assert.NotContains(t, text, "elsewhere")
	assertBriefIsContextSafe(t, text)

	// With nothing bound here, the leases section is that one line.
	var only strings.Builder
	Payload{OtherLeases: 3}.writeLeases(&only)
	assert.Equal(t, fmt.Sprintf("3 other leases: `%s`\n", hint.LsJobs.String()), only.String())
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

	store, err := testReaders(t, root).OpenJobs(t.Context(), root)
	require.NoError(t, err)
	for i := 0; i < briefMaxLeases+5; i++ {
		row := jobtypes.Job{ID: "exited-" + strconv.Itoa(i), State: jobtypes.StateExited, CheckoutRoot: root}
		_, err = store.Update(ctx, row.ID, func(cur *jobtypes.Job) { *cur = row })
		require.NoError(t, err)
	}
	running := jobtypes.Job{ID: "running", State: jobtypes.StateRunning, CheckoutRoot: root}
	_, err = store.Update(ctx, running.ID, func(cur *jobtypes.Job) { *cur = running })
	require.NoError(t, err)

	brief := Gather(ctx, root, nil, testReaders(t, root))
	require.Len(t, brief.Leases, briefMaxLeases)
	assert.Equal(t, "running", brief.Leases[0].ID, "editing jobs are named before exited holders")
	assert.Equal(t, 6, brief.OtherLeases)
	text := brief.Text()
	assert.Contains(t, text, fmt.Sprintf("6 other leases: `%s`", hint.LsJobs.String()))
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

	var waiting strings.Builder
	Payload{Unintegrated: &briefUnintegrated{Count: 2, Branches: []string{"done-a", "done-b"}}}.writeUnintegrated(&waiting)
	assert.Contains(t, waiting.String(), "finished branches off the base: 2: done-a, done-b\n")
	assert.Contains(t, waiting.String(), fmt.Sprintf("integrate them: `%s`", hint.JobFork.With("--stdin", "--checkout", "<dir>")))

	var unknown strings.Builder
	Payload{Unintegrated: &briefUnintegrated{Unknown: "version control is disabled here"}}.writeUnintegrated(&unknown)
	assert.Equal(t, "finished branches off the base: unknown, version control is disabled here\n", unknown.String())

	var none strings.Builder
	Payload{}.writeUnintegrated(&none)
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

	store, err := testReaders(t, root).OpenJobs(t.Context(), root)
	require.NoError(t, err)
	for _, row := range []jobtypes.Job{
		{ID: "plan", State: jobtypes.StateRunning, Worktree: &jobtypes.Worktree{Root: work, Source: root}},
		{ID: "plan/exited", Parent: "plan", State: jobtypes.StateExited},
		{ID: "plan/waiting", Parent: "plan", State: jobtypes.StateDeclared, DependsOn: []string{"plan/exited"}},
		{ID: "plan/failed", Parent: "plan", State: jobtypes.StateFail},
		{ID: "landed", State: jobtypes.StatePass, Worktree: &jobtypes.Worktree{Root: t.TempDir(), Source: root, Removed: 1}},
		{ID: "elsewhere", State: jobtypes.StateRunning, Worktree: &jobtypes.Worktree{Root: t.TempDir(), Source: t.TempDir()}},
	} {
		_, err = store.Update(ctx, row.ID, func(cur *jobtypes.Job) { *cur = row })
		require.NoError(t, err)
		if row.Worktree != nil {
			_, err = store.RecordWorktree(ctx, row.ID, *row.Worktree)
			require.NoError(t, err)
		}
	}
	seedFiled(t, store, "plan/exited", &jobtypes.Result{UnresolvedRisks: []string{"merge is the root's", "second"}})

	brief := Gather(ctx, root, nil, testReaders(t, root))
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
		fmt.Sprintf(`exited and never collected, so each ends as no_return unless waited on:
  %s
`, hint.JobWait.With("plan/exited")),
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

	store, err := testReaders(t, root).OpenJobs(t.Context(), root)
	require.NoError(t, err)
	for _, row := range []jobtypes.Job{
		{ID: "worker", State: jobtypes.StateRunning},
		{ID: "worker/a", Parent: "worker", State: jobtypes.StateDeclared},
		{ID: "worker/a/b", Parent: "worker/a", State: jobtypes.StatePass},
		{ID: "sibling", State: jobtypes.StateRunning},
	} {
		_, err = store.Update(ctx, row.ID, func(cur *jobtypes.Job) { *cur = row })
		require.NoError(t, err)
	}
	t.Setenv(trail.EnvBaggage, "magus.lease=worker")

	brief := Gather(ctx, root, nil, testReaders(t, root))
	assert.Equal(t, &briefJobTree{Root: "worker", Jobs: []briefJob{
		{ID: "worker/a", State: "declared", Next: "take it: " + hint.JobExec.With("worker/a")},
		{ID: "worker/a/b", State: "pass", Depth: 1},
	}}, brief.Jobs)
	assert.Contains(t, brief.Text(), "jobs forked under worker:\n  worker/a (declared) no checkout yet\n")
}

// An unread store must not read as a session with no jobs.
func TestSessionBriefJobTreeCapsAndNamesWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	unread := Payload{Jobs: &briefJobTree{Unread: "permission denied"}}.Text()
	assert.Contains(t, unread, fmt.Sprintf("jobs: the store could not be read (permission denied); `%s`", hint.LsJobs.String()))
	assert.NotContains(t, Payload{}.Text(), "jobs", "a session that owns no jobs prints no tree")

	unpushed := &briefUnpushed{Base: "origin/main", Count: 2}
	assert.Equal(t, fmt.Sprintf("gate it in its checkout (%s), then push", hint.Affected.With("ci")),
		briefNextStep(nil, jobtypes.Job{ID: "done", State: jobtypes.StatePass}, unpushed))
	assert.Empty(t, briefNextStep(nil, jobtypes.Job{ID: "done", State: jobtypes.StatePass}, &briefUnpushed{Base: "origin/main"}))
	assert.Equal(t, "waits on gone, which no job declares",
		briefNextStep(nil, jobtypes.Job{ID: "w", State: jobtypes.StateDeclared, DependsOn: []string{"gone"}}, nil))

	b := Payload{Workspace: "/checkout", Jobs: &briefJobTree{
		Jobs: []briefJob{{ID: "done", State: "pass", Checkout: "/checkout", Branch: "done", Head: "abc1234", Unpushed: unpushed}},
		More: 3,
	}}
	text := b.Text()
	assert.Contains(t, text, "  done (pass) on done at abc1234 in this checkout, 2 commit(s) not on origin/main\n")
	assert.Contains(t, text, fmt.Sprintf("  and 3 more: `%s`\n", hint.LsJobs.String()))
	assertBriefIsContextSafe(t, text)
}

// An exited job nobody waits on ends as no_return, so each one is named with its wait line,
// past the cap too; one a wait already rejected has been collected.
func TestSessionBriefNamesEveryExitedJobNobodyCollected(t *testing.T) {
	t.Parallel()

	rows := []jobtypes.Job{{ID: "root", State: jobtypes.StateRunning}}
	for i := range briefMaxJobs + 2 {
		rows = append(rows, jobtypes.Job{ID: "root/" + strconv.Itoa(i), Parent: "root", State: jobtypes.StateExited})
	}
	rows = append(rows, jobtypes.Job{ID: "root/rejected", Parent: "root", State: jobtypes.StateExited, Rejection: &jobtypes.Rejection{Violations: []string{"check failed"}}})

	tree := sessionJobTree(t.Context(), rows, nil, "root", t.TempDir(), types.VCSOptions{})
	require.NotNil(t, tree)
	assert.Len(t, tree.Jobs, briefMaxJobs)
	assert.Equal(t, 3, tree.More)
	require.Len(t, tree.Uncollected, briefMaxJobs+2)
	assert.NotContains(t, tree.Uncollected, "root/rejected")

	text := Payload{Jobs: tree}.Text()
	assert.Contains(t, text, "exited and never collected, so each ends as no_return unless waited on:\n")
	assert.Contains(t, text, fmt.Sprintf(`
  %s
`, hint.JobWait.With("root/13")), "a job past the cap is still named")
	assertBriefIsContextSafe(t, text)
}

func TestSessionBriefListsWhatIsNotMerged(t *testing.T) {
	t.Parallel()
	brief := Payload{Unmerged: jobtypes.UnmergedReport{Branch: "feat/x", Jobs: []jobtypes.Unmerged{
		{Job: "w", Head: "abc123", Why: "2 commit(s) are not on feat/x", Remedy: "merge abc123"},
	}}}

	text := brief.Text()

	assert.Contains(t, text, "not merged onto feat/x: 1")
	assert.Contains(t, text, "w at abc123: 2 commit(s) are not on feat/x; merge abc123")
}

func TestSessionBriefSaysAnUnknownBranchIsUnknown(t *testing.T) {
	t.Parallel()
	brief := Payload{Unmerged: jobtypes.UnmergedReport{Unknown: "no branch checked out"}}

	assert.Contains(t, brief.Text(), "not merged: unknown, no branch checked out")
}

func TestSessionBriefCapsALongUnmergedReport(t *testing.T) {
	t.Parallel()
	var rows []jobtypes.Unmerged
	for i := range briefUnmergedLines + 3 {
		rows = append(rows, jobtypes.Unmerged{Job: fmt.Sprintf("w%d", i), Why: "off", Remedy: "merge"})
	}

	text := Payload{Unmerged: jobtypes.UnmergedReport{Jobs: rows}}.Text()

	assert.Contains(t, text, "and 4 more")
	assert.NotContains(t, text, "w12")
}

// TestClassifyByRole pins the three-way split a session brief shows: anything not
// declared source or output is undeclared, including an empty role.
func TestClassifyByRole(t *testing.T) {
	out := []types.FileEntry{
		{Path: "cmd/magus/agent.go", Role: "source"},
		{Path: "MAGUS.md", Role: "output"},
		{Path: "docs/gen/index.html", Role: "output"},
		{Path: "scratch.txt", Role: "unclaimed"},
		{Path: "notes.md", Role: ""},
	}

	sources, outputs, undeclared := classifyByRole(out)
	assert.Equal(t, []string{"cmd/magus/agent.go"}, sources)
	assert.Equal(t, []string{"MAGUS.md", "docs/gen/index.html"}, outputs)
	assert.Equal(t, []string{"scratch.txt", "notes.md"}, undeclared)
}

// testReaders reads the job store and the lease binding the way the CLI does, with the
// stores under the isolated state directory.
func testReaders(t *testing.T, root string) Readers {
	t.Helper()
	base, err := xdg.StateDir()
	require.NoError(t, err)
	loc := job.Location{CacheDir: filepath.Join(base, "cache"), StateBase: base, Root: root}
	return Readers{
		OpenJobs: func(context.Context, string) (*job.Store, error) { return job.NewStore(loc), nil },
		ActingLease: func(ctx context.Context, _ string) string {
			lease, _ := job.RunLease(ctx, loc)
			return lease
		},
		PromptCache: func(string, time.Time) sessions.PromptCacheClock { return sessions.PromptCacheClock{} },
		CacheDir:    func(string) (string, error) { return "", errors.New("no cache directory in this test") },
		ResolveRoot: func(root string) string { return root },
		ConsoleURL:  func() string { return "" },
		InCheckout: func(summaries []session.Summary, root string) []session.Summary {
			var out []session.Summary
			for _, s := range summaries {
				if s.Workspace == "" || filepath.Clean(s.Workspace) == filepath.Clean(root) {
					out = append(out, s)
				}
			}
			return out
		},
	}
}

func emitJournalEvent(t *testing.T, h slog.Handler, e journal.Event) {
	t.Helper()
	journal.Emit(journal.WithLogger(context.Background(), journal.NewLogger(h)), e)
}

// seedFiled files result as the job's first attempt, beside store's rows. The job package
// files a result only through an exit; internal/guard keeps the same copy.
func seedFiled(t *testing.T, store *job.Store, id string, result *jobtypes.Result) {
	t.Helper()
	path, err := store.Path()
	require.NoError(t, err)
	raw, err := codec.Marshal(result)
	require.NoError(t, err)
	var filed map[string]any
	require.NoError(t, codec.Unmarshal(raw, &filed))
	line, err := codec.Marshal(map[string]any{
		"apiVersion": jobtypes.APIVersion, "kind": "Attempt",
		"spec": map[string]any{"job": id, "n": 1}, "status": map[string]any{"result": fixtureCamelKeys(filed)},
	})
	require.NoError(t, err)
	require.NoError(t, codec.AppendLines(filepath.Join(filepath.Dir(path), "attempts.jsonl"), [][]byte{line}, 0o600))
}

// fixtureCamelKeys is v with every object key camelCased, as the attempts.jsonl record
// spells them.
func fixtureCamelKeys(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			parts := strings.Split(k, "_")
			for i := 1; i < len(parts); i++ {
				parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
			}
			out[strings.Join(parts, "")] = fixtureCamelKeys(x)
		}
		return out
	case []any:
		for i := range v {
			v[i] = fixtureCamelKeys(v[i])
		}
		return v
	}
	return v
}
