package guard

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdviseInstalledSkillWrite pins the discriminator: the STAMP decides, not
// the path. Both files below sit in the same directory under a magus-* name,
// and only one of them is magus's to overwrite.
func TestAdviseInstalledSkillWrite(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) string {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		return path
	}

	installed := write(".claude/skills/magus-run/SKILL.md", "---\nname: magus-run\nmetadata:\n  source: magus\n---\n\n# Running work\n")
	got := adviseInstalledSkillWrite(installed)
	assert.Contains(t, got, "INSTALLED skill")
	assert.Contains(t, got, "magus-workspace-rules")

	// A workspace's own skill lives in the same directory and must draw silence:
	// telling an author their hand-written file is generated is worse than
	// saying nothing.
	local := write(".claude/skills/"+agent.LocalSkillName+"/SKILL.md", "---\nname: "+agent.LocalSkillName+"\nmetadata:\n  source: workspace\n---\n\n# Our rules\n")
	assert.Empty(t, adviseInstalledSkillWrite(local))

	// The embedded SOURCE an installed copy is generated from carries no frontmatter, and
	// its prose may quote the stamp; only the frontmatter is the stamp.
	source := write("internal/agent/skills/magus-workspace-rules/SKILL.md",
		"# Adapting the agent integration\n\nIf a file's frontmatter says `source: magus`, it is not yours.\n\nsource: magus\n")
	assert.Empty(t, adviseInstalledSkillWrite(source))
	quoted := write(".claude/skills/team-rules/SKILL.md", "---\nname: team-rules\n---\n\nsource: magus\n")
	assert.Empty(t, adviseInstalledSkillWrite(quoted), "a stamp in the body is prose")

	// Not a skill file, not in a skill directory, and not there at all.
	assert.Empty(t, adviseInstalledSkillWrite(write(".claude/skills/magus-run/README.md", "source: magus")))
	assert.Empty(t, adviseInstalledSkillWrite(write("docs/SKILL.md", "source: magus")))
	assert.Empty(t, adviseInstalledSkillWrite(filepath.Join(dir, ".claude", "skills", "magus-vcs-hygiene", "SKILL.md")))
}

// fleetFixture stands up a workspace root and a lease ledger holding leases, and
// returns the context pinning both plus the root. Everything lands in temporary
// directories, so a guard test never reads or writes the checkout's real ledger.
func fleetFixture(t *testing.T, leases ...types.Job) (context.Context, string) {
	t.Helper()
	// The ledger now lives in the per-repository state directory, and the guard resolves
	// it with no seam a test can reach, so the environment is what keeps this off the
	// developer's own ledger.
	testkit.Isolate(t)
	root, cacheDir := t.TempDir(), t.TempDir()
	store := job.NewStore(job.Location{CacheDir: cacheDir, Root: root})
	for _, u := range leases {
		_, err := store.Update(t.Context(), u.ID, func(cur *types.Job) { *cur = u })
		require.NoError(t, err)
	}
	location := location{cacheDir: cacheDir, workspace: root}
	return context.WithValue(t.Context(), locationKey{}, location), root
}

// fleetLeases is the two-lease plan most cases below grade against: two live workers with
// disjoint write paths, one of them declaring a denied subtree inside its own.
//
// Both are REGISTERED, because these cases are about boundaries and an unregistered lease is
// denied before any boundary is consulted. TestGradeLeasedWriteRequiresACheckpoint covers that
// rule on its own.
func fleetLeases() []types.Job {
	return []types.Job{
		{
			ID:           "lease-a",
			Criteria:     "own the ledger store\nacceptance: List stays cheap",
			WritePaths:   []string{"internal/ledger/**"},
			State:        types.StateRunning,
			Checkpoint:   "rev-a",
			ReportedBase: "rev-a",
			BaseVerdict:  types.BaseMatch,
			Registered:   1,
		},
		{
			ID:           "lease-b",
			Criteria:     "grade writes in the guard",
			WritePaths:   []string{"cmd/magus/**", "docs/guard.md"},
			DenyPaths:    []string{"cmd/magus/gen/**"},
			State:        types.StateDeclared,
			Checkpoint:   "rev-a",
			ReportedBase: "rev-a",
			BaseVerdict:  types.BaseMatch,
			Registered:   1,
		},
	}
}

// TestGradeLeasedWriteDenies pins the two denials and the fact each one must carry:
// the owning lease's id, its goal's first line, and a next step. A denial that only says
// no sends the agent around the guard, which is the failure the whole ledger design is
// built to avoid.
func TestGradeLeasedWriteDenies(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)

	t.Run("inside another live lease's write paths", func(t *testing.T) {
		got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/ledger/store.go"))
		require.Equal(t, "deny", got.Decision)
		assert.Contains(t, got.Reason, "lease-a", "the denial must name the owner")
		assert.Contains(t, got.Reason, "own the ledger store", "the denial must carry the owner's goal")
		assert.NotContains(t, got.Reason, "acceptance:",
			"only the goal's FIRST line belongs in a denial; the criteria block would bury the next step")
		assert.Contains(t, got.Reason, "re-partition", "the denial must name a next step")
		assert.Contains(t, got.Reason, "lease-b", "the denial must say who magus thinks is writing")
	})

	t.Run("inside the acting lease's own deny paths", func(t *testing.T) {
		// Also pins the precedence: cmd/magus/gen is inside lease-b's write tree AND on its
		// deny list, and the more specific declaration is the one that decides.
		got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/gen/cli_flags.go"))
		require.Equal(t, "deny", got.Decision)
		assert.Contains(t, got.Reason, "DENIED")
		assert.Contains(t, got.Reason, "lease-b")
		assert.Contains(t, got.Reason, "cmd/magus/gen/**", "the denial must quote the declaration it matched")
	})

	t.Run("outside the acting lease's own write paths", func(t *testing.T) {
		// Ground nobody else claims. The boundary the orchestrator handed out is still the
		// boundary, and a worker that widens its own is what the declaration exists to catch.
		got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "README.md"))
		require.Equal(t, "deny", got.Decision)
		assert.Contains(t, got.Reason, "lease-b")
		assert.Contains(t, got.Reason, "README.md")
		assert.Contains(t, got.Reason, "write_paths", "the denial must name the field that decided it")
		assert.Contains(t, got.Reason, "cmd/magus/**", "the denial must list the write paths it was measured against")
	})

	t.Run("a read-only lease writing anywhere", func(t *testing.T) {
		leases := append(fleetLeases(), types.Job{
			ID:       "scout",
			Criteria: "inventory the guard rules",
			ReadOnly: true,
			State:    types.StateRunning,
		})
		ctx, root := fleetFixture(t, leases...)
		got := gradeLeasedWrite(ctx, Dependencies{}, "scout", filepath.Join(root, "README.md"))
		require.Equal(t, "deny", got.Decision)
		assert.Contains(t, got.Reason, "read_only", "the denial must name the field that decided it")
		assert.Contains(t, got.Reason, "scout")
		// Ahead of the registration rule: this row never registered, and being told to
		// checkpoint first would be a second refusal for one mistake.
		assert.NotContains(t, got.Reason, "checkpoint")
	})
}

// TestGradeLeasedWritePasses covers the silences. Each is a case where the guard has
// no opinion, which is different from clearing the write: a later rule still gets to
// speak, and the empty Decision is what leaves room for it.
func TestGradeLeasedWritePasses(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)

	t.Run("inside the acting lease's own write paths", func(t *testing.T) {
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/agent.go")).Decision)
	})

	t.Run("a lease that declared no write paths", func(t *testing.T) {
		// An empty owned set is a boundary nobody wrote, not a boundary of size zero, so it
		// scopes nothing. read_only is what says a lease writes nothing on purpose.
		leases := fleetLeases()
		leases[1].WritePaths, leases[1].DenyPaths = nil, nil
		ctx, root := fleetFixture(t, leases...)
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "README.md")).Decision)
	})

	t.Run("outside the workspace", func(t *testing.T) {
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(t.TempDir(), "elsewhere.go")).Decision)
	})

	t.Run("un-enrolled on ground no lease claims", func(t *testing.T) {
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "", filepath.Join(root, "README.md")).Decision)
	})
}

