package cache

import (
	"context"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// rewriteMain replaces the test project's source from inside a running target,
// standing in for a formatter or an autofix charm writing over its own inputs.
func rewriteMain(t *testing.T, root, body string) func(context.Context) error {
	t.Helper()
	return func(_ context.Context) error {
		writeMain(t, root, body)
		return nil
	}
}

func TestRunReportsUndeclaredSourceMutation(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main // recieve")

	_, err := c.Run(t.Context(), makeStep(root), rewriteMain(t, root, "package main // receive"))

	require.Error(t, err, "a target that rewrote its own declared source must fail")
	assert.ErrorContains(t, err, string(types.UndeclaredSourceModified))
	assert.ErrorContains(t, err, "test/pkg/main.go", "the diagnostic names the file that moved")
}

// The verdict stays short; the reason rides beside it as the failure record's why, where a
// person's display prints it and an agent's leaves it in the run log.
func TestRunReportsUndeclaredSourceMutationWithItsReason(t *testing.T) {
	root := t.TempDir()
	logs := &recordingHandler{}
	c, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), WithLocalWrite(true), WithLogger(slog.New(logs)))
	require.NoError(t, err, "cache.Open")
	writeMain(t, root, "package main // recieve")

	_, err = c.Run(t.Context(), makeStep(root), rewriteMain(t, root, "package main // receive"))
	require.Error(t, err)
	const why = "A step that rewrites its own declared sources changes its cache key as it runs; declare them, or stop writing them."
	assert.Equal(t, why, types.DiagnosticRationale(err))
	assert.NotContains(t, err.Error(), why, "the reason is never part of the error's text")

	var failure string
	for _, l := range logs.lines() {
		if strings.HasPrefix(l, "cache.error ") {
			failure = l
		}
	}
	assert.Contains(t, failure, " why="+why)
}

// permitsSuite pins that a permit reaches the result and never the cache.
type permitsSuite struct {
	suite.Suite
}

func TestPermits(t *testing.T) {
	suite.Run(t, new(permitsSuite))
}

func permitOne(ctx context.Context) error {
	journal.RecordPermits(ctx, 1)
	return nil
}

func (s *permitsSuite) TestAPermitReachesTheResult() {
	root, _, c := newMutableCache(s.T())
	writeMain(s.T(), root, "package main")
	res, err := c.Run(s.T().Context(), makeStep(root), permitOne)
	s.Require().NoError(err)
	s.Equal(1, res.Permitted)
}

func (s *permitsSuite) TestAPermitIsCountedApartFromAMiss() {
	root, _, c := newMutableCache(s.T())
	writeMain(s.T(), root, "package main")
	_, err := c.Run(s.T().Context(), makeStep(root), permitOne)
	s.Require().NoError(err)
	s.Equal(1, c.Stats().Permitted)
	s.Zero(c.Stats().Miss)
}

func (s *permitsSuite) TestAPermitIsNeverReplayed() {
	root, _, c := newMutableCache(s.T())
	writeMain(s.T(), root, "package main")
	_, err := c.Run(s.T().Context(), makeStep(root), permitOne)
	s.Require().NoError(err)
	res, err := c.Run(s.T().Context(), makeStep(root), permitOne)
	s.Require().NoError(err)
	s.False(res.Hit, "the second run judges the base again")
	s.Equal(1, res.Permitted)
}

func (s *permitsSuite) TestAPlainStepPermitsNothingAndIsReplayed() {
	root, _, c := newMutableCache(s.T())
	writeMain(s.T(), root, "package main")
	noop := func(context.Context) error { return nil }
	_, err := c.Run(s.T().Context(), makeStep(root), noop)
	s.Require().NoError(err)
	res, err := c.Run(s.T().Context(), makeStep(root), noop)
	s.Require().NoError(err)
	s.True(res.Hit)
	s.Zero(res.Permitted)
}

