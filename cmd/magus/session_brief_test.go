package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/internal/sessions"
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
			Bind:  "magus session lease f2-guard",
			// Longer than a line on purpose: the clip is part of the contract.
			Goal:       strings.Repeat("hold the boundary ", 20),
			Validation: "magus run test internal/ledger",
		}},
		Failures: []briefFailure{{
			Target: "test", Project: "internal/ledger",
			Ref: "abc123", Inspect: "magus query output abc123", At: "2026-09-10 12:03:04",
		}},
		GuardWiring: []string{".claude/settings.json"},
		Rules:       []string{"AGENTS.md", ".claude/skills"},
		PromptCache: briefClock(),
	}

	text := b.Text()
	for _, want := range []string{
		"branch: w-compact-hook  revision: a41984c9c",
		"unpushed: 3 commit(s) not on origin/main",
		"tree: 3 changed file(s): 1 source, 1 output, 1 unclaimed",
		"leases live here:",
		"bind: magus session lease f2-guard",
		"validation: magus run test internal/ledger",
		"the last recorded run failed:",
		"magus query output abc123",
		"guard wiring: .claude/settings.json",
		"rules live in AGENTS.md, .claude/skills",
		"prompt cache: last tool call here 7m ago",
		"closed: Anthropic default",
		"open: Anthropic 1h opt-in",
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

// The gather, against a checkout on disk. Every section it fills here is read out of
// a file this test wrote, which is the property the whole surface rests on: a brief
// that could be produced without the checkout would be a summary again.
func TestSessionBriefReadsTheCheckout(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	global = globalFlags{}
	root := t.TempDir()
	ctx := context.Background()

	// An empty magusfile marks the directory as a workspace root.
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# rules\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".claude"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".claude", "settings.json"),
		[]byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"command":"magus session hook"}]}]}}`), 0o644))

	store, err := openLedger(root)
	require.NoError(t, err)
	_, err = store.Put(ctx, types.Lease{
		ID:         "f2-guard",
		State:      types.StateRunning,
		Goal:       "hold the boundary\nsecond line nobody reads here",
		Validation: "magus run test internal/ledger",
		OwnedPaths: []string{"internal/ledger"},
	})
	require.NoError(t, err)

	// One session, one failing target: the run history the brief reads back.
	handlers := withSessionJournal(ctx, nil, root, "run", []string{"test"})
	require.Len(t, handlers, 1)
	emitJournalEvent(t, handlers[0], journal.Event{
		Kind: journal.KindResult, Inv: "invBrief", Target: "test",
		Project: "internal/ledger", Status: journal.StatusFail, Ref: "ref-1",
	})

	brief := gatherSessionBrief(ctx, root, nil)

	assert.Equal(t, root, brief.Workspace)
	require.Len(t, brief.Leases, 1)
	assert.Equal(t, "f2-guard", brief.Leases[0].ID)
	assert.Equal(t, "hold the boundary", brief.Leases[0].Goal, "a lease's goal reads as one line here; the rest is `magus ledger brief`")
	assert.Equal(t, ledger.NewBrief(types.Lease{ID: "f2-guard"}, ledger.BriefFacts{}).Bind, brief.Leases[0].Bind)

	require.Len(t, brief.Failures, 1)
	assert.Equal(t, "ref-1", brief.Failures[0].Ref)
	assert.Equal(t, "magus query output ref-1", brief.Failures[0].Inspect)

	assert.Equal(t, []string{filepath.Join(".claude", "settings.json")}, brief.GuardWiring)
	assert.Contains(t, brief.Rules, "AGENTS.md")

	text := brief.Text()
	assert.Contains(t, text, "magus query output ref-1")
	assert.Contains(t, text, "magus session lease f2-guard")
	assertBriefIsContextSafe(t, text)
}

// A terminal lease has stopped competing for its paths, so a rehydrating session
// must not be told to bind it.
func TestSessionBriefSkipsLeasesThatAreDone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	ctx := context.Background()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))

	store, err := openLedger(root)
	require.NoError(t, err)
	_, err = store.Put(ctx, types.Lease{ID: "landed", State: types.StatePass})
	require.NoError(t, err)
	_, err = store.Put(ctx, types.Lease{ID: "running", State: types.StateRunning})
	require.NoError(t, err)

	brief := gatherSessionBrief(ctx, root, nil)
	require.Len(t, brief.Leases, 1)
	assert.Equal(t, "running", brief.Leases[0].ID)
}

// briefClock is a session idle long enough that one window is behind it and the rest
// are not, which is the only state where the two-group split says something.
func briefClock() sessions.PromptCacheClock {
	last := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	return sessions.PromptCacheAt("s1", last, last.Add(7*time.Minute))
}