// TestGradeLeasedWriteIdleFleet is the zero-cost contract: with nothing to grade
// against, the guard reads the ledger and then says nothing, whatever the path.
func TestGradeLeasedWriteIdleFleet(t *testing.T) {
	t.Run("no ledger at all", func(t *testing.T) {
		ctx, root := fleetFixture(t)
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/ledger/store.go")).Decision)
	})

	t.Run("every lease terminal", func(t *testing.T) {
		leases := fleetLeases()
		leases[0].State, leases[1].State = types.StatePass, types.StateNoReturn
		ctx, root := fleetFixture(t, leases...)
		// A finished lease has stopped competing for its paths, which is the rule
		// types.leaseOverlaps applies when it decides which pairs to report.
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/ledger/store.go")).Decision)
	})

	t.Run("no state recorded", func(t *testing.T) {
		// The store stores a row naming no state as declared, so the legacy row an older
		// magus left is planted as the file it wrote.
		ctx, root := fleetFixture(t)
		leases := fleetLeases()
		leases[0].State, leases[1].State = "", ""
		body, err := json.Marshal(map[string]any{"jobs": leases})
		require.NoError(t, err)
		path, err := job.NewStore(job.Location{CacheDir: ctx.Value(locationKey{}).(location).cacheDir, Root: root}).Path()
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, body, 0o644))
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/ledger/store.go")).Decision)
	})

	t.Run("no trail location", func(t *testing.T) {
		// Pinned to an EMPTY location rather than left unpinned: an unpinned context sends
		// hookLocation up from the CWD to this checkout's real cache dir, and the test
		// would then grade against whatever plan the developer is actually running.
		ctx := context.WithValue(t.Context(), locationKey{}, location{})
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "lease-b", "internal/ledger/store.go").Decision)
	})
}

// TestGradeLeasedWriteMalformedDeclaration pins the fail-open being made VISIBLE. The
// matcher's error was discarded, so a declaration it could not read matched nothing: a
// deny path spelled with a stray bracket stopped denying and said so nowhere, which
// is the shape of failure this rule is least able to afford: a boundary that looks
// enforced and is not.
func TestGradeLeasedWriteMalformedDeclaration(t *testing.T) {
	t.Run("the acting lease's own deny list", func(t *testing.T) {
		leases := fleetLeases()
		leases[1].DenyPaths = []string{"cmd/magus/[gen/**"}
		ctx, root := fleetFixture(t, leases...)
		got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/gen/cli_flags.go"))
		require.Equal(t, "advise", got.Decision, "an unreadable pattern says nothing about the write, only that nothing graded it")
		assert.Contains(t, got.Context, "cmd/magus/[gen/**", "the advisory must name the pattern to fix")
		assert.Contains(t, got.Context, "lease-b")
		assert.Contains(t, got.Context, "not being enforced")
	})

	t.Run("another lease's write paths", func(t *testing.T) {
		leases := fleetLeases()
		leases[0].WritePaths = []string{"internal/[ledger/**"}
		ctx, root := fleetFixture(t, leases...)
		got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/ledger/store.go"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "lease-a")
	})

	t.Run("a valid entry still denies through an earlier malformed one", func(t *testing.T) {
		// The malformed pattern comes first, the valid glob that covers the write second.
		// Short-circuiting on the bad pattern downgraded this deny to an advisory: a valid
		// deny boundary must still hold when a sibling entry is unreadable.
		leases := fleetLeases()
		leases[1].DenyPaths = []string{"cmd/magus/[gen/**", "cmd/magus/gen/**"}
		ctx, root := fleetFixture(t, leases...)
		got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/gen/cli_flags.go"))
		require.Equal(t, "deny", got.Decision, "the valid deny pattern must deny even though an earlier entry could not be read")
	})
}

// TestGradeLeasedWriteUnenrolled is the doctrine case: a writer magus cannot attribute
// is told what it is walking into and is never stopped. magus cannot tell "not part of the
// fleet" from "part of it and not saying so", and blocking a person in their own checkout
// is the wrong way to be wrong.
func TestGradeLeasedWriteUnenrolled(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)
	got := gradeLeasedWrite(ctx, Dependencies{}, "", filepath.Join(root, "internal/ledger/store.go"))
	require.Equal(t, "advise", got.Decision)
	assert.Contains(t, got.Context, "lease-a", "the advisory must name the lease already working there")
	assert.Contains(t, got.Context, "own the ledger store")
	assert.Contains(t, got.Context, "magus.lease", "the advisory must say how to enroll")
	assert.Contains(t, got.Context, "seatbelt", "the advisory must say why it is not a block")
}

// TestGradeLeasedWriteInvalidLeaseID pins the treated-as-absent contract. A typo'd id must
// not silently buy un-enrolled treatment: erroring would block the tool call over metadata,
// so the write is graded as naming no lease and the notice saying so comes from
// adviseInvalidLease, which hookCmd fires for BOTH kinds of call.
func TestGradeLeasedWriteInvalidLeaseID(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)

	t.Run("the notice itself is the id rule, not the write rule", func(t *testing.T) {
		assert.Contains(t, adviseInvalidLease("lease b!"), "not a valid lease id")
		assert.Contains(t, adviseInvalidLease("lease b!"), "magus.lease")
		assert.Contains(t, adviseInvalidLease(strings.Repeat("u", types.MaxJobIDLen+1)), "not a valid lease id")
		assert.Empty(t, adviseInvalidLease("lease-a"))
		assert.Empty(t, adviseInvalidLease(""), "naming no lease is not a typo")
	})

	t.Run("on unclaimed ground the write rule has nothing to say", func(t *testing.T) {
		got := gradeLeasedWrite(ctx, Dependencies{}, "lease b!", filepath.Join(root, "README.md"))
		assert.Empty(t, got.Decision)
	})

	t.Run("on owned ground it advises rather than denying", func(t *testing.T) {
		// The id is unusable, so the write is graded as un-enrolled, and an un-enrolled
		// write is never denied, even on another lease's ground.
		got := gradeLeasedWrite(ctx, Dependencies{}, "lease b!", filepath.Join(root, "internal/ledger/store.go"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "lease-a")
	})

	t.Run("a valid id nobody declared is un-enrolled, not denied", func(t *testing.T) {
		got := gradeLeasedWrite(ctx, Dependencies{}, "lease-z", filepath.Join(root, "internal/ledger/store.go"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "lease-a")
		assert.NotContains(t, got.Context, "not a valid lease id")
	})
}

// TestGradeLeasedWriteCorruptLedger is the fail-open case. A guard that blocked on a
// file it cannot parse would take the whole fleet down with one bad write; it says so
// instead, because a boundary that silently stopped being checked looks exactly like a
// fleet nobody declared.
func TestGradeLeasedWriteCorruptLedger(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)
	path, err := fleetLedger(t, ctx).Path()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/ledger/store.go"))
	assert.NotEqual(t, "deny", got.Decision, "a job store magus cannot read must never block an edit")
	require.Equal(t, "advise", got.Decision)
	assert.Contains(t, got.Context, "could not be read")
	assert.Contains(t, got.Context, "client tool", "the advisory must name the tool that re-declares the plan")
}

