package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRepoIdentityWorktree proves every worktree of one repo resolves to the same
// identity, so they share one memory directory - the reason the key is not the checkout
// path.
func TestRepoIdentityWorktree(t *testing.T) {
	main := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git"), 0o755))

	wt := t.TempDir()
	gitfile := "gitdir: " + filepath.Join(main, ".git", "worktrees", "feature-x") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte(gitfile), 0o644))

	assert.Equal(t, main, repoIdentity(main), "a plain checkout identifies as itself")
	assert.Equal(t, main, repoIdentity(wt), "a linked worktree identifies as the main repo")
	bare := t.TempDir()
	assert.Equal(t, bare, repoIdentity(bare), "no .git: the root is the identity")
}

func TestDirIsOutsideRepoAndStable(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := t.TempDir()

	dir, err := Dir(root)
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(dir))
	assert.Contains(t, dir, filepath.Join(state, "magus", "memory"))
	assert.NotContains(t, dir, root, "memory must not live under the repo")
	assert.Contains(t, filepath.Base(dir), filepath.Base(root)+"-", "dir name leads with the repo basename")

	again, err := Dir(root)
	require.NoError(t, err)
	assert.Equal(t, dir, again, "the key is deterministic")
}

// testRoot isolates the store: XDG_STATE_HOME points at a temp dir, and root is an
// empty temp dir (no .git, so repoIdentity is root itself).
func testRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return t.TempDir()
}

func TestPutGetRoundTrip(t *testing.T) {
	root := testRoot(t)
	in := Record{
		Name:       "hasher-sha256-over-blake3",
		Type:       TypeDecision,
		Status:     "accepted",
		Refs:       []Ref{{Kind: RefKindNode, Target: "file:internal/hash/hasher.go"}, {Kind: RefKindOutput, Target: "ref9f3a2c1b"}},
		References: []string{"cache-key-derivation"},
		Body:       "Keep stdlib SHA256. BLAKE3 ~3.3x slower on arm64 and hashing is off the hot path.",
	}
	stored, err := Put(root, in)
	require.NoError(t, err)
	assert.NotZero(t, stored.Created)
	assert.GreaterOrEqual(t, stored.Updated, stored.Created)

	got, err := Get(root, in.Name)
	require.NoError(t, err)
	assert.Equal(t, stored, got) // whole-struct: frontmatter + body + timestamps survive the round trip
}

// TestEliminationRoundTripsItsExcerpt pins the serialization the feature rests on. Captured
// tool output arrives with interior newlines, indented lines, and can hold a line that is
// exactly the frontmatter delimiter; a YAML block scalar indents every line it carries, so
// all three survive. It cannot carry the trailing newline, which Put trims to keep the
// stored record and the returned one equal.
func TestEliminationRoundTripsItsExcerpt(t *testing.T) {
	root := testRoot(t)
	in := Record{
		Name:    "resize-bar-misreported",
		Type:    TypeElimination,
		Refs:    []Ref{{Kind: RefKindOutput, Target: "out1a2b3c"}},
		Body:    "Not the BIOS: the aperture is reported at its real size.",
		Excerpt: "BAR0: 256M\n---\n  aperture matches lspci\n",
	}
	stored, err := Put(root, in)
	require.NoError(t, err)
	assert.Equal(t, "BAR0: 256M\n---\n  aperture matches lspci", stored.Excerpt)

	got, err := Get(root, in.Name)
	require.NoError(t, err)
	assert.Equal(t, stored, got) // whole-struct: a delimiter line inside the excerpt does not truncate the frontmatter
}

func TestPutPreservesCreatedOnUpdate(t *testing.T) {
	root := testRoot(t)
	rec := Record{Name: "cache-op-surface", Type: TypePointer, Refs: []Ref{{Kind: RefKindQuery, Target: "kind:op depends cache"}}}
	first, err := Put(root, rec)
	require.NoError(t, err)

	rec.Refs = []Ref{{Kind: RefKindQuery, Target: "kind:op depends hasher"}}
	second, err := Put(root, rec)
	require.NoError(t, err)
	assert.Equal(t, first.Created, second.Created, "created time is preserved across an update")
	assert.GreaterOrEqual(t, second.Updated, first.Updated)
}

func TestListIsNameOrdered(t *testing.T) {
	root := testRoot(t)
	for _, n := range []string{"zebra", "alpha", "mid"} {
		_, err := Put(root, Record{Name: n, Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}})
		require.NoError(t, err)
	}
	got, err := List(root)
	require.NoError(t, err)
	var names []string
	for _, r := range got {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"alpha", "mid", "zebra"}, names)
}

