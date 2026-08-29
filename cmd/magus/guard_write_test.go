package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/internal/trail"
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

	// Not a skill file, not in a skill directory, and not there at all.
	assert.Empty(t, adviseInstalledSkillWrite(write(".claude/skills/magus-run/README.md", "source: magus")))
	assert.Empty(t, adviseInstalledSkillWrite(write("docs/SKILL.md", "source: magus")))
	assert.Empty(t, adviseInstalledSkillWrite(filepath.Join(dir, ".claude", "skills", "magus-vcs-hygiene", "SKILL.md")))
}

// fleetFixture stands up a workspace root and a lease ledger holding leases, and
// returns the context pinning both plus the root. Everything lands in temporary
// directories, so a guard test never reads or writes the checkout's real ledger.
func fleetFixture(t *testing.T, leases ...types.Lease) (context.Context, string) {
	t.Helper()
	root, cacheDir := t.TempDir(), t.TempDir()
	store := ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root})
	for _, u := range leases {
		_, err := store.Put(t.Context(), u)
		require.NoError(t, err)
	}
	location := hookActivityLocation{base: cacheDir, workspace: root}
	return context.WithValue(t.Context(), hookActivityLocationKey{}, location), root
}

// fleetLeases is the two-lease plan most cases below grade against: two live workers with
// disjoint owned paths, one of them declaring a forbidden subtree inside its own.
//
// Both are REGISTERED, because these cases are about boundaries and an unregistered lease is
// denied before any boundary is consulted. TestGradeLeasedWriteRequiresACheckpoint covers that
// rule on its own.
func fleetLeases() []types.Lease {
	return []types.Lease{
		{
			ID:           "lease-a",
			Goal:         "own the ledger store\nacceptance: List stays cheap",
			OwnedPaths:   []string{"internal/ledger/**"},
			State:        types.StateRunning,
			Checkpoint:   "rev-a",
			ReportedBase: "rev-a",
			BaseVerdict:  types.BaseMatch,
			Registered:   1,
		},
		{
			ID:             "lease-b",
			Goal:           "grade writes in the guard",
			OwnedPaths:     []string{"cmd/magus/**", "docs/guard.md"},
			ForbiddenPaths: []string{"cmd/magus/gen/**"},
			State:          types.StateDeclared,
			Checkpoint:     "rev-a",
			ReportedBase:   "rev-a",
			BaseVerdict:    types.BaseMatch,
			Registered:     1,
		},
	}
}

// TestGradeLeasedWriteDenies pins the two denials and the fact each one must carry:
// the owning lease's id, its goal's first line, and a next step. A denial that only says
// no sends the agent around the guard, which is the failure the whole ledger design is
// built to avoid.
func TestGradeLeasedWriteDenies(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)

	t.Run("inside another live lease's owned paths", func(t *testing.T) {
		got := gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "internal/ledger/store.go"))
		require.Equal(t, "deny", got.Decision)
		assert.Contains(t, got.Reason, "lease-a", "the denial must name the owner")
		assert.Contains(t, got.Reason, "own the ledger store", "the denial must carry the owner's goal")
		assert.NotContains(t, got.Reason, "acceptance:",
			"only the goal's FIRST line belongs in a denial; the criteria block would bury the next step")
		assert.Contains(t, got.Reason, "re-partition", "the denial must name a next step")
		assert.Contains(t, got.Reason, "lease-b", "the denial must say who magus thinks is writing")
	})

	t.Run("inside the acting lease's own forbidden paths", func(t *testing.T) {
		// Also pins the precedence: cmd/magus/gen is inside lease-b's owned tree AND on its
		// forbidden list, and the more specific declaration is the one that decides.
		got := gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "cmd/magus/gen/cli_flags.go"))
		require.Equal(t, "deny", got.Decision)
		assert.Contains(t, got.Reason, "FORBIDDEN")
		assert.Contains(t, got.Reason, "lease-b")
		assert.Contains(t, got.Reason, "cmd/magus/gen/**", "the denial must quote the declaration it matched")
	})
}