// TestDeclarationCovering pins the glob vocabulary a denial rests on. The precision matters
// more here than in types.pathsIntersect, which over-reports on purpose: this answer blocks
// a write, and a guard that blocks legitimate edits is one agents learn to route around.
func TestDeclarationCovering(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		decl, rel string
		want      bool
	}{
		{"internal/ledger", "internal/ledger/store.go", true},
		{"internal/ledger/**", "internal/ledger/sub/store.go", true},
		{"internal/ledger/*.go", "internal/ledger/store.go", true},
		{"internal/ledger/*.go", "internal/ledger/sub/store.go", false},
		{"cmd/magus/agent.go", "cmd/magus/agent.go", true},
		{"cmd/magus/agent.go", "cmd/magus/agent_test.go", false},
		{"internal/ledger", "internal/ledgerkeeper/store.go", false},
		{"console/src/**/*.ts", "console/src/a/b.ts", true},
		// The case types.pathsIntersect deliberately gets "wrong": two leases splitting one
		// directory by extension do NOT collide, and truncating both to "console/src" would
		// deny an edit nobody is competing for.
		{"console/src/**/*.css", "console/src/a/b.ts", false},
		{"**/*.go", "internal/ledger/store.go", true},
		{"", "internal/ledger/store.go", false},
		{"   ", "internal/ledger/store.go", false},
		{".", "internal/ledger/store.go", false},
		{"/", "internal/ledger/store.go", false},
	} {
		t.Run(tt.decl+" vs "+tt.rel, func(t *testing.T) {
			_, got, err := declarationCovering([]string{tt.decl}, tt.rel)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("returns the declaration it matched, verbatim", func(t *testing.T) {
		decl, ok, err := declarationCovering([]string{"docs/**", "internal/ledger/**"}, "internal/ledger/store.go")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "internal/ledger/**", decl, "a denial quotes the declaration as the orchestrator wrote it")
	})

	// A pattern the matcher rejects used to be swallowed and read as "no declaration
	// covers this path", so a deny entry with a stray bracket silently stopped
	// denying while the rule still looked enforced.
	t.Run("a malformed pattern is reported, not silently unmatched", func(t *testing.T) {
		_, ok, err := declarationCovering([]string{"internal/[ledger"}, "internal/ledger/store.go")
		require.Error(t, err)
		assert.False(t, ok)
		assert.Contains(t, err.Error(), "internal/[ledger", "the advisory has to name the pattern to fix")
	})
}

// A deny path naming one declaration denies the edits that change it and no others.
// Observed: a job denied `knowledge.go#loadKnowledgeAgentContacts` had every edit to
// knowledge.go denied, a doc comment in another function included.
func TestGradeLeasedEditDeniedDeclaration(t *testing.T) {
	denier := claimLease("denier", "run.go", "notes.txt")
	denier.DenyPaths = []string{"run.go#A", "notes.txt#Intro"}
	ctx, root := claimFixture(t, denier)
	run := filepath.Join(root, "run.go")
	notes := filepath.Join(root, "notes.txt")
	require.NoError(t, os.WriteFile(notes, []byte("Intro\nhello\n"), 0o644))
	edit := func(oldText, newText string) writeFields { return writeFields{OldText: oldText, NewText: newText} }

	for _, tc := range []struct {
		name   string
		path   string
		fields writeFields
		want   string // "" passes; otherwise a fragment of the deny reason
	}{
		{name: "another declaration", path: run, fields: edit("b()", "b2()")},
		{name: "above the first declaration", path: run, fields: edit("package run\n", "package run\n\nimport \"fmt\"\n")},
		{name: "a whole-file write leaving the declaration alone", path: run, fields: writeFields{Content: strings.Replace(claimedGo, "c()", "c2()", 1)}},
		{name: "the named declaration", path: run, fields: edit("a()", "a2()"), want: `this edit changes func A() { in run.go, which your lease denier declared DENIED as "run.go#A"`},
		{name: "every edit of a sequence is placed", path: run, fields: writeFields{Edits: []textEdit{
			{OldText: "b()", NewText: "b2()"}, {OldText: "a()", NewText: "a2()"},
		}}, want: `declared DENIED as "run.go#A"`},
		{name: "a whole-file write changing the declaration", path: run, fields: writeFields{Content: strings.Replace(claimedGo, "a()", "a2()", 1)}, want: `declared DENIED as "run.go#A"`},
		{name: "deleting the declaration", path: run, fields: edit("func A() {\n\ta()\n}\n\n", ""), want: `declared DENIED as "run.go#A"`},
		{name: "an edit that does not apply is denied whole", path: run, fields: edit("absent()", "x()"), want: "could not be placed in a declaration"},
		{name: "a write with no payload is denied whole", path: run, want: `declared "run.go#A" DENIED`},
		{name: "a file no diff driver reads is denied whole", path: notes, fields: edit("hello", "bye"), want: `declared "notes.txt#Intro" DENIED`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := gradeLeasedEdit(ctx, Dependencies{}, "denier", tc.path, tc.fields)
			if tc.want == "" {
				assert.Empty(t, got.Decision, got.Reason)
				return
			}
			require.Equal(t, "deny", got.Decision)
			assert.Contains(t, got.Reason, tc.want)
		})
	}
}

// A deny path naming a file or a pattern still denies every edit to what it covers, and so
// does a declaration of a pattern, which names no one file to read a declaration from.
func TestGradeLeasedEditWholeFileDeny(t *testing.T) {
	for _, deny := range []string{"run.go", "*.go", "**", "*.go#A"} {
		t.Run(deny, func(t *testing.T) {
			denier := claimLease("denier", "run.go")
			denier.DenyPaths = []string{deny}
			ctx, root := claimFixture(t, denier)
			got := gradeLeasedEdit(ctx, Dependencies{}, "denier", filepath.Join(root, "run.go"), writeFields{OldText: "b()", NewText: "b2()"})
			require.Equal(t, "deny", got.Decision)
			assert.Contains(t, got.Reason, "is covered by "+strconv.Quote(deny))
		})
	}
}

// A declaration claim is graded per declaration only for a writer whose own write paths
// claim declarations of the same file. A writer holding the file whole may edit the claimed
// declaration: fork reports that pair as an overlap, and sequencing it is the plan's. A
// writer claiming none of the file meets the claim at the path level, as owning the file.
func TestGradeLeasedEditAgainstAnotherJobsDeclarationClaim(t *testing.T) {
	stray := claimLease("stray")
	ctx, root := claimFixture(t, claimLease("whole", "run.go"), stray)
	run := filepath.Join(root, "run.go")
	edit := func(oldText, newText string) writeFields { return writeFields{OldText: oldText, NewText: newText} }

	assert.Empty(t, gradeLeasedEdit(ctx, Dependencies{}, "whole", run, edit("c()", "c2()")).Decision)
	assert.Empty(t, gradeLeasedEdit(ctx, Dependencies{}, "whole", run, edit("a()", "a2()")).Decision,
		"a whole-file holder is not graded against own-a's run.go#A")

	for _, fields := range []writeFields{edit("c()", "c2()"), edit("a()", "a2()")} {
		got := gradeLeasedEdit(ctx, Dependencies{}, "stray", run, fields)
		require.Equal(t, "deny", got.Decision)
		assert.Contains(t, got.Reason, "run.go is owned by lease own-a")
	}
}

// The lease-id shape itself is pinned in internal/trail's TestValidLeaseID; the guard's
// treated-as-absent behavior for a bad id is pinned by TestGradeLeasedWriteInvalidLeaseID.

// TestAdviseInstructionWrite pins the nudge to the two cross-host instruction files and
// to a wording that says what the file costs without telling the reader not to write it:
// host instructions belong exactly where they are being written.
func TestAdviseInstructionWrite(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"AGENTS.md", "CLAUDE.md", "claude.md", "/repo/nested/AGENTS.md", "  AGENTS.md  "} {
		advice := adviseInstructionWrite(path)
		require.NotEmpty(t, advice, "expected an advisory for %q", path)
		assert.Contains(t, advice, "every session", "the advisory names the cost")
		assert.Contains(t, advice, "magus doctor", "and the tool whose output makes a sentence here redundant")
	}
	for _, path := range []string{"", "README.md", "MAGUS.md", "docs/agents.md.tmpl", "agents.mdx"} {
		assert.Empty(t, adviseInstructionWrite(path), "no advisory belongs on %q", path)
	}
}

