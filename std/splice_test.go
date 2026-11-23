//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

type SpliceModuleSuite struct {
	suite.Suite
	root string
}

func TestSpliceModule(t *testing.T) { suite.Run(t, new(SpliceModuleSuite)) }

func (s *SpliceModuleSuite) SetupTest() {
	s.root = filesystem.ResolveRulePath(s.T().TempDir())
	s.write("a.go", "package a\n\nvar x = 1\n")
}

func (s *SpliceModuleSuite) write(rel, body string) {
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	s.Require().NoError(os.MkdirAll(filepath.Dir(abs), 0o755))
	s.Require().NoError(os.WriteFile(abs, []byte(body), 0o644))
}

func (s *SpliceModuleSuite) read(rel string) string {
	body, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	s.Require().NoError(err)
	return string(body)
}

func (s *SpliceModuleSuite) inRoot() context.Context {
	return WithCurrentWorkingDirectory(context.Background(), s.root)
}

// leased grants writes under allowed/ alone, and its judge refuses
// claimed.go the way the guard refuses another lease's file.
func (s *SpliceModuleSuite) leased() context.Context {
	ctx := sandbox.WithPolicy(context.Background(), &sandbox.Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{
		{Path: s.root, Read: true},
		{Path: filepath.Join(s.root, "allowed"), Read: true, Write: true},
	}}})
	ctx = WithEditJudge(ctx, EditJudge{
		Covers: func(_ context.Context, path string) bool { return strings.HasPrefix(path, s.root) },
		Grade: func(_ context.Context, path string, _, _ []byte) error {
			if filepath.Base(path) == "claimed.go" {
				return errors.New("refused by the guard [claimed-declaration]: lease other claims it")
			}
			return nil
		},
	})
	return WithCurrentWorkingDirectory(ctx, s.root)
}

func (s *SpliceModuleSuite) plan(ctx context.Context, changes ...types.EditChange) types.EditPlan {
	p, err := SplicePlan(ctx, changes)
	s.Require().NoError(err)
	return p
}

func (s *SpliceModuleSuite) apply(ctx context.Context, p types.EditPlan) types.Result[types.EditApplied] {
	r, err := SpliceApply(ctx, p)
	s.Require().NoError(err)
	return r
}

// failure is r's Diagnostic with its message cut to the prefix
// asserted, so the comparison covers every other field.
func (s *SpliceModuleSuite) failure(r types.Result[types.EditApplied], prefix string) types.Diagnostic {
	s.Require().False(r.IsOk())
	got := *r.Error
	s.Require().True(strings.HasPrefix(got.Message, prefix), got.Message)
	got.Message = prefix
	return got
}

func refusedDiagnostic(message string) types.Diagnostic {
	return types.Diagnostic{
		Code:    string(types.EditPlanRefused),
		Message: message,
		URL:     types.CodeURL(types.EditPlanRefused),
		Hint:    "fix each refusal, then plan again: nothing was written",
	}
}

func (s *SpliceModuleSuite) TestReplaceIsAUniqueAnchor() {
	c, err := SpliceReplace(s.inRoot(), "a.go", "x", "y")
	s.Require().NoError(err)
	s.Equal(types.EditChange{Path: "a.go", Old: "x", New: "y", Count: 1}, c)
}

func (s *SpliceModuleSuite) TestReplaceAllIsEveryOccurrence() {
	c, err := SpliceReplaceAll(s.inRoot(), "a.go", "x", "y")
	s.Require().NoError(err)
	s.Equal(types.EditChange{Path: "a.go", Old: "x", New: "y", Count: -1}, c)
}

func (s *SpliceModuleSuite) TestReplaceRegexIsEveryMatch() {
	c, err := SpliceReplaceRegex(s.inRoot(), "a.go", `x (\d)`, "y $1")
	s.Require().NoError(err)
	s.Equal(types.EditChange{Path: "a.go", Old: `x (\d)`, New: "y $1", Count: -1, Regex: true}, c)
}

func (s *SpliceModuleSuite) TestCreateCarriesTheContent() {
	c, err := SpliceCreate(s.inRoot(), "b.go", "package b\n")
	s.Require().NoError(err)
	s.Equal(types.EditChange{Path: "b.go", New: "package b\n", Create: true}, c)
}