// TestGradeLeasedWritePasses covers the silences. Each is a case where the guard has
// no opinion, which is different from clearing the write: a later rule still gets to
// speak, and the empty Decision is what leaves room for it.
func TestGradeLeasedWritePasses(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)

	t.Run("inside the acting lease's own owned paths", func(t *testing.T) {
		assert.Empty(t, gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "cmd/magus/agent.go")).Decision)
	})

	t.Run("ground no live lease claims", func(t *testing.T) {
		// An orchestrator's owned set is a plan, not a census. Denying here would block a
		// lease from a file nobody is competing for.
		assert.Empty(t, gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "README.md")).Decision)
	})

	t.Run("outside the workspace", func(t *testing.T) {
		assert.Empty(t, gradeLeasedWrite(ctx, "lease-b", filepath.Join(t.TempDir(), "elsewhere.go")).Decision)
	})

	t.Run("un-enrolled on ground no lease claims", func(t *testing.T) {
		assert.Empty(t, gradeLeasedWrite(ctx, "", filepath.Join(root, "README.md")).Decision)
	})
}

// TestGradeLeasedWriteIdleFleet is the zero-cost contract: with nothing to grade
// against, the guard reads the ledger and then says nothing, whatever the path.
func TestGradeLeasedWriteIdleFleet(t *testing.T) {
	t.Run("no ledger at all", func(t *testing.T) {
		ctx, root := fleetFixture(t)
		assert.Empty(t, gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "internal/ledger/store.go")).Decision)
	})

	t.Run("every lease terminal", func(t *testing.T) {
		leases := fleetLeases()
		leases[0].State, leases[1].State = types.StatePass, types.StateNoReturn
		ctx, root := fleetFixture(t, leases...)
		// A finished lease has stopped competing for its paths, which is the rule
		// types.leaseOverlaps applies when it decides which pairs to report.
		assert.Empty(t, gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "internal/ledger/store.go")).Decision)
	})

	t.Run("no state recorded", func(t *testing.T) {
		leases := fleetLeases()
		leases[0].State, leases[1].State = "", ""
		ctx, root := fleetFixture(t, leases...)
		assert.Empty(t, gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "internal/ledger/store.go")).Decision)
	})

	t.Run("no trail location", func(t *testing.T) {
		// Pinned to an EMPTY location rather than left unpinned: an unpinned context sends
		// hookActivityTrail up from the CWD to this checkout's real cache dir, and the test
		// would then grade against whatever plan the developer is actually running.
		ctx := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{})
		assert.Empty(t, gradeLeasedWrite(ctx, "lease-b", "internal/ledger/store.go").Decision)
	})
}

// TestGradeLeasedWriteUnenrolled is the doctrine case: a writer magus cannot attribute
// is told what it is walking into and is never stopped. magus cannot tell "not part of the
// fleet" from "part of it and not saying so", and blocking a person in their own checkout
// is the wrong way to be wrong.
func TestGradeLeasedWriteUnenrolled(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)
	got := gradeLeasedWrite(ctx, "", filepath.Join(root, "internal/ledger/store.go"))
	require.Equal(t, "advise", got.Decision)
	assert.Contains(t, got.Context, "lease-a", "the advisory must name the lease already working there")
	assert.Contains(t, got.Context, "own the ledger store")
	assert.Contains(t, got.Context, "magus.lease", "the advisory must say how to enroll")
	assert.Contains(t, got.Context, "seatbelt", "the advisory must say why it is not a block")
}