// TestDenyNotesWrite covers the only deny among the file-write rules. The negative cases matter
// more than the positive one: this rule blocks work, so it must be silent in every
// workspace that did not opt in by DECLARING a store.
func TestDenyNotesWrite(t *testing.T) {
	root := t.TempDir()
	// A workspace magus.FindRoot can resolve, so the rule reaches its real decision
	// rather than bailing out on a missing workspace and passing for the wrong reason.
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// scratch\n"), 0o644))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	// Nothing declared: the feature is off, so nothing is judged and nothing is guessed.
	for _, path := range []string{"notes/a.md", filepath.Join(root, "notes", "a.md"), "internal/foo.go"} {
		assert.Empty(t, denyNotesWrite(Dependencies{}, path),
			"with no declared store, %q must pass - a deny fired on a guess blocks work in a workspace that never opted in", path)
	}

	deps := Dependencies{NotesShared: "notes"}
	// The store must exist to be defended; see TestDenyNotesWriteRequiresTheStoreToExist.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "notes", "nested"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "team", "notes"), 0o755))

	for _, path := range []string{"notes/a.md", "./notes/a.md", filepath.Join(root, "notes", "a.md"), "  notes/nested/b.md  "} {
		reason := denyNotesWrite(deps, path)
		require.NotEmpty(t, reason, "expected a deny for %q", path)
		assert.Contains(t, reason, "NOTES store", "the reason names what was blocked")
		assert.Contains(t, reason, "magus notes edit", "and says how a person writes it instead")
	}

	// A path outside the declared store is untouched, including one that merely looks
	// like it (notes-archive shares the prefix but is a different directory).
	for _, path := range []string{"internal/foo.go", "docs/notes.md", "notes-archive/a.md", "../outside/a.md"} {
		assert.Empty(t, denyNotesWrite(deps, path), "%q is not in the declared store", path)
	}

	// The exclusion follows the declaration, not the name.
	wide := Dependencies{NotesShared: "team/notes"}
	assert.Empty(t, denyNotesWrite(wide, "notes/a.md"), "a different directory named notes is not the store")
	assert.NotEmpty(t, denyNotesWrite(wide, "team/notes/a.md"))
}

// TestDenyNotesWriteRequiresTheStoreToExist closes the hole a user-global config opens.
// magus reads config from an explicit --config path or $XDG_CONFIG_HOME before the
// workspace, so one global `knowledge.notes.path` would declare a store in every
// workspace. A declaration nobody acted on must defend nothing.
// TestDenyNotesWriteIgnoresAForeignDeclaration: the merged config carries settings from
// outside this repo (user-global, an explicit --config anywhere on disk), so a `notes.shared`
// set once on a machine is "declared" in every workspace on it. Acting on that alone would
// deny writes in repositories that never adopted the policy, so a declaration this repo did
// not make is backed by the on-disk store or it defends nothing.
func TestDenyNotesWriteIgnoresAForeignDeclaration(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// scratch\n"), 0o644))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	deps := Dependencies{NotesShared: "notes"}

	// No magus.yaml here, so the declaration can only have come from elsewhere on the
	// machine. This repo never opted in.
	assert.Empty(t, denyNotesWrite(deps, "notes/a.md"),
		"a declaration this repo did not make must not deny writes in it")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "notes"), 0o755))
	assert.NotEmpty(t, denyNotesWrite(deps, "notes/a.md"),
		"a store that exists on disk is defended whoever declared it")
}

// TestDenyNotesWriteDefendsAnEmptyDeclaredStore is the regression guard for the hole that
// dogfooding found on 2026-08-13: with the key declared and no note yet written, a direct
// file write to notes/<name>.md PASSED.
//
// The gate was "the directory exists", on the reasoning that a person creates the store by
// writing the first note, so an agent could never bring it into being. The reverse held.
// An agent could author the store's FIRST note (the single entry with nothing beside it to
// look wrong against), and the deny would switch on immediately afterwards, defending the
// forgery it had just let through. The opt-in is the committed key, not the directory.
func TestDenyNotesWriteDefendsAnEmptyDeclaredStore(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// scratch\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"),
		[]byte("knowledge:\n  notes:\n    shared: notes\n"), 0o644))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	deps := Dependencies{NotesShared: "notes"}

	require.NoDirExists(t, filepath.Join(root, "notes"), "the store has no files yet - that is the case under test")
	assert.NotEmpty(t, denyNotesWrite(deps, "notes/first.md"),
		"a repo that committed the key is defended before its first note exists, or an agent writes that first note")
	assert.NotEmpty(t, denyNotesWrite(deps, filepath.Join(root, "notes", "nested", "deep.md")))

	// Still scoped: declaring a notes store does not defend the rest of the repo.
	assert.Empty(t, denyNotesWrite(deps, "internal/foo.go"))
}

// TestGuardDeniesAuthoringANote closes the gap the path rule cannot see: these verbs
// author through a COMMAND, so denyNotesWrite's file-write rule never meets them.
func TestGuardDeniesAuthoringANote(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		"magus notes edit team-conventions",
		"printf 'prose' | magus notes edit team-conventions --anchor project:.",
		"cat body.md | ./magus notes edit foo",
		// A GLOBAL FLAG between the program and the verb. magus accepts these ahead of
		// the subcommand, so requiring `notes` immediately after `magus` left the rule
		// with a one-word bypass.
		"magus --root . notes edit team-conventions",
		"magus -o json notes edit foo",
		// A line the parser cannot read still falls back to the pattern.
		"magus notes edit foo && (",
		// `capture` defaults to the PRIVATE store, which denyNotesWrite never resolves.
		"magus notes capture",
		"magus notes capture --shared --title x",
		"./magus --root . notes capture",
		"magus notes capture && (",
	} {
		v := Evaluate(testDependencies(), cmd)
		assert.NotEmpty(t, v.Deny, "expected a deny for %q", cmd)
		assert.Equal(t, denyRule{Name: denyRuleNotesAuthor}, v.Rule, "%q must deny as the notes rule", cmd)
	}
	// Tokens after `--` go to the spell's tool, not to magus.
	assert.Empty(t, Evaluate(testDependencies(), "magus run go::go-test . -- notes capture").Deny)
	// Reading is untouched: the boundary is on authorship, not on access.
	for _, cmd := range []string{"magus notes ls", "magus notes get foo", "magus notes verify"} {
		assert.Empty(t, Evaluate(testDependencies(), cmd).Deny, "%q only reads", cmd)
	}
}

// fleetLedger reopens the ledger the guard just graded against, resolved exactly the way the guard
// resolves it, so these assertions read the same store the code under test wrote.
func fleetLedger(t *testing.T, ctx context.Context) *job.Store {
	t.Helper()
	loc := hookLocation(ctx, Dependencies{})
	return job.NewStore(job.Location{CacheDir: loc.cacheDir, Root: loc.workspace})
}

func unattributedOf(t *testing.T, store *job.Store, id string) []types.JobUnattributedWrite {
	t.Helper()
	rows, err := store.List()
	require.NoError(t, err)
	for _, r := range rows {
		if r.ID == id {
			return r.Unattributed
		}
	}
	return nil
}

// TestGradeLeasedWriteRecordsWhatItAdvisedAbout is the half the advisory was missing.
//
// Telling the WRITER to coordinate left the lease whose file moved as the only party never
// informed, and it is the one holding a now-stale read. The record is what lets it find out by
// asking rather than by being told.
func TestGradeLeasedWriteRecordsWhatItAdvisedAbout(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)
	owned := filepath.Join(root, "internal/ledger/store.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(owned), 0o755))
	require.NoError(t, os.WriteFile(owned, []byte("package ledger // edited by hand\n"), 0o644))

	got := gradeLeasedWrite(ctx, Dependencies{}, "", owned)
	require.Equal(t, "advise", got.Decision)

	store := fleetLedger(t, ctx)
	recorded := unattributedOf(t, store, "lease-a")
	require.Len(t, recorded, 1, "the owner is told what moved under it")
	assert.Equal(t, "internal/ledger/store.go", recorded[0].Path)
	assert.NotEmpty(t, recorded[0].Digest)
	assert.NotEqual(t, types.DigestAbsent, recorded[0].Digest,
		"a digest of nothing gives the owner nothing to compare against")
	assert.NotZero(t, recorded[0].At)

	// The controls. Without these the test would pass against a guard that recorded on every
	// write, which would fill the ledger with a lease's own ordinary work.
	t.Run("a lease writing its own owned path is not an intrusion", func(t *testing.T) {
		ctx, root := fleetFixture(t, fleetLeases()...)
		mine := filepath.Join(root, "internal/ledger/store.go")
		require.NoError(t, os.MkdirAll(filepath.Dir(mine), 0o755))
		require.NoError(t, os.WriteFile(mine, []byte("package ledger\n"), 0o644))

		gradeLeasedWrite(ctx, Dependencies{}, "lease-a", mine)

		assert.Empty(t, unattributedOf(t, fleetLedger(t, ctx), "lease-a"))
	})

	t.Run("unclaimed ground records nothing", func(t *testing.T) {
		ctx, root := fleetFixture(t, fleetLeases()...)
		loose := filepath.Join(root, "README.md")
		require.NoError(t, os.WriteFile(loose, []byte("# readme\n"), 0o644))

		gradeLeasedWrite(ctx, Dependencies{}, "", loose)

		store := fleetLedger(t, ctx)
		assert.Empty(t, unattributedOf(t, store, "lease-a"))
		assert.Empty(t, unattributedOf(t, store, "lease-b"))
	})
}