func TestListEmptyStore(t *testing.T) {
	got, err := List(testRoot(t))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestValidateRejections(t *testing.T) {
	cases := map[string]Record{
		"bad name":        {Name: "Not Kebab", Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}},
		"unknown type":    {Name: "x", Type: "observation", Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}},
		"no refs":         {Name: "x", Type: TypePointer},
		"unknown kind":    {Name: "x", Type: TypePointer, Refs: []Ref{{Kind: "fact", Target: "t"}}},
		"empty target":    {Name: "x", Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "  "}}},
		"pointer w/prose": {Name: "x", Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}, Body: "not allowed"},
		// An elimination that cannot show its evidence is the shape the type exists to
		// refuse: a verdict a later reader can only take on faith.
		"elimination w/o excerpt": {Name: "x", Type: TypeElimination, Refs: []Ref{{Kind: RefKindOutput, Target: "out1a2b3c"}}, Body: "ruled out"},
		"elimination w/o body":    {Name: "x", Type: TypeElimination, Refs: []Ref{{Kind: RefKindOutput, Target: "out1a2b3c"}}, Excerpt: "FAIL: 0 differing lines"},
		"plan w/excerpt":          {Name: "x", Type: TypePlan, Refs: []Ref{{Kind: RefKindCommand, Target: "magus ci"}}, Body: "ship", Excerpt: "not allowed here"},
	}
	root := testRoot(t)
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, Validate(rec))
			_, err := Put(root, rec)
			assert.Error(t, err, "Put must reject an invalid record")
		})
	}
}

func TestDeleteAllowMissing(t *testing.T) {
	root := testRoot(t)
	assert.NoError(t, Delete(root, "ghost", true), "idempotent delete of an absent record is a no-op")
	assert.Error(t, Delete(root, "ghost", false), "strict delete of an absent record errors")

	_, err := Put(root, Record{Name: "real", Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}})
	require.NoError(t, err)
	require.NoError(t, Delete(root, "real", false))
	_, err = Get(root, "real")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestReadCursor(t *testing.T) {
	root := testRoot(t)
	got, err := ReadCursor(root)
	require.NoError(t, err)
	assert.Empty(t, got, "an unwritten cursor reads empty, not an error")

	dir, err := Dir(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, cursorFile), []byte("left off wiring the @memory shard"), 0o644))
	got, err = ReadCursor(root)
	require.NoError(t, err)
	assert.Equal(t, "left off wiring the @memory shard", got)
}

func TestVerifyReportsMalformedAndStaleEntries(t *testing.T) {
	root := testRoot(t)
	good, err := Put(root, Record{Name: "active-plan", Type: TypePlan, Status: "active", Refs: []Ref{{Kind: RefKindCommand, Target: "magus affected ci"}}, Body: "Finish the release gate."})
	require.NoError(t, err)
	_, err = Put(root, Record{Name: "old-plan", Type: TypePlan, Status: "stale", Refs: []Ref{{Kind: RefKindCommand, Target: "magus affected ci"}}, References: []string{good.Name}, Body: "Replace this."})
	require.NoError(t, err)
	dir, err := Dir(root)
	require.NoError(t, err)
	bad := filepath.Join(dir, recordsSubdir, "broken.md")
	require.NoError(t, os.WriteFile(bad, []byte("not frontmatter"), 0o644))

	report, err := Verify(root, allRefsResolve)
	require.NoError(t, err)
	assert.Equal(t, Verification{Records: 2, Issues: []Issue{
		{Severity: "error", Code: "invalid-entry", Path: bad, Message: "memory: broken.md: missing YAML frontmatter", Hint: "Repair or remove this file, then run `magus memory verify` again."},
		{Severity: "warning", Code: "stale-entry", Path: filepath.Join(dir, recordsSubdir, "old-plan.md"), Record: "old-plan", Message: "entry is marked stale", Hint: "Refresh it with `magus memory put` or remove it with `magus memory delete`."},
	}}, report)

	// The malformed file is a repair task, so verify keeps it at error severity. It is
	// still one entry, so the two readable ones come back: the listing is where a human
	// goes to delete the bad entry, and taking it down leaves nowhere to repair from.
	listed, err := List(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"active-plan", "old-plan"}, recordNames(listed))
}

// TestListSkipsAnEntryWrittenByANewerMagus is the forward-compat direction. The journal is
// per-repository durable data and every worktree's binary reads it, so a type introduced
// after this binary shipped must cost that one entry and nothing else. Nothing is broken,
// so it is a warning and verify stays green.
func TestListSkipsAnEntryWrittenByANewerMagus(t *testing.T) {
	root := testRoot(t)
	_, err := Put(root, Record{Name: "readable", Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}})
	require.NoError(t, err)
	dir := mustDir(t, root)
	future := filepath.Join(dir, recordsSubdir, "from-the-future.md")
	require.NoError(t, os.WriteFile(future,
		[]byte("---\nname: from-the-future\ntype: zzz-future\nrefs:\n    - kind: node\n      target: project:magus\n---\n"), 0o644))

	listed, err := List(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"readable"}, recordNames(listed))

	report, err := Verify(root, allRefsResolve)
	require.NoError(t, err)
	assert.Equal(t, Verification{Records: 1, Issues: []Issue{{
		Severity: "warning", Code: "unknown-entry-type", Path: future,
		Message: `from-the-future.md: memory: unknown record type "zzz-future" (want pointer, decision, plan, or elimination)`,
		Hint:    "A newer magus wrote this entry. Skipped here; upgrade to read it, or delete it with `magus memory delete`.",
	}}}, report)

	// Writing one is still refused: tolerance is for the read path alone.
	_, err = Put(root, Record{Name: "x", Type: "zzz-future", Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}})
	assert.ErrorIs(t, err, ErrUnknownType)
}