// TestGradeLeasedWriteInvalidLeaseID pins the treated-as-absent contract. A typo'd id
// must not silently buy un-enrolled treatment: erroring would block the tool call over
// metadata, so the notice is the whole signal the writer gets.
func TestGradeLeasedWriteInvalidLeaseID(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)

	t.Run("on unclaimed ground the notice stands alone", func(t *testing.T) {
		got := gradeLeasedWrite(ctx, "lease b!", filepath.Join(root, "README.md"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "not a valid lease id")
		assert.Contains(t, got.Context, "magus.lease")
	})

	t.Run("over-long ids are invalid too", func(t *testing.T) {
		got := gradeLeasedWrite(ctx, strings.Repeat("u", types.MaxLeaseIDLen+1), filepath.Join(root, "README.md"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "not a valid lease id")
	})

	t.Run("on owned ground it advises rather than denying", func(t *testing.T) {
		// The id is unusable, so the write is graded as un-enrolled - and an un-enrolled
		// write is never denied, even on another lease's ground.
		got := gradeLeasedWrite(ctx, "lease b!", filepath.Join(root, "internal/ledger/store.go"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "not a valid lease id")
		assert.Contains(t, got.Context, "lease-a")
	})

	t.Run("a valid id nobody declared is un-enrolled, not denied", func(t *testing.T) {
		got := gradeLeasedWrite(ctx, "lease-z", filepath.Join(root, "internal/ledger/store.go"))
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
	location := ctx.Value(hookActivityLocationKey{}).(hookActivityLocation)
	require.NoError(t, os.WriteFile(filepath.Join(location.base, "ledger", "leases.json"), []byte("{not json"), 0o644))

	got := gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "internal/ledger/store.go"))
	assert.NotEqual(t, "deny", got.Decision, "a ledger magus cannot read must never block an edit")
	require.Equal(t, "advise", got.Decision)
	assert.Contains(t, got.Context, "could not be read")
	assert.Contains(t, got.Context, "magus_ledger", "the advisory must name the surface that re-declares the plan")
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
			_, got := declarationCovering([]string{tt.decl}, tt.rel)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("returns the declaration it matched, verbatim", func(t *testing.T) {
		decl, ok := declarationCovering([]string{"docs/**", "internal/ledger/**"}, "internal/ledger/store.go")
		require.True(t, ok)
		assert.Equal(t, "internal/ledger/**", decl, "a denial quotes the declaration as the orchestrator wrote it")
	})
}

// The lease-id shape itself is pinned in internal/trail's TestValidLeaseID; the guard's
// treated-as-absent behavior for a bad id is pinned by TestGradeLeasedWriteInvalidLeaseID.

// TestHookCmdGradesAgainstTheLedger drives the wire contract rather than the grader: the
// flag reaches the rule, a denial exits with the blocking status, and the flag outranks
// the environment.
func TestHookCmdGradesAgainstTheLedger(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)
	run := func(stdin string, args ...string) (string, error) {
		global = globalFlags{}
		var out strings.Builder
		err := hookCmd(ctx, strings.NewReader(stdin), &out, args)
		return out.String(), err
	}
	owned := filepath.Join(root, "internal/ledger/store.go")

	t.Run("--lease denies a write into another lease's paths", func(t *testing.T) {
		got, err := run(owned, "--path", "--lease", "lease-b", "-o", "name")
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.exitCode)
		assert.Equal(t, "deny\n", got)
	})

	t.Run("the baggage member supplies the default", func(t *testing.T) {
		t.Setenv(trail.EnvBaggage, "userId=alice,"+trail.BaggageLease+"=lease-b")
		_, err := run(owned, "--path", "-o", "name")
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.exitCode)
	})

	// Baggage a magus member does not appear in is a fleet nobody enrolled: another tenant's
	// members must never be read as a lease.
	t.Run("baggage carrying no magus member enrolls nobody", func(t *testing.T) {
		t.Setenv(trail.EnvBaggage, "userId=alice,serverNode=DF28")
		_, err := run(owned, "--path", "-o", "name")
		require.NoError(t, err, "an un-enrolled writer is advised, never blocked")
	})

	t.Run("the flag wins over the environment", func(t *testing.T) {
		// The path belongs to lease-a. Acting AS lease-a it is the writer's own ground,
		// so a pass here proves the flag replaced the environment's lease-b rather than
		// joining it.
		t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=lease-b")
		_, err := run(owned, "--path", "--lease", "lease-a", "-o", "name")
		require.NoError(t, err, "acting as the owner must not be denied")
	})
}

// TestAdviseMemoryWrite pins the nudge to the two cross-host instruction files
// and to a capture-not-replication wording: it must name the journal WITHOUT
// telling the reader not to write the file, since host instructions belong
// exactly where they are being written.
func TestAdviseMemoryWrite(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"AGENTS.md", "CLAUDE.md", "claude.md", "/repo/nested/AGENTS.md", "  AGENTS.md  "} {
		advice := adviseMemoryWrite(path)
		require.NotEmpty(t, advice, "expected a memory advisory for %q", path)
		assert.Contains(t, advice, "magus memory put", "the advisory must name the command it is routing to")
	}
	for _, path := range []string{"", "README.md", "MAGUS.md", "docs/agents.md.tmpl", "agents.mdx"} {
		assert.Empty(t, adviseMemoryWrite(path), "no advisory belongs on %q", path)
	}
}