// The declaration is what separates a formatter from the footgun: editsExistingSource
// folds its globs into Sources, so without Step.Updates the two are indistinguishable.
func TestRunAllowsDeclaredSourceMutation(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main // recieve")

	step := makeStep(root)
	step.Updates = types.MustParseGlobs("test/pkg/*.go")

	_, err := c.Run(t.Context(), step, rewriteMain(t, root, "package main // receive"))

	require.NoError(t, err, "a declared in-place edit is the API working, not a finding")
	got, err := os.ReadFile(filepath.Join(root, "test", "pkg", "main.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main // receive", string(got), "the declared edit stands")
}

// A target that leaves its sources alone must stay silent, or every passing run in the
// workspace grows a diagnostic and the code stops meaning anything.
func TestRunQuietWhenSourcesUntouched(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")

	_, err := c.Run(t.Context(), makeStep(root), func(_ context.Context) error { return nil })

	require.NoError(t, err)
}

// Rewriting a source with byte-identical content is not a mutation anyone can observe
// through a cache key, and reporting it would train readers to ignore MGS4007.
func TestRunIgnoresIdenticalRewrite(t *testing.T) {
	root, _, c := newMutableCache(t)
	const body = "package main"
	writeMain(t, root, body)

	_, err := c.Run(t.Context(), makeStep(root), rewriteMain(t, root, body))

	require.NoError(t, err)
}

// A failing target has already said why it failed; MGS4007 must not bury that.
func TestRunSourceMutationDeferredToRunError(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main // recieve")

	boom := assert.AnError
	_, err := c.Run(t.Context(), makeStep(root), func(_ context.Context) error {
		writeMain(t, root, "package main // receive")
		return boom
	})

	require.ErrorIs(t, err, boom, "the target's own failure is what surfaces")
	assert.NotContains(t, err.Error(), string(types.UndeclaredSourceModified))
}

func TestMutatedSourcesDeletionCountsCreationDoesNot(t *testing.T) {
	before := sourceFingerprint{"a.go": "h1", "b.go": "h2"}
	after := sourceFingerprint{"b.go": "h2", "c.go": "h3"}

	assert.Equal(t, []string{"a.go"}, mutatedSources(before, after, nil, nil),
		"a deleted input is a write; a created one is MGS1028's question, not this one")
}

func TestMutatedSourcesHonoursUpdateGlobs(t *testing.T) {
	before := sourceFingerprint{"docs/a.md": "h1", "src/b.go": "h2"}
	after := sourceFingerprint{"docs/a.md": "changed", "src/b.go": "changed"}

	assert.Equal(t, []string{"src/b.go"},
		mutatedSources(before, after, types.MustParseGlobs("docs/**/*.md"), nil))
}

// TestMutatedSourcesHonoursAnUpdateAnOutputExcludes is the hand file a generator's
// output carves out and another target edits in place: the update declares the edit, and
// the output's exclusion must not cancel it.
func TestMutatedSourcesHonoursAnUpdateAnOutputExcludes(t *testing.T) {
	before := sourceFingerprint{"gen/rt.go": "h1", "gen/fs.go": "h2"}
	after := sourceFingerprint{"gen/rt.go": "edited", "gen/fs.go": "regenerated"}

	assert.Empty(t, mutatedSources(before, after,
		types.MustParseGlobs("gen/rt.go"), types.MustParseGlobs("gen/*.go", "!gen/rt.go")))
}

// ctx.needs composes targets, so a chained target's declared output is written inside
// the outer step's window. Those bytes have an owner, and blaming whichever target was
// on the outside is how `lint` got accused of generating a spell doc.
func TestMutatedSourcesIgnoresAnyTargetsDeclaredOutput(t *testing.T) {
	before := sourceFingerprint{"docs/gen-page.md": "h1", "docs/authored.md": "h2"}
	after := sourceFingerprint{"docs/gen-page.md": "regenerated", "docs/authored.md": "reformatted"}

	assert.Equal(t, []string{"docs/authored.md"},
		mutatedSources(before, after, nil, types.MustParseGlobs("docs/gen-page.md")))
}

// The cap keeps a formatter over a large tree from printing hundreds of paths, while
// still saying how many it withheld.
func TestJoinCappedNamesTheRemainder(t *testing.T) {
	assert.Equal(t, "a, b", joinCapped([]string{"a", "b"}, 5))
	assert.Equal(t, "a, b (and 2 more)", joinCapped([]string{"a", "b", "c", "d"}, 2))
}

// fingerprintSources must read the tree, not a memo: it runs on both sides of a target
// that executes, where a remembered pre-run hash is exactly the wrong answer.
func TestFingerprintSourcesSeesWritesBetweenCalls(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	step := makeStep(root)

	before, err := c.fingerprintSources(t.Context(), &step)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(
		filepath.Join(root, "test", "pkg", "main.go"), []byte("package main // edited"), 0o644))

	after, err := c.fingerprintSources(t.Context(), &step)
	require.NoError(t, err)

	assert.NotEqual(t, before["test/pkg/main.go"], after["test/pkg/main.go"])
}