func (s *SpliceModuleSuite) TestBlocksParsesADocument() {
	doc, err := SpliceBlocks(s.inRoot(), "## a.go\n```old\nx = 1\n```\n```new\nx = 2\n```\n", "edits.md")
	s.Require().NoError(err)
	s.Equal(types.EditDocument{Changes: []types.EditChange{{Path: "a.go", Old: "x = 1", New: "x = 2", Count: 1}}}, doc)
}

func (s *SpliceModuleSuite) TestBlocksRefusalsNameTheDocument() {
	doc, err := SpliceBlocks(s.inRoot(), "```create\n```\n", "edits.md")
	s.Require().NoError(err)
	s.Require().Len(doc.Refused, 1)
	s.Equal("edits.md", doc.Refused[0].Path)
}

func (s *SpliceModuleSuite) TestPlanWritesNothing() {
	p := s.plan(s.inRoot(), types.EditChange{Path: "a.go", Old: "x = 1", New: "x = 2", Count: 1})

	s.Empty(p.Refused)
	s.False(p.NothingToDo)
	s.NotEmpty(p.Digest)
	s.Equal([]types.EditSpan{{Path: "a.go", Start: types.EditPosition{Line: 3, Column: 5}, End: types.EditPosition{Line: 3, Column: 10}}}, p.Spans)
	s.Require().Len(p.Files, 1)
	s.Equal("a.go", p.Files[0].Path)
	s.Equal("package a\n\nvar x = 1\n", s.read("a.go"))
}

func (s *SpliceModuleSuite) TestPlanRefusalsAreData() {
	p := s.plan(s.inRoot(), types.EditChange{Path: "missing.go", Old: "x", New: "y", Count: 1})

	s.Equal([]types.EditRefusal{{Path: "missing.go", Reason: "no such file; a new file is a creation"}}, p.Refused)
	s.Empty(p.Digest)
	s.Empty(p.Files)
}

func (s *SpliceModuleSuite) TestPlanUnderALeaseRefusesOnePathAndAllowsAnother() {
	s.write("allowed/ok.go", "package ok\n")
	s.write("denied/no.go", "package no\n")

	p := s.plan(s.leased(),
		types.EditChange{Path: "allowed/ok.go", Old: "ok", New: "fine", Count: 1},
		types.EditChange{Path: "denied/no.go", Old: "no", New: "nope", Count: 1},
	)

	s.Require().Len(p.Refused, 1)
	s.Equal("denied/no.go", p.Refused[0].Path)
	s.Contains(p.Refused[0].Reason, string(types.PathWriteDenied))
	s.Equal("package no\n", s.read("denied/no.go"))
}

func (s *SpliceModuleSuite) TestPlanUnderALeaseAllowsTheGrantedPath() {
	s.write("allowed/ok.go", "package ok\n")
	ctx := s.leased()

	p := s.plan(ctx, types.EditChange{Path: "allowed/ok.go", Old: "ok", New: "fine", Count: 1})
	s.Require().Empty(p.Refused)
	r := s.apply(ctx, p)

	s.Require().True(r.IsOk(), "%+v", r.Error)
	s.Equal("package fine\n", s.read("allowed/ok.go"))
}

func (s *SpliceModuleSuite) TestPlanMeetsTheEditJudge() {
	s.write("allowed/claimed.go", "package claimed\n")

	p := s.plan(s.leased(), types.EditChange{Path: "allowed/claimed.go", Old: "claimed", New: "mine", Count: 1})

	s.Equal([]types.EditRefusal{{Path: "allowed/claimed.go",
		Reason: "refused by the guard [claimed-declaration]: lease other claims it"}}, p.Refused)
}

func (s *SpliceModuleSuite) TestACreationMeetsTheWriteCheck() {
	p := s.plan(s.leased(),
		types.EditChange{Path: "allowed/new/a.go", New: "package a\n", Create: true},
		types.EditChange{Path: "denied/b.go", New: "package b\n", Create: true},
	)

	s.Require().Len(p.Refused, 1)
	s.Equal("denied/b.go", p.Refused[0].Path)
	s.Contains(p.Refused[0].Reason, string(types.PathWriteDenied))
}