// TestDenyNotesWrite covers the only deny on the path surface. The negative cases matter
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

	saved := globalCfg.Knowledge.Notes.Shared
	t.Cleanup(func() { globalCfg.Knowledge.Notes.Shared = saved })

	// Nothing declared: the feature is off, so nothing is judged and nothing is guessed.
	globalCfg.Knowledge.Notes.Shared = ""
	for _, path := range []string{"notes/a.md", filepath.Join(root, "notes", "a.md"), "internal/foo.go"} {
		assert.Empty(t, denyNotesWrite(path),
			"with no declared store, %q must pass - a deny fired on a guess blocks work in a workspace that never opted in", path)
	}

	globalCfg.Knowledge.Notes.Shared = "notes"
	// The store must exist to be defended - see TestDenyNotesWriteRequiresTheStoreToExist.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "notes", "nested"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "team", "notes"), 0o755))

	for _, path := range []string{"notes/a.md", "./notes/a.md", filepath.Join(root, "notes", "a.md"), "  notes/nested/b.md  "} {
		reason := denyNotesWrite(path)
		require.NotEmpty(t, reason, "expected a deny for %q", path)
		assert.Contains(t, reason, "NOTES store", "the reason names what was blocked")
		assert.Contains(t, reason, "magus memory put", "and routes the agent somewhere it MAY write")
		assert.Contains(t, reason, "magus notes edit", "and says how a person writes it instead")
	}

	// A path outside the declared store is untouched, including one that merely looks
	// like it (notes-archive shares the prefix but is a different directory).
	for _, path := range []string{"internal/foo.go", "docs/notes.md", "notes-archive/a.md", "../outside/a.md"} {
		assert.Empty(t, denyNotesWrite(path), "%q is not in the declared store", path)
	}

	// The exclusion follows the declaration, not the name.
	globalCfg.Knowledge.Notes.Shared = "team/notes"
	assert.Empty(t, denyNotesWrite("notes/a.md"), "a different directory named notes is not the store")
	assert.NotEmpty(t, denyNotesWrite("team/notes/a.md"))
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

	saved := globalCfg.Knowledge.Notes.Shared
	t.Cleanup(func() { globalCfg.Knowledge.Notes.Shared = saved })
	globalCfg.Knowledge.Notes.Shared = "notes"

	// No magus.yaml here, so the declaration can only have come from elsewhere on the
	// machine. This repo never opted in.
	assert.Empty(t, denyNotesWrite("notes/a.md"),
		"a declaration this repo did not make must not deny writes in it")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "notes"), 0o755))
	assert.NotEmpty(t, denyNotesWrite("notes/a.md"),
		"a store that exists on disk is defended whoever declared it")
}

// TestDenyNotesWriteDefendsAnEmptyDeclaredStore is the regression guard for the hole that
// dogfooding found on 2026-08-13: with the key declared and no note yet written, a direct
// file write to notes/<name>.md PASSED.
//
// The gate was "the directory exists", on the reasoning that a person creates the store by
// writing the first note, so an agent could never bring it into being. The reverse held.
// An agent could author the store's FIRST note - the single entry with nothing beside it to
// look wrong against - and the deny would switch on immediately afterwards, defending the
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

	saved := globalCfg.Knowledge.Notes.Shared
	t.Cleanup(func() { globalCfg.Knowledge.Notes.Shared = saved })
	globalCfg.Knowledge.Notes.Shared = "notes"

	require.NoDirExists(t, filepath.Join(root, "notes"), "the store has no files yet - that is the case under test")
	assert.NotEmpty(t, denyNotesWrite("notes/first.md"),
		"a repo that committed the key is defended before its first note exists, or an agent writes that first note")
	assert.NotEmpty(t, denyNotesWrite(filepath.Join(root, "notes", "nested", "deep.md")))

	// Still scoped: declaring a notes store does not defend the rest of the repo.
	assert.Empty(t, denyNotesWrite("internal/foo.go"))
}