// TestGradeLeasedWriteRequiresACheckpoint is the rule that turns a skill into a guarantee.
//
// The instruction to checkpoint before working lived only in a skill, which an agent can skip;
// and the record it was meant to leave is missing exactly when somebody needs to recover from it.
// This is the enforcement point, and it is a deny because an advisory is the same pinky promise
// with better wording.
func TestGradeLeasedWriteRequiresACheckpoint(t *testing.T) {
	unregistered := func() []types.Job {
		fleet := fleetLeases()
		fleet[1].Registered = 0
		fleet[1].ReportedBase = ""
		fleet[1].BaseVerdict = types.BaseUnknown
		return fleet
	}

	t.Run("an unregistered lease is denied even inside its own paths", func(t *testing.T) {
		ctx, root := fleetFixture(t, unregistered()...)

		got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/diff.go"))

		require.Equal(t, "deny", got.Decision, "owning the path is not enough; the base has to be on record")
		assert.Contains(t, got.Reason, "magus vcs checkpoint", "the denial must name the base it records")
		assert.Contains(t, got.Reason, "magus job exec lease-b", "and the command that records it")
		assert.Contains(t, got.Reason, "lease-b")
	})

	t.Run("a registered lease writes its own paths freely", func(t *testing.T) {
		// The positive control. Without it this would pass against a guard that denied everything.
		ctx, root := fleetFixture(t, fleetLeases()...)

		got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/diff.go"))

		assert.Empty(t, got.Decision)
	})

	t.Run("a human is never subject to it", func(t *testing.T) {
		// An un-enrolled writer never reaches this rule: magus cannot tell "not in the fleet" from
		// "in it and not saying so", and blocking a person in their own checkout is the one
		// failure the guard must not have.
		ctx, root := fleetFixture(t, unregistered()...)

		got := gradeLeasedWrite(ctx, Dependencies{}, "", filepath.Join(root, "cmd/magus/diff.go"))

		assert.NotEqual(t, "deny", got.Decision)
	})
}

// A worker that registered on a base other than the one it was handed is ADVISED, not blocked: an
// orchestrator may have rebased the plan deliberately, and magus cannot tell that from a worker
// that wandered. What it refuses is letting the divergence stay silent until the merge finds it.
func TestGradeLeasedWriteFlagsADivergedBase(t *testing.T) {
	fleet := fleetLeases()
	fleet[1].ReportedBase = "rev-somewhere-else"
	fleet[1].BaseVerdict = types.BaseDiverged
	ctx, root := fleetFixture(t, fleet...)

	got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/diff.go"))

	require.Equal(t, "advise", got.Decision, "a deliberate rebase must not be blocked")
	assert.Contains(t, got.Context, "rev-somewhere-else")
	assert.Contains(t, got.Context, "rev-a", "the advisory names both bases so the reader can tell which moved")
}

// TestAdviseUnleasedWorker is the teaching case for a fleet running unrecorded: a process
// that claims a spawner, names no lease, and writes into a workspace whose ledger holds no
// live row to grade it against. Nothing records who owns which paths, so a collision is
// invisible until somebody reads the diff.
func TestAdviseUnleasedWorker(t *testing.T) {
	// A well-formed W3C traceparent: version, trace id, parent span id, flags.
	const spawned = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	t.Run("spawn ancestry and no lease advises", func(t *testing.T) {
		ctx, root := fleetFixture(t)
		t.Setenv(trail.EnvTraceparent, spawned)
		got := gradeLeasedWrite(ctx, Dependencies{}, "", filepath.Join(root, "internal/thing/x.go"))

		require.Equal(t, writeGrade{Decision: "advise", Context: got.Context, Kind: advisoryUnleasedWrite}, got,
			"the spawn chain is a claim, so it may teach and may never block; a standing fact, so it is held to one firing per session")
		assert.Contains(t, got.Context, `client tool (magus\job.put)`, "the advisory must name the tool that declares the plan")
		assert.Contains(t, got.Context, envHookLease, "and the channel a worker enrolls over")
	})

	t.Run("no ancestry stays silent", func(t *testing.T) {
		ctx, root := fleetFixture(t)
		t.Setenv(trail.EnvTraceparent, "")
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "", filepath.Join(root, "internal/thing/x.go")).Decision,
			"a run carrying no trace context IS a person, and a person editing their own checkout is owed silence")
	})

	t.Run("a malformed claim stays silent", func(t *testing.T) {
		// Dropped rather than salvaged, which is trail.SpawnFromEnv's contract. A value that
		// does not parse claims nothing, so there is no worker here to teach.
		ctx, root := fleetFixture(t)
		t.Setenv(trail.EnvTraceparent, "not-a-traceparent")
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "", filepath.Join(root, "internal/thing/x.go")).Decision)
	})

	t.Run("an enrolled worker stays silent", func(t *testing.T) {
		ctx, root := fleetFixture(t)
		t.Setenv(trail.EnvTraceparent, spawned)
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "lease-a", filepath.Join(root, "internal/thing/x.go")).Decision,
			"naming a lease is the whole thing being asked for")
	})

	t.Run("a live ledger grades instead", func(t *testing.T) {
		// The rule fills a silence and never competes: with live rows on record the existing
		// grading answers, and this advisory is not reached at all.
		ctx, root := fleetFixture(t, fleetLeases()...)
		t.Setenv(trail.EnvTraceparent, spawned)
		got := gradeLeasedWrite(ctx, Dependencies{}, "", filepath.Join(root, "internal/ledger/store.go"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "lease-a", "the collision report is the more specific answer")
	})
}