// An entry is not recorded when a hashed input moved while the step ran, because the key
// names a tree that no longer exists. The run itself stays green: the work succeeded, and
// what is unsafe is only filing it under this key.
//
// The moved file is one a project DECLARES AS AN OUTPUT, which is the case that was silent.
// MGS4007 deliberately ignores those so it never accuses a target of writing a generated
// file, and that exemption was reused as the store gate, so a peer rewriting a generated
// file inside this step's hash window was claimed, ignored, and stored anyway: the old key
// mapped to output built from the new bytes, and on a shared remote every other machine
// got it.
//
// Proven by putting the tree BACK to what it was hashed at and running again. If the entry
// had been recorded, that second run would hit.
func TestRunDoesNotRecordAnEntryWhoseInputsMovedWhileItRan(t *testing.T) {
	root, c := openCache(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
	src := filepath.Join(root, "pkg", "enum.go")
	const hashedAt = "package pkg // the bytes the key was computed from\n"
	require.NoError(t, os.WriteFile(src, []byte(hashedAt), 0o644))

	step := Step{
		ProjectPath:   ".",
		WorkspaceRoot: root,
		Target:        "build",
		Sources:       types.MustParseGlobs("pkg/*.go"),
		// Claimed as a declared output, which is what silenced the old check.
		OwnedOutputs: types.MustParseGlobs("pkg/*.go"),
	}

	runs := 0
	body := func(context.Context) error {
		runs++
		// A peer rewriting a generated file this step already hashed.
		return os.WriteFile(src, []byte("package pkg // rewritten by somebody else\n"), 0o644)
	}

	_, err := c.Run(t.Context(), step, body)
	require.NoError(t, err, "the run must stay green: the step's own work succeeded")
	require.Equal(t, 1, runs)

	// Back to the bytes the key was computed from. A recorded entry would answer here.
	require.NoError(t, os.WriteFile(src, []byte(hashedAt), 0o644))
	_, err = c.Run(t.Context(), step, body)
	require.NoError(t, err)
	assert.Equal(t, 2, runs,
		"the second run replayed an entry recorded under a key whose inputs had already moved")
}

// formatterStep is a step that rewrites pkg/enum.go in place and declares it, the way
// editsExistingSource does: the update glob is also a source.
func formatterStep(t *testing.T) (root string, c *Cache, step Step) {
	t.Helper()
	root, c = openCache(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
	return root, c, Step{
		ProjectPath:   ".",
		WorkspaceRoot: root,
		Target:        "format",
		Sources:       types.MustParseGlobs("pkg/*.go"),
		Updates:       types.MustParseGlobs("pkg/*.go"),
	}
}

// Updates are never snapshotted or replayed, so an entry recorded under the pre-run key
// of a run that moved its update file is a hit that leaves the file unformatted.
// Proven by putting the file back to those pre-run bytes and running again.
func TestRunDoesNotRecordAnEntryWhoseDeclaredUpdateMoved(t *testing.T) {
	root, c, step := formatterStep(t)
	src := filepath.Join(root, "pkg", "enum.go")
	require.NoError(t, os.WriteFile(src, []byte("unformatted"), 0o644))

	runs := 0
	format := func(context.Context) error {
		runs++
		return os.WriteFile(src, []byte("formatted"), 0o644)
	}

	_, err := c.Run(t.Context(), step, format)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(src, []byte("unformatted"), 0o644))
	_, err = c.Run(t.Context(), step, format)
	require.NoError(t, err)

	assert.Equal(t, 2, runs, "a hit under the pre-run key skipped the formatter")
	got, err := os.ReadFile(src)
	require.NoError(t, err)
	assert.Equal(t, "formatted", string(got))
}

// The other half of the rule: a run that leaves its update file as it found it is a
// fixed point, and its entry is recorded.
func TestRunRecordsAnEntryWhoseDeclaredUpdateStayedPut(t *testing.T) {
	root, c, step := formatterStep(t)
	src := filepath.Join(root, "pkg", "enum.go")
	require.NoError(t, os.WriteFile(src, []byte("formatted"), 0o644))

	runs := 0
	format := func(context.Context) error {
		runs++
		return os.WriteFile(src, []byte("formatted"), 0o644)
	}

	for range 2 {
		_, err := c.Run(t.Context(), step, format)
		require.NoError(t, err)
	}

	assert.Equal(t, 1, runs, "a fixed point must cache")
}

// The predicate itself, isolated from Run's wiring.
func TestKeyStillDescribesInputsSeesAClaimedOutputMove(t *testing.T) {
	root, c := openCache(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
	src := filepath.Join(root, "pkg", "enum.go")
	require.NoError(t, os.WriteFile(src, []byte("package pkg // one\n"), 0o644))

	// Declared as an output, which is what the blame check exempts and the store gate
	// must not. Not under gen/, which the source walk prunes.
	s := Step{ProjectPath: ".", WorkspaceRoot: root, Target: "build",
		Sources: types.MustParseGlobs("pkg/*.go"), OwnedOutputs: types.MustParseGlobs("pkg/*.go")}

	before, err := c.fingerprintSources(t.Context(), &s)
	require.NoError(t, err)
	require.NotEmpty(t, before, "the fingerprint saw no sources, so nothing below proves anything")

	require.NoError(t, os.WriteFile(src, []byte("package pkg // two, rewritten by a peer\n"), 0o644))
	after, err := c.fingerprintSources(t.Context(), &s)
	require.NoError(t, err)
	moved, fresh := keyStillDescribesInputs(before, after)
	assert.False(t, fresh, "a hashed input moved and the key was still called fresh")
	assert.Equal(t, []string{"pkg/enum.go"}, moved)
}

func TestASourceEditedDuringTheMissIsNotFiledUnderTheOldKey(t *testing.T) {
	root, c, step := formatterStep(t)
	src := filepath.Join(root, "pkg", "enum.go")
	const keyedAt = "keyed"
	require.NoError(t, os.WriteFile(src, []byte(keyedAt), 0o644))

	runs := 0
	body := func(context.Context) error {
		runs++
		return os.WriteFile(src, []byte("edited by a peer"), 0o644)
	}
	_, err := c.Run(t.Context(), step, body)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(src, []byte(keyedAt), 0o644))
	_, err = c.Run(t.Context(), step, body)
	require.NoError(t, err)
	assert.Equal(t, 2, runs, "the entry was filed under a key the edit had already outdated")
}

func TestASkippedFreshnessCheckRefusesTheStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 0 file, so the re-check cannot fail")
	}
	logs := &recordingHandler{}
	root, c := openCache(t, WithLogger(slog.New(logs)))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
	src := filepath.Join(root, "pkg", "enum.go")
	require.NoError(t, os.WriteFile(src, []byte("package pkg"), 0o644))
	step := Step{ProjectPath: ".", WorkspaceRoot: root, Target: "build",
		Sources: types.MustParseGlobs("pkg/*.go")}

	runs := 0
	body := func(context.Context) error {
		runs++
		return os.Chmod(src, 0)
	}
	_, err := c.Run(t.Context(), step, body)
	require.NoError(t, err, "a skipped check must not turn the run red")
	require.NoError(t, os.Chmod(src, 0o644))

	warned := slices.ContainsFunc(logs.lines(), func(l string) bool {
		return strings.Contains(l, "cache.warn") && strings.Contains(l, "could not be re-checked")
	})
	assert.True(t, warned, "the skip is logged at warn with its cause")

	_, err = c.Run(t.Context(), step, body)
	require.NoError(t, err)
	assert.Equal(t, 2, runs, "an entry was stored although its freshness was never checked")
}