// TestGuardDeniesAuthoringANote closes the surface the path rule cannot see. `magus notes
// edit` can take prose on stdin, which is a COMMAND rather than a file write, so without
// this an agent could author a note through a boundary that is supposed to be about who is
// writing rather than which surface they reached for.
func TestGuardDeniesAuthoringANote(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		"magus notes edit team-conventions",
		"printf 'prose' | magus notes edit team-conventions --anchor project:.",
		"cat body.md | ./magus notes edit foo",
	} {
		v := evaluateBashGuard(cmd)
		assert.NotEmpty(t, v.Deny, "expected a deny for %q", cmd)
		assert.Contains(t, v.Deny, "magus memory put", "the reason routes to the store an agent MAY write")
	}
	// Reading is untouched: the boundary is on authorship, not on access.
	for _, cmd := range []string{"magus notes ls", "magus notes get foo", "magus notes verify"} {
		assert.Empty(t, evaluateBashGuard(cmd).Deny, "%q only reads", cmd)
	}
}

// fleetLedger reopens the ledger the guard just graded against, resolved exactly the way the guard
// resolves it, so these assertions read the same store the code under test wrote.
func fleetLedger(t *testing.T, ctx context.Context) *ledger.Store {
	t.Helper()
	loc := hookActivityTrail(ctx)
	return ledger.NewStore(ledger.Location{CacheDir: loc.base, Root: loc.workspace})
}

func unattributedOf(t *testing.T, store *ledger.Store, id string) []types.LeaseUnattributedWrite {
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

	got := gradeLeasedWrite(ctx, "", owned)
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

		gradeLeasedWrite(ctx, "lease-a", mine)

		assert.Empty(t, unattributedOf(t, fleetLedger(t, ctx), "lease-a"))
	})

	t.Run("unclaimed ground records nothing", func(t *testing.T) {
		ctx, root := fleetFixture(t, fleetLeases()...)
		loose := filepath.Join(root, "README.md")
		require.NoError(t, os.WriteFile(loose, []byte("# readme\n"), 0o644))

		gradeLeasedWrite(ctx, "", loose)

		store := fleetLedger(t, ctx)
		assert.Empty(t, unattributedOf(t, store, "lease-a"))
		assert.Empty(t, unattributedOf(t, store, "lease-b"))
	})
}

// TestGradeLeasedWriteRequiresACheckpoint is the rule that turns a skill into a guarantee.
//
// The instruction to checkpoint before working lived only in a skill, which an agent can skip -
// and the record it was meant to leave is missing exactly when somebody needs to recover from it.
// This is the enforcement point, and it is a deny because an advisory is the same pinky promise
// with better wording.
func TestGradeLeasedWriteRequiresACheckpoint(t *testing.T) {
	unregistered := func() []types.Lease {
		fleet := fleetLeases()
		fleet[1].Registered = 0
		fleet[1].ReportedBase = ""
		fleet[1].BaseVerdict = types.BaseUnknown
		return fleet
	}

	t.Run("an unregistered lease is denied even inside its own paths", func(t *testing.T) {
		ctx, root := fleetFixture(t, unregistered()...)

		got := gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "cmd/magus/diff.go"))

		require.Equal(t, "deny", got.Decision, "owning the path is not enough; the base has to be on record")
		assert.Contains(t, got.Reason, "magus vcs checkpoint", "the denial must name the command")
		assert.Contains(t, got.Reason, "magus_ledger", "and where to register what it prints")
		assert.Contains(t, got.Reason, "lease-b")
	})

	t.Run("a registered lease writes its own paths freely", func(t *testing.T) {
		// The positive control. Without it this would pass against a guard that denied everything.
		ctx, root := fleetFixture(t, fleetLeases()...)

		got := gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "cmd/magus/diff.go"))

		assert.Empty(t, got.Decision)
	})

	t.Run("a human is never subject to it", func(t *testing.T) {
		// An un-enrolled writer never reaches this rule: magus cannot tell "not in the fleet" from
		// "in it and not saying so", and blocking a person in their own checkout is the one
		// failure the guard must not have.
		ctx, root := fleetFixture(t, unregistered()...)

		got := gradeLeasedWrite(ctx, "", filepath.Join(root, "cmd/magus/diff.go"))

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

	got := gradeLeasedWrite(ctx, "lease-b", filepath.Join(root, "cmd/magus/diff.go"))

	require.Equal(t, "advise", got.Decision, "a deliberate rebase must not be blocked")
	assert.Contains(t, got.Context, "rev-somewhere-else")
	assert.Contains(t, got.Context, "rev-a", "the advisory names both bases so the reader can tell which moved")
}