// magusTreeFixture makes the working directory look like a checkout of magus's own
// sources, which is the gate the two rules below are scoped by.
func magusTreeFixture(t *testing.T) string {
	t.Helper()
	root := inWorkspace(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cmd", "magus"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755))
	return root
}

// TestAdviseAgentSourceWrite: an edit to what agents are TAUGHT routes through the method
// that maintains it, because both ways to get it wrong here are silent.
func TestAdviseAgentSourceWrite(t *testing.T) {
	magusTreeFixture(t)

	for _, rel := range []string{
		"internal/agent/skills/magus-run/SKILL.md",
		"internal/handler/mcp/registry.go",
		"internal/hint/mcp_tool.go",
		"internal/hint/cli_command.go",
	} {
		got := adviseAgentSourceWrite(rel)
		assert.Contains(t, got, "magus-skill-authoring", rel)
		assert.Contains(t, got, "SkillVersion", rel)
	}

	assert.Empty(t, adviseAgentSourceWrite("internal/handler/mcp/diff.go"), "one handler is not the registry")
	assert.Empty(t, adviseAgentSourceWrite("internal/agent/catalog.go"))
	assert.Empty(t, adviseAgentSourceWrite("cmd/magus/agent.go"))
}

// TestAdviseDescriptorWrite: the generator INPUT, not the generated output. The first is
// the omitted edit, the second is the wasted one, and adviseGeneratedWrite already has the
// second.
func TestAdviseDescriptorWrite(t *testing.T) {
	magusTreeFixture(t)

	for _, rel := range []string{"proto/magus/v1/run.proto", "std/fs.go"} {
		got := adviseDescriptorWrite(rel)
		assert.Contains(t, got, "SAME commit", rel)
		assert.Contains(t, got, "magus run generate .", rel)
	}

	assert.Empty(t, adviseDescriptorWrite("std/fs_test.go"), "a test beside a descriptor feeds no generator")
	assert.Empty(t, adviseDescriptorWrite("std/http/client.go"), "a subdirectory is a module's implementation, not its API")
	assert.Empty(t, adviseDescriptorWrite("proto/README.md"))
	assert.Empty(t, adviseDescriptorWrite("internal/cache/cache.go"))
}

// Both rules name paths and a target belonging to magus's OWN checkout, which a shipped
// verdict may not normally do. The gate is what makes that legitimate, so it is the part
// worth pinning: in anybody else's workspace neither rule can fire at all.
func TestMagusOwnSourceTreeGatesTheRepoScopedRules(t *testing.T) {
	inWorkspace(t) // an ordinary workspace: no magusfile, no cmd/magus
	assert.Empty(t, adviseAgentSourceWrite("internal/agent/skills/magus-run/SKILL.md"))
	assert.Empty(t, adviseDescriptorWrite("std/fs.go"))
	assert.Empty(t, adviseDescriptorWrite("proto/magus/v1/run.proto"))
}

// The host sends an ABSOLUTE path, and this repository is routinely checked out under
// .claude/worktrees/<name>. Matching the absolute form is how adviseNewSourceDir once
// shipped inert with a green suite; both rules here resolve relative first for that reason.
func TestRepoScopedRulesHandleTheAbsolutePathTheHostSends(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, ".claude", "worktrees", "feature-x")
	require.NoError(t, os.MkdirAll(ws, 0o755))
	t.Chdir(ws)
	require.NoError(t, os.WriteFile(filepath.Join(ws, "magusfile.buzz"), nil, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "cmd", "magus"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "internal", "agent"), 0o755))

	assert.Contains(t, adviseAgentSourceWrite(filepath.Join(ws, "internal", "agent", "skills", "magus-run", "SKILL.md")), "magus-skill-authoring")
	assert.Contains(t, adviseDescriptorWrite(filepath.Join(ws, "std", "fs.go")), "SAME commit")
	assert.Empty(t, adviseDescriptorWrite(filepath.Join(root, "elsewhere", "std", "fs.go")), "outside the workspace is not this workspace's business")
}

// wideningForms are the spellings of a command that widens a job's write paths. A
// refusal served to the worker it refused is a command that worker runs, so one naming a
// widening hands the worker the escalation the boundary exists to withhold. An entry
// (`enter`) admits one write into another job's paths and widens nothing, so it may stay.
var wideningForms = []string{"--add-write-path", "job apply", `"write_paths"`, "write_paths="}

// A write outside the lease names the read-only view of what the job holds and who to
// ask, and no command that would widen it.
func TestGradeLeasedWriteServesNoWidening(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)

	for name, rel := range map[string]string{
		"a path outside the write paths":     "internal/thing/new.go",
		"a path another live lease owns":     "internal/ledger/store.go",
		"a path the row's deny list refuses": "cmd/magus/gen/cli_flags.go",
	} {
		got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, rel))
		require.Equal(t, "deny", got.Decision, name)
		for _, form := range wideningForms {
			assert.NotContains(t, got.Reason, form, name)
		}
	}

	outside := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/thing/new.go"))
	assert.Contains(t, outside.Reason, "`"+hint.DescribeJob.With("lease-b")+"`")
	assert.Contains(t, outside.Reason, "ask the job's owner")
}

// The sweep over every refusal this package can serve: no source file spells the CLI's
// widening flag or renders a put of write_paths, so a new refusal cannot start serving
// one either. The guard still PARSES write_paths (mcp.go), which serves nothing.
func TestNoRefusalNamesAWideningCommand(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		assert.NotRegexp(t, `add-write-path|hint\.JobEdit|clientJobPut\([^)]*(write_paths|writePathsParam)`, string(raw),
			"%s spells a widening command", f)
	}
}

// A path the orchestrator revoked is refused by name, with when it was taken, so the
// worker reads why a write that passed before is refused now.
func TestGradeLeasedWriteNamesARevokedPath(t *testing.T) {
	leases := fleetLeases()
	leases[1].WritePaths = append(leases[1].WritePaths, "internal/thing/new.go")
	ctx, root := fleetFixture(t, leases...)
	store := storeAt(ctx)
	revoked, err := store.Update(ctx, "lease-b", func(u *types.Job) {
		u.WritePaths = slices.DeleteFunc(u.WritePaths, func(p string) bool { return p == "internal/thing/new.go" })
	})
	require.NoError(t, err)
	require.Len(t, revoked.Releases, 1)

	got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/thing/new.go"))
	require.Equal(t, "deny", got.Decision)
	assert.Contains(t, got.Reason, `The orchestrator revoked "internal/thing/new.go" from lease lease-b at `+
		time.Unix(revoked.Releases[0].ReleasedAt, 0).UTC().Format(time.RFC3339))
}

// dependentFleet is a plan where "waiter" is queued behind "dep" and declares the paths dep
// has to write in. dep declares no write paths, which scopes nothing, so the only thing
// that can deny its write is another job's ownership.
func dependentFleet(depState types.JobState) []types.Job {
	registered := func(j types.Job) types.Job {
		j.Checkpoint, j.ReportedBase, j.BaseVerdict, j.Registered = "rev-a", "rev-a", types.BaseMatch, 1
		return j
	}
	return []types.Job{
		registered(types.Job{ID: "dep", Criteria: "land the store", State: depState}),
		registered(types.Job{ID: "waiter", Criteria: "build on the store", WritePaths: []string{"internal/job/**"}, DependsOn: []string{"dep"}, State: types.StateDeclared}),
		registered(types.Job{ID: "other", Criteria: "unrelated", WritePaths: []string{"docs/**"}, State: types.StateRunning}),
	}
}

func TestGradeLeasedWriteQueuedDependentDoesNotBlockItsDependency(t *testing.T) {
	ctx, root := fleetFixture(t, dependentFleet(types.StateRunning)...)
	got := gradeLeasedWrite(ctx, Dependencies{}, "dep", filepath.Join(root, "internal/job/store.go"))
	assert.Empty(t, got.Decision, got.Reason)

	stray := gradeLeasedWrite(ctx, Dependencies{}, "other", filepath.Join(root, "internal/job/store.go"))
	assert.NotContains(t, stray.Reason, "waiter", "a queued job is nobody's owner")
}

func TestGradeLeasedWriteDependentBlocksOnceItsDependencyPasses(t *testing.T) {
	for _, tt := range []struct {
		state types.JobState
		owns  bool
	}{
		{types.StateDeclared, false},
		{types.StateRunning, false},
		{types.StateExited, false},
		{types.StateFail, false},
		{types.StateNoReturn, false},
		{types.StatePass, true},
	} {
		t.Run(string(tt.state), func(t *testing.T) {
			ctx, root := fleetFixture(t, dependentFleet(tt.state)...)
			got := gradeLeasedWrite(ctx, Dependencies{}, "other", filepath.Join(root, "internal/job/verify.go"))
			if !tt.owns {
				assert.NotContains(t, got.Reason, "owned by lease waiter")
				return
			}
			require.Equal(t, "deny", got.Decision)
			assert.Contains(t, got.Reason, "owned by lease waiter")
		})
	}
}

func TestGradeLeasedWriteDeniesAnOverdueLease(t *testing.T) {
	leases := fleetLeases()
	leases[1].Deadline = time.Now().Add(-time.Minute).Unix()
	ctx, root := fleetFixture(t, leases...)

	got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/main.go"))
	require.Equal(t, "deny", got.Decision, "an overdue lease writing inside its own write paths is denied")
	assert.Contains(t, got.Reason, "lease-b")
	assert.Contains(t, got.Reason, "deadline")
	assert.Contains(t, got.Reason, time.Unix(leases[1].Deadline, 0).UTC().Format(time.RFC3339))

	leases[1].Deadline = time.Now().Add(time.Hour).Unix()
	ctx, root = fleetFixture(t, leases...)
	assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/main.go")).Decision,
		"a deadline still ahead bounds nothing yet")
}