// A moved input some target generates is the step's own run rewriting its key, which is
// MGS4010; one nothing declares moved under it from outside and stays a plain notice.
func TestMovedInputsNoticeCodesOnlyAGeneratedInput(t *testing.T) {
	s := Step{ProjectPath: ".", Target: "test", OwnedOutputs: types.MustParseGlobs("gen/*.json")}

	got := movedInputsNotice(s, "0123456789abcdef", []string{"gen/graph.json", "README.md"})
	assert.Contains(t, got, string(types.SelfInvalidatingKey))
	assert.Contains(t, got, "gen/graph.json")
	assert.NotContains(t, got, "README.md", "only the generated inputs are the target's own doing")

	got = movedInputsNotice(s, "0123456789abcdef", []string{"README.md"})
	assert.NotContains(t, got, string(types.SelfInvalidatingKey))
	assert.Contains(t, got, "README.md")
}

// UndeclaredWriteSuite pins MGS4011: a run that writes a file in its project
// that no target declares fails and names the file.
type UndeclaredWriteSuite struct {
	suite.Suite
	root string
	c    *Cache
	logs *recordingHandler
}

func TestUndeclaredWriteSuite(t *testing.T) {
	suite.Run(t, new(UndeclaredWriteSuite))
}

func (s *UndeclaredWriteSuite) SetupTest() {
	s.logs = &recordingHandler{}
	s.root, s.c = openCache(s.T(), WithLogger(slog.New(s.logs)))
	s.write("pkg/main.go", "package main")
}