func recordNames(recs []Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Name
	}
	return out
}

// TestListReturnsRecordsWithOnlyWarnings proves a warning-only issue (e.g. a normal,
// documented "stale" status) does not make List discard every valid record. Before the
// fix, List short-circuited on len(issues) != 0 regardless of severity, so this returned
// (nil, nil) instead of the one well-formed record.
func TestListReturnsRecordsWithOnlyWarnings(t *testing.T) {
	root := testRoot(t)
	_, err := Put(root, Record{Name: "old-plan", Type: TypePlan, Status: "stale", Refs: []Ref{{Kind: RefKindCommand, Target: "magus affected ci"}}, Body: "Replace this."})
	require.NoError(t, err)

	got, err := List(root)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "old-plan", got[0].Name)
}

func TestVerifyReportsBrokenEntryReference(t *testing.T) {
	root := testRoot(t)
	_, err := Put(root, Record{Name: "release-plan", Type: TypePlan, Refs: []Ref{{Kind: RefKindCommand, Target: "magus affected ci"}}, References: []string{"missing-decision"}, Body: "Ship after CI."})
	require.NoError(t, err)

	report, err := Verify(root, allRefsResolve)
	require.NoError(t, err)
	assert.Equal(t, Verification{Records: 1, Issues: []Issue{{
		Severity: "error", Code: "missing-reference", Record: "release-plan",
		Path:    filepath.Join(mustDir(t, root), recordsSubdir, "release-plan.md"),
		Message: "references missing entry \"missing-decision\"",
		Hint:    "Create the referenced entry, update this entry with `magus memory put`, or delete the broken reference.",
	}}}, report)
}

// allRefsResolve is the RefResolver for the checks that are not about evidence decay.
func allRefsResolve(Ref) error { return nil }

// TestVerifyWarnsOnDecayedEvidenceAndKeepsTheExcerpt covers the property the excerpt was
// added for. An output blob lives under the checkout that produced it while this store is
// keyed by repository, so the common case is a ref minted in a worktree since removed. The
// entry degrades to a warning and keeps its evidence.
//
// The second half runs the same journal against a resolver that succeeds. Without it the
// warning could be unconditional and the assertion above would still pass.
func TestVerifyWarnsOnDecayedEvidenceAndKeepsTheExcerpt(t *testing.T) {
	root := testRoot(t)
	rec, err := Put(root, Record{
		Name:    "cache-key-drift",
		Type:    TypeElimination,
		Refs:    []Ref{{Kind: RefKindOutput, Target: "out1a2b3c"}, {Kind: RefKindCommand, Target: "magus affected ci"}},
		Body:    "Not the cache key: both runs hashed the same inputs.",
		Excerpt: "key inputs identical, 0 differing lines",
	})
	require.NoError(t, err)

	dead := func(ref Ref) error {
		if ref.Kind == RefKindOutput {
			return os.ErrNotExist
		}
		return nil
	}
	report, err := Verify(root, dead)
	require.NoError(t, err)
	assert.Equal(t, Verification{Records: 1, Issues: []Issue{{
		Severity: "warning", Code: "unresolvable-ref", Record: "cache-key-drift",
		Path:    filepath.Join(mustDir(t, root), recordsSubdir, "cache-key-drift.md"),
		Message: `evidence ref "output: out1a2b3c" no longer resolves`,
		Hint:    "An output blob lives in the checkout that produced it, so a ref minted in a removed worktree cannot be reopened anywhere. Re-run the work for a fresh ref, or copy what it showed into the entry's excerpt.",
	}}}, report)

	listed, err := List(root)
	require.NoError(t, err)
	require.Len(t, listed, 1, "a decayed ref is a warning, so the entry is still readable")
	assert.Equal(t, rec.Excerpt, listed[0].Excerpt, "the evidence outlives the ref that pointed at it")

	alive, err := Verify(root, allRefsResolve)
	require.NoError(t, err)
	assert.Empty(t, alive.Issues, "a ref that still resolves raises nothing")
}

func mustDir(t *testing.T, root string) string {
	t.Helper()
	dir, err := Dir(root)
	require.NoError(t, err)
	return dir
}