func TestGradeLeasedWriteIgnoresAnOverdueOwner(t *testing.T) {
	leases := fleetLeases()
	leases[0].Deadline = time.Now().Add(-time.Minute).Unix()
	ctx, root := fleetFixture(t, leases...)

	got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/ledger/store.go"))
	assert.NotContains(t, got.Reason, "is owned by lease lease-a", "an overdue lease no longer holds its write paths against others")
}

func TestGradeLeasedWriteNamesHowToReleaseAnOwner(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)

	got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/ledger/store.go"))
	require.Equal(t, "deny", got.Decision)
	assert.Contains(t, got.Reason, "last updated")
	assert.Contains(t, got.Reason, "ago")
	assert.Contains(t, got.Reason, hint.JobExit.With("lease-a"))
}

// TestLeasedPathAdvisesOncePerSessionPerLease: the owned-path advisory was 52% of every
// advisory served in the 2026-09-24 audit, 8,419 servings, because it spoke on every edit.
// The writer has the fact after the first, so a session hears it once per lease; a
// second lease is a second fact, and a second session has heard nothing.
func TestLeasedPathAdvisesOncePerSessionPerLease(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	ctx, root := fleetFixture(t, fleetLeases()...)
	write := func(session, rel string) Verdict {
		return Judge(ctx, Dependencies{}, Request{Input: filepath.Join(root, rel), IsPath: true, Session: session, Host: "test-host"})
	}

	first := write("s1", "internal/ledger/store.go")
	assert.Equal(t, verdictWithRule("advise", string(advisoryLeasedPath)), unworded(first))
	assert.Contains(t, first.Context, "if you are lease lease-a")
	assert.NotContains(t, first.Context, "concurrent agent")
	assert.Contains(t, first.Context, "whoever took it may be editing this file now", "fleetLeases registers lease-a with no checkout")

	assert.Equal(t, "pass", write("s1", "internal/ledger/other.go").Decision, "the same lease, told once")
	assert.Equal(t, "pass", write("s1", "internal/ledger/store.go").Decision)

	other := write("s1", "cmd/magus/main.go")
	assert.Equal(t, string(advisoryLeasedPath), other.Rule, "a different lease is a new fact")
	assert.Contains(t, other.Context, "if you are lease lease-b")

	assert.Equal(t, string(advisoryLeasedPath), write("s2", "internal/ledger/store.go").Rule, "a new session has heard nothing")
}

// The holder of a leased path may be a person or an agent, so the advisory says where the
// job was taken, or that nobody has taken it, and never guesses which.
func TestHolderNoticeSaysWhereTheJobWasTaken(t *testing.T) {
	assert.Contains(t, holderNotice(types.Job{Registered: 1, CheckoutRoot: "/src/ana"}), "whoever took it in /src/ana")
	assert.Contains(t, holderNotice(types.Job{Registered: 1}), "whoever took it may be editing")
	assert.Contains(t, holderNotice(types.Job{}), "nobody has taken it yet")
}

// entryFleet is an orchestrator lease and a worker forked beneath it, whose holder took
// its job in the fixture's workspace so the guard reads its trail from there.
func entryFleet(root string) []types.Job {
	return []types.Job{
		{ID: "orch", WritePaths: []string{"cmd/**"}, State: types.StateRunning, Registered: 1},
		{ID: "orch/worker", Parent: "orch", WritePaths: []string{"internal/ledger/**"}, State: types.StateRunning, Registered: 1, CheckoutRoot: root},
	}
}

// holderCalled plants a tool call the guard graded under lease, ago before now.
func holderCalled(t *testing.T, ctx context.Context, lease string, ago time.Duration) {
	t.Helper()
	trail.Append(ctx, hookLocation(ctx, Dependencies{}).cacheDir, trail.Event{
		Ts: time.Now().Add(-ago).UnixMilli(), Kind: trail.KindAgentCommand, Action: "Edit", Lease: lease, Outcome: trail.OutcomeOK,
	})
}

// TestEnterAdmitsAnOrchestratorIntoALiveJobsWritePath pins the entry: denied without one,
// held while the holder works, one write through once it is idle, and the next write
// denied again.
func TestEnterAdmitsAnOrchestratorIntoALiveJobsWritePath(t *testing.T) {
	ctx, root := fleetFixture(t)
	store := storeAt(ctx)
	for _, row := range entryFleet(root) {
		_, err := store.Update(ctx, row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	target := filepath.Join(root, "internal/ledger/store.go")

	denied := gradeLeasedWrite(ctx, Dependencies{}, "orch", target)
	require.Equal(t, "deny", denied.Decision)
	assert.Contains(t, denied.Reason, `magus\job.put("orch/worker", opts: {"enter": "internal/ledger/store.go"})`)

	_, err := store.Enter(ctx, "orch/worker", "internal/ledger/store.go")
	require.NoError(t, err)

	holderCalled(t, ctx, "orch/worker", 5*time.Second)
	busy := gradeLeasedWrite(ctx, Dependencies{}, "orch", target)
	require.Equal(t, "deny", busy.Decision, "the holder is mid-turn")
	assert.Contains(t, busy.Reason, "idle")

	require.NoError(t, os.RemoveAll(filepath.Join(hookLocation(ctx, Dependencies{}).cacheDir, "activity")))
	holderCalled(t, ctx, "orch/worker", 2*time.Minute)
	assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "orch", target).Decision, "one write through")

	rows, err := store.List()
	require.NoError(t, err)
	entries := job.Entries(rows, "orch/worker")
	require.Len(t, entries, 1)
	assert.NotZero(t, entries[0].Consumed)
	assert.Equal(t, "deny", gradeLeasedWrite(ctx, Dependencies{}, "orch", target).Decision, "the entry is single-use")

	t.Run("the holder is told to re-read", func(t *testing.T) {
		got := gradeLeasedWrite(ctx, Dependencies{}, "orch/worker", target)
		assert.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "re-read internal/ledger/store.go")
		assert.NotEmpty(t, got.Key)
	})

	t.Run("an unattributed writer consumes an entry without the advisory", func(t *testing.T) {
		other := filepath.Join(root, "internal/ledger/other.go")
		_, err := store.Enter(ctx, "orch/worker", "internal/ledger/other.go")
		require.NoError(t, err)
		assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "", other).Decision)
		assert.Equal(t, advisoryLeasedPath, gradeLeasedWrite(ctx, Dependencies{}, "", other).Kind)
	})
}

// TestEnterRefuses pins what the store will not record as an entry.
func TestEnterRefuses(t *testing.T) {
	ctx, root := fleetFixture(t)
	store := storeAt(ctx)
	for _, row := range append(entryFleet(root), types.Job{ID: "done", WritePaths: []string{"docs/**"}, State: types.StatePass}) {
		_, err := store.Update(ctx, row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}

	for name, tc := range map[string]struct{ id, rel, want string }{
		"no such job":            {"nobody", "internal/ledger/a.go", "nothing to enter"},
		"an ended job":           {"done", "docs/a.md", "free to write"},
		"outside write paths":    {"orch/worker", "cmd/main.go", "widened into it"},
		"not workspace-relative": {"orch/worker", "../elsewhere.go", "workspace-relative"},
	} {
		_, err := store.Enter(ctx, tc.id, tc.rel)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.want, name)
	}

	_, err := store.Enter(ctx, "orch/worker", "internal/ledger/a.go")
	require.NoError(t, err)
	_, err = store.Enter(ctx, "orch/worker", "internal/ledger/a.go")
	require.ErrorContains(t, err, "not yet written", "an open entry is not entered twice")
	_, err = store.Enter(ctx, "orch/worker", "internal/ledger/b.go")
	require.NoError(t, err)
	_, err = store.Enter(ctx, "orch/worker", "internal/ledger/c.go")
	require.ErrorContains(t, err, "resume its holder", "a job takes two")

	rows, err := store.List()
	require.NoError(t, err)
	assert.Len(t, job.Entries(rows, "orch/worker"), job.MaxJobEntries)
}

// storeAt is the job store the fixture's context pins.
func storeAt(ctx context.Context) *job.Store {
	at := hookLocation(ctx, Dependencies{})
	return job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace})
}