func (s *UndeclaredWriteSuite) noticeNaming(rel string) bool {
	return slices.ContainsFunc(s.logs.lines(), func(l string) bool {
		return strings.Contains(l, string(types.UndeclaredOutputWritten)) && strings.Contains(l, rel)
	})
}

func (s *UndeclaredWriteSuite) write(rel, body string) {
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	s.Require().NoError(os.MkdirAll(filepath.Dir(abs), 0o755))
	s.Require().NoError(os.WriteFile(abs, []byte(body), 0o644))
}

func (s *UndeclaredWriteSuite) step(project, target string) Step {
	return Step{
		ProjectPath:   project,
		WorkspaceRoot: s.root,
		Target:        target,
		Sources:       types.MustParseGlobs("pkg/*.go"),
	}
}

func (s *UndeclaredWriteSuite) writing(rels ...string) func(context.Context) error {
	return func(context.Context) error {
		for _, rel := range rels {
			s.write(rel, "written by the target")
		}
		return nil
	}
}

func (s *UndeclaredWriteSuite) TestAnUndeclaredFileFailsAndIsNamed() {
	_, err := s.c.Run(s.T().Context(), s.step(".", "breaking"), s.writing("report.txt"))
	s.Require().Error(err)
	s.Contains(err.Error(), "report.txt")
}

func (s *UndeclaredWriteSuite) TestTheErrorCarriesItsCode() {
	_, err := s.c.Run(s.T().Context(), s.step(".", "breaking"), s.writing("report.txt"))
	s.Require().ErrorIs(err, types.UndeclaredOutputWritten)
}