func (s *SpliceModuleSuite) TestACreationPlansItsParents() {
	p := s.plan(s.inRoot(), types.EditChange{Path: "b/c/new.go", New: "package c\n", Create: true})

	s.Empty(p.Refused)
	s.Equal([]string{"b", "b/c"}, p.MakesDirs)
	s.Require().Len(p.Files, 1)
	s.Empty(p.Files[0].DigestBefore)
}

func (s *SpliceModuleSuite) TestAReadDeniedPathIsNotRead() {
	ctx := sandbox.WithPolicy(context.Background(), &sandbox.Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{
		{Path: filepath.Join(s.root, "open"), Read: true, Write: true},
	}}})
	ctx = WithCurrentWorkingDirectory(ctx, s.root)

	p := s.plan(ctx, types.EditChange{Path: "a.go", Old: "x = 1", New: "x = 2", Count: 1})

	s.Require().Len(p.Refused, 1)
	s.Equal("a.go", p.Refused[0].Path)
	s.Contains(p.Refused[0].Reason, string(types.PathReadDenied))
}

func (s *SpliceModuleSuite) TestDiffReadsAsAPatch() {
	p := s.plan(s.inRoot(),
		types.EditChange{Path: "a.go", Old: "x = 1", New: "x = 2", Count: 1},
		types.EditChange{Path: "b.go", New: "package b\n", Create: true},
	)

	got, err := SpliceDiff(s.inRoot(), p, 1)

	s.Require().NoError(err)
	s.Equal("--- a.go\n+++ edited\n@@ -2,2 +2,2 @@\n \n-var x = 1\n+var x = 2\n"+
		"--- b.go\n+++ edited\n@@ -0,0 +1 @@\n+package b\n", got.Value)
	s.Nil(got.Error)
}

func (s *SpliceModuleSuite) TestDiffRefusesWholePreviewAfterTreeChanges() {
	p := s.plan(s.inRoot(),
		types.EditChange{Path: "a.go", Old: "x = 1", New: "x = 2", Count: 1},
		types.EditChange{Path: "b.go", New: "package b\n", Create: true})
	s.write("b.go", "other\n")
	got, err := SpliceDiff(s.inRoot(), p, 1)
	s.Require().NoError(err)
	s.Require().NotNil(got.Error)
	s.Equal(string(types.EditPlanRefused), got.Error.Code)
	s.Contains(got.Error.Message, "b.go")
	s.Empty(got.Value)
	s.Equal("other\n", s.read("b.go"))
}

func (s *SpliceModuleSuite) TestApplyWritesThePlan() {
	p := s.plan(s.inRoot(), types.EditChange{Path: "a.go", Old: "x = 1", New: "x = 2", Count: 1})

	r := s.apply(s.inRoot(), p)

	s.Require().True(r.IsOk(), "%+v", r.Error)
	s.Equal(types.EditApplied{Written: p.Files}, r.Value)
	s.Equal("package a\n\nvar x = 2\n", s.read("a.go"))
}

func (s *SpliceModuleSuite) TestARerunHasNothingToDo() {
	changes := []types.EditChange{
		{Path: "a.go", Old: "x = 1", New: "x = 2", Count: 1},
		{Path: "b.go", New: "package b\n", Create: true},
	}
	first := s.plan(s.inRoot(), changes...)
	s.Require().True(s.apply(s.inRoot(), first).IsOk())

	again := s.plan(s.inRoot(), changes...)
	r := s.apply(s.inRoot(), again)

	s.True(again.NothingToDo)
	s.Equal(changes, again.Already)
	s.Require().True(r.IsOk(), "%+v", r.Error)
	s.Equal(types.EditApplied{Written: []types.EditedFile{}, Already: 2, NothingToDo: true}, r.Value)
}