// vcsOffSwitchWorkspace puts the test in a workspace magus.FindRoot resolves, the way
// TestDenyNotesWrite does: a marker file magus.FindRoot's own walk recognizes, so the rule
// reaches its real decision instead of bailing out on "no workspace" and passing for the
// wrong reason.
func vcsOffSwitchWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// scratch\n"), 0o644))
	t.Chdir(root)
	return root
}

// vcsOffSwitchSpawned is a well-formed W3C traceparent, the same fixture value
// TestAdviseUnleasedWorker uses for "some tool started this process deliberately".
const vcsOffSwitchSpawned = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// TestVCSOffSwitchDeniesLeasedWrite is finding 3 of the guard-boundary audit (U8): a
// leased write that leaves the workspace's own magus.yaml with vcs.enabled: false is
// denied, since vcs.Resolve then returns no VCS at all and the guard's approval
// authority (HEAD of whatever VCS resolves) goes with it.
func TestVCSOffSwitchDeniesLeasedWrite(t *testing.T) {
	root := vcsOffSwitchWorkspace(t)
	fields := writeFields{Content: "concurrency: 4\nvcs:\n  enabled: false\n"}

	got := denyVCSOffSwitch("lease-a", filepath.Join(root, "magus.yaml"), fields)

	require.Equal(t, writeGrade{Decision: "deny", Reason: got.Reason, Rule: string(denyRuleVCSOffSwitch)}, got)
	assert.Contains(t, got.Reason, "vcs.enabled", "names the field the write is denied over")
	assert.Contains(t, got.Reason, "lease-a", "leaseActorClause names the acting lease")
	assert.Contains(t, got.Reason, "orchestrator", "a leased write is told its orchestrator can do this, it cannot")
}

// TestVCSOffSwitchDeniesAgentAttributedEdit covers the other half of "leased or
// agent-attributed": no lease is named, but the process carries spawn ancestry
// (trail.SpawnFromEnv), the same claim adviseUnleasedWorker already reads elsewhere among
// the file-write rules. The edit is a replacement applied to what the file holds on disk,
// not a whole-file write, so this also proves resolvedWriteContent applies it correctly.
func TestVCSOffSwitchDeniesAgentAttributedEdit(t *testing.T) {
	root := vcsOffSwitchWorkspace(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"),
		[]byte("concurrency: 4\nvcs:\n  enabled: true\n"), 0o644))
	t.Setenv(trail.EnvTraceparent, vcsOffSwitchSpawned)
	fields := writeFields{OldText: "enabled: true", NewText: "enabled: false"}

	got := denyVCSOffSwitch("", filepath.Join(root, "magus.yaml"), fields)

	require.Equal(t, "deny", got.Decision)
	assert.Contains(t, got.Reason, "by hand", "an unleased writer is told a person makes this edit, not it")
}

// TestVCSOffSwitchDeniesUserTierFile covers the other tier config.Load reads:
// $XDG_CONFIG_HOME/magus/magus.yaml is in effect in every workspace on the machine
// (internal/config/load.go tier 2), and a workspace magus.yaml that never mentions vcs
// still inherits whatever it sets, so a write there is in scope exactly like the repo's
// own file.
func TestVCSOffSwitchDeniesUserTierFile(t *testing.T) {
	root := vcsOffSwitchWorkspace(t)
	udc := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", udc)
	userConfig := filepath.Join(udc, "magus", "magus.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(userConfig), 0o755))
	fields := writeFields{Content: "vcs:\n  enabled: false\n"}

	got := denyVCSOffSwitch("lease-a", userConfig, fields)

	require.Equal(t, "deny", got.Decision, "root is %q", root)
}

// TestVCSOffSwitchPassesEverythingElse is the negative space: every case this rule must
// leave exactly as it was before it existed.
func TestVCSOffSwitchPassesEverythingElse(t *testing.T) {
	t.Run("unleased and unattributed is a person in their own checkout", func(t *testing.T) {
		root := vcsOffSwitchWorkspace(t)
		t.Setenv(trail.EnvTraceparent, "")
		fields := writeFields{Content: "vcs:\n  enabled: false\n"}
		assert.Empty(t, denyVCSOffSwitch("", filepath.Join(root, "magus.yaml"), fields).Decision,
			"a run carrying no lease and no trace context IS a person, owed silence like every other rule here")
	})

	t.Run("vcs.enabled left true passes", func(t *testing.T) {
		root := vcsOffSwitchWorkspace(t)
		fields := writeFields{Content: "vcs:\n  enabled: true\n"}
		assert.Empty(t, denyVCSOffSwitch("lease-a", filepath.Join(root, "magus.yaml"), fields).Decision)
	})

	t.Run("vcs.enabled unset passes", func(t *testing.T) {
		root := vcsOffSwitchWorkspace(t)
		fields := writeFields{Content: "concurrency: 8\n"}
		assert.Empty(t, denyVCSOffSwitch("lease-a", filepath.Join(root, "magus.yaml"), fields).Decision)
	})

	t.Run("a path that is not a magus.yaml tier passes, whatever it says", func(t *testing.T) {
		root := vcsOffSwitchWorkspace(t)
		fields := writeFields{Content: "vcs:\n  enabled: false\n"}
		assert.Empty(t, denyVCSOffSwitch("lease-a", filepath.Join(root, "internal", "config.go"), fields).Decision)
	})

	t.Run("decided by parsing, not by grepping: the text lives under an unrelated key", func(t *testing.T) {
		root := vcsOffSwitchWorkspace(t)
		fields := writeFields{Content: "notes: \"remember to set vcs:\\n  enabled: false somewhere\"\n"}
		assert.Empty(t, denyVCSOffSwitch("lease-a", filepath.Join(root, "magus.yaml"), fields).Decision,
			"the literal text appears in the document but never under the vcs key, so a parse finds nothing to fire on")
	})

	t.Run("malformed YAML fails open", func(t *testing.T) {
		root := vcsOffSwitchWorkspace(t)
		fields := writeFields{Content: "vcs: [not a mapping\n"}
		assert.Empty(t, denyVCSOffSwitch("lease-a", filepath.Join(root, "magus.yaml"), fields).Decision,
			"a document this rule cannot parse must not be guessed at")
	})

	t.Run("an edit whose OldText is not on disk fails open", func(t *testing.T) {
		root := vcsOffSwitchWorkspace(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("concurrency: 4\n"), 0o644))
		fields := writeFields{OldText: "enabled: true", NewText: "enabled: false"}
		assert.Empty(t, denyVCSOffSwitch("lease-a", filepath.Join(root, "magus.yaml"), fields).Decision,
			"a replacement that would not apply is one the host itself refuses; nothing here guesses at the result")
	})
}

// A worker isolated in a worktree nested in the session's checkout has its hooks resolve the
// session's checkout, while its lease names files in the worktree it took the lease in. The
// same relative path in the session's checkout is another file, and a scratch directory
// holding only a go.mod is no checkout at all.
func TestGradeLeasedWriteInTheLeasesOwnCheckout(t *testing.T) {
	ctx, outer := fleetFixture(t)
	wt := filepath.Join(outer, ".claude", "worktrees", "w")
	for _, dir := range []string{outer, wt} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "std"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
	}
	_, err := storeAt(ctx).Update(ctx, "w", func(cur *types.Job) {
		*cur = types.Job{ID: "w", WritePaths: []string{"std/*.go"}, State: types.StateRunning,
			Checkpoint: "rev-a", ReportedBase: "rev-a", BaseVerdict: types.BaseMatch, Registered: 1, CheckoutRoot: wt}
	})
	require.NoError(t, err)
	scratch := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(scratch, "go.mod"), []byte("module repro\n"), 0o644))

	assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "w", filepath.Join(wt, "std", "module.go")).Decision)

	other := gradeLeasedWrite(ctx, Dependencies{}, "w", filepath.Join(outer, "std", "module.go"))
	assert.Equal(t, "deny", other.Decision, "the same relative path in another checkout is not the file handed out")
	assert.Contains(t, other.Reason, "another checkout")

	assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "w", filepath.Join(scratch, "main.go")).Decision,
		"a go.mod alone makes no magus checkout")
}