func (s *UndeclaredWriteSuite) TestADeclaredOutputPasses() {
	step := s.step(".", "build")
	step.Outputs = types.MustParseGlobs("dist/**")
	_, err := s.c.Run(s.T().Context(), step, s.writing("dist/app"))
	s.Require().NoError(err)
}

func (s *UndeclaredWriteSuite) TestADeclaredUpdatePasses() {
	s.write("notes.md", "unformatted")
	step := s.step(".", "format")
	step.Updates = types.MustParseGlobs("notes.md")
	_, err := s.c.Run(s.T().Context(), step, s.writing("notes.md"))
	s.Require().NoError(err)
}

func (s *UndeclaredWriteSuite) TestAWriteUnderMagusStatePasses() {
	_, err := s.c.Run(s.T().Context(), s.step(".", "graph"), s.writing(".magus/graph.jsonl"))
	s.Require().NoError(err)
}

func (s *UndeclaredWriteSuite) TestATargetWritingNothingPasses() {
	_, err := s.c.Run(s.T().Context(), s.step(".", "lint"), func(context.Context) error { return nil })
	s.Require().NoError(err)
}

func (s *UndeclaredWriteSuite) TestACacheHitDoesNotRunTheCheck() {
	step := s.step(".", "lint")
	_, err := s.c.Run(s.T().Context(), step, func(context.Context) error { return nil })
	s.Require().NoError(err)

	res, err := s.c.Run(s.T().Context(), step, s.writing("report.txt"))
	s.Require().NoError(err)
	s.True(res.Hit)
	s.NoFileExists(filepath.Join(s.root, "report.txt"), "a hit runs no body")
}

func (s *UndeclaredWriteSuite) TestANestedProjectsWriteIsNotBlamedOnItsParent() {
	step := s.step(".", "build")
	step.NestedDirs = []string{"libs/x"}
	_, err := s.c.Run(s.T().Context(), step, s.writing("libs/x/out.txt"))
	s.Require().NoError(err)
}

func (s *UndeclaredWriteSuite) TestAnotherProjectsDeclaredOutputIsClaimed() {
	s.write("app/keep", "")
	step := s.step("app", "generate")
	step.OwnedOutputs = types.MustParseGlobs("app/schema.json")
	_, err := s.c.Run(s.T().Context(), step, s.writing("app/schema.json"))
	s.Require().NoError(err)
}

// runBeside keeps peer running until writer's run returns, so the write
// lands inside peer's window.
func (s *UndeclaredWriteSuite) runBeside(peer, writer Step, rel string) (peerErr, writerErr error) {
	started, done := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := s.c.Run(s.T().Context(), peer, func(context.Context) error {
			close(started)
			<-done
			return nil
		})
		result <- err
	}()
	<-started
	_, writerErr = s.c.Run(s.T().Context(), writer, s.writing(rel))
	close(done)
	return <-result, writerErr
}

func (s *UndeclaredWriteSuite) TestTwoTargetsOfOneProjectDoNotBlameEachOther() {
	peerErr, writerErr := s.runBeside(s.step(".", "test"), s.step(".", "lint"), "report.txt")
	s.NoError(peerErr, "the peer was running as the file was written")
	s.NoError(writerErr, "the writer had a peer running beside it")
	s.True(s.noticeNaming("report.txt"), "a write nobody is blamed for is still reported")
}

func (s *UndeclaredWriteSuite) TestAPeersDeclaredOutputClaimsTheWrite() {
	peer := s.step(".", "build")
	peer.Outputs = types.MustParseGlobs("dist/**")
	_, writerErr := s.runBeside(peer, s.step(".", "test"), "dist/app")
	s.Require().NoError(writerErr)
	s.False(s.noticeNaming("dist/app"), "a peer declared the write")
}

func (s *UndeclaredWriteSuite) TestASiblingProjectsRunDoesNotExcuseAWrite() {
	s.write("a/keep", "")
	s.write("b/keep", "")
	_, writerErr := s.runBeside(s.step("b", "test"), s.step("a", "lint"), "a/report.txt")
	s.ErrorIs(writerErr, types.UndeclaredOutputWritten)
}