func (s *SpliceModuleSuite) TestReapplyingAnAppliedPlanHasNothingToDo() {
	p := s.plan(s.inRoot(), types.EditChange{Path: "a.go", Old: "x = 1", New: "x = 2", Count: 1})
	s.Require().True(s.apply(s.inRoot(), p).IsOk())

	r := s.apply(s.inRoot(), p)

	s.Require().True(r.IsOk(), "%+v", r.Error)
	s.Equal(types.EditApplied{Written: []types.EditedFile{}, Already: 1, NothingToDo: true}, r.Value)
	s.Equal("package a\n\nvar x = 2\n", s.read("a.go"))
}

func (s *SpliceModuleSuite) TestADeletionRerunsWhileTheCommittedRevisionHoldsOld() {
	repo := testkit.NewRepository(s.T(), map[string]string{"a.go": "package a\n// gone\n"})
	ctx := WithCurrentWorkingDirectory(context.Background(), repo.Root())
	deletion := types.EditChange{Path: "a.go", Old: "// gone\n", New: "", Count: 1}
	s.Require().True(s.apply(ctx, s.plan(ctx, deletion)).IsOk())

	again := s.plan(ctx, deletion)

	s.Empty(again.Refused)
	s.True(again.NothingToDo)
	s.Equal([]types.EditChange{deletion}, again.Already)
}

func (s *SpliceModuleSuite) TestADeletionTheCommittedRevisionLacksIsRefused() {
	repo := testkit.NewRepository(s.T(), map[string]string{"a.go": "package a\n"})
	ctx := WithCurrentWorkingDirectory(context.Background(), repo.Root())

	p := s.plan(ctx, types.EditChange{Path: "a.go", Old: "// gone\n", New: "", Count: 1})

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "old is absent here and in the committed revision, " +
		"so whether this deletion was applied cannot be told: name a line that stays, in old and in new"}}, p.Refused)
}

func (s *SpliceModuleSuite) TestApplyRefusesAPlanWithRefusals() {
	p := s.plan(s.inRoot(),
		types.EditChange{Path: "missing.go", Old: "x", New: "y", Count: 1},
		types.EditChange{Path: "a.go", Old: "q", New: "r"},
	)

	r := s.apply(s.inRoot(), p)

	s.Require().False(r.IsOk())
	s.Equal(refusedDiagnostic("missing.go: no such file; a new file is a creation\n"+
		"a.go: count 0 is neither 1, one occurrence, nor -1, every occurrence"), *r.Error)
	s.Equal("package a\n\nvar x = 1\n", s.read("a.go"))
}

func (s *SpliceModuleSuite) TestApplyRefusesAStalePlan() {
	p := s.plan(s.inRoot(), types.EditChange{Path: "a.go", Old: "x = 1", New: "x = 2", Count: 1})
	s.write("a.go", "package a\n\nvar x = 1\n// someone else's line\n")

	prefix := fmt.Sprintf("the tree changed since the plan was made (digest %s, now sha256:", p.Digest)
	s.Equal(refusedDiagnostic(prefix), s.failure(s.apply(s.inRoot(), p), prefix))
	s.Equal("package a\n\nvar x = 1\n// someone else's line\n", s.read("a.go"))
}

func (s *SpliceModuleSuite) TestAWriteFailureCarriesNoCode() {
	s.write("ro/a.go", "one\n")
	p := s.plan(s.inRoot(), types.EditChange{Path: "ro/a.go", Old: "one", New: "two", Count: 1})
	s.Require().NoError(os.Chmod(filepath.Join(s.root, "ro"), 0o555))
	s.T().Cleanup(func() { _ = os.Chmod(filepath.Join(s.root, "ro"), 0o755) })

	prefix := "stage ro/a.go, nothing written"
	s.Equal(types.Diagnostic{Message: prefix}, s.failure(s.apply(s.inRoot(), p), prefix))
	s.Equal("one\n", s.read("ro/a.go"))
}

func (s *SpliceModuleSuite) TestNoMethodRaises() {
	for _, m := range Splice.Methods {
		s.False(m.Raises, m.Name)
	}
	s.NoError(ValidateModule(Splice.WithTags()))
}

func (s *SpliceModuleSuite) TestSpliceIsANativeModule() {
	s.True(slices.ContainsFunc(nativeModules, func(m Module) bool { return m.Name == "splice" }))
}