// chain runs inner from outer's body, the way ctx.needs does.
func (s *UndeclaredWriteSuite) chain(outer, inner Step, rel string) (outerErr, innerErr error) {
	_, outerErr = s.c.Run(s.T().Context(), outer, func(ctx context.Context) error {
		_, innerErr = s.c.Run(ctx, inner, s.writing(rel))
		return nil
	})
	return outerErr, innerErr
}

func (s *UndeclaredWriteSuite) TestAChainedTargetIsBlamedForItsOwnWrite() {
	_, innerErr := s.chain(s.step(".", "ci"), s.step(".", "generate"), "report.txt")
	s.ErrorIs(innerErr, types.UndeclaredOutputWritten, "the enclosing run is not a peer")
}

func (s *UndeclaredWriteSuite) TestAChainedTargetsDeclaredOutputPassesTheOuterRun() {
	inner := s.step(".", "generate")
	inner.Outputs = types.MustParseGlobs("gen.txt")
	outerErr, innerErr := s.chain(s.step(".", "ci"), inner, "gen.txt")
	s.NoError(innerErr)
	s.NoError(outerErr)
}

func (s *UndeclaredWriteSuite) TestOutputRedirectedIntoTheTreeIsNotBlamed() {
	log, err := os.Create(filepath.Join(s.root, "run.log"))
	s.Require().NoError(err)
	defer func() { _ = log.Close() }()
	stdout := os.Stdout
	os.Stdout = log
	defer func() { os.Stdout = stdout }()

	_, err = s.c.Run(s.T().Context(), s.step(".", "test"), func(context.Context) error {
		_, err := log.WriteString("ok\n")
		return err
	})
	s.Require().NoError(err)
}

func (s *UndeclaredWriteSuite) TestASpellsDeclaredCacheInsideTheTreeIsClaimed() {
	s.write(".gocache/README", "")
	step := s.step(".", "test")
	step.WritePaths = []string{filepath.Join(s.root, ".gocache")}
	_, err := s.c.Run(s.T().Context(), step, s.writing(".gocache/00/entry-a"))
	s.Require().NoError(err)
}

// gitTrack makes the workspace a repository tracking rels.
func (s *UndeclaredWriteSuite) gitTrack(rels ...string) {
	for _, args := range [][]string{
		{"init", "-q"},
		append([]string{"add", "--"}, rels...),
	} {
		out, err := osexec.Command("git", append([]string{"-C", s.root}, args...)...).CombinedOutput()
		s.Require().NoError(err, string(out))
	}
}

func (s *UndeclaredWriteSuite) TestATrackedFileSavedMidRunIsNotBlamed() {
	s.write("README.md", "before")
	s.gitTrack("README.md")
	_, err := s.c.Run(s.T().Context(), s.step(".", "test"), s.writing("README.md"))
	s.Require().NoError(err)
}

func (s *UndeclaredWriteSuite) TestAModifiedUntrackedFileIsBlamed() {
	s.write("README.md", "before")
	s.write("coverage.out", "before")
	s.gitTrack("README.md")
	_, err := s.c.Run(s.T().Context(), s.step(".", "test"), s.writing("coverage.out"))
	s.Require().ErrorIs(err, types.UndeclaredOutputWritten)
	s.Contains(err.Error(), "coverage.out")
}

func (s *UndeclaredWriteSuite) TestARelinkedBinaryIsClaimed() {
	step := s.step(".", "go-build")
	step.WritePaths = []string{filepath.Join(s.root, "magus")}
	_, err := s.c.Run(s.T().Context(), step, s.writing("magus"))
	s.Require().NoError(err)
}

func (s *UndeclaredWriteSuite) TestANestedCheckoutIsNotWalked() {
	s.write("worktrees/w/.git", "gitdir: elsewhere")
	_, err := s.c.Run(s.T().Context(), s.step(".", "lint"), s.writing("worktrees/w/edited.go"))
	s.Require().NoError(err)
}
