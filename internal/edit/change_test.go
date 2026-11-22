package edit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type ExpandSuite struct{ suite.Suite }

func TestEditExpand(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ExpandSuite))
}

func (s *ExpandSuite) expand(files map[string]string, changes ...types.EditChange) Expansion {
	return Expand(s.T().Context(), writeFiles(s.T(), files), changes, nil)
}

// expandCommitted expands with committed as the revision's files;
// a path it lacks reads as absent at that revision.
func (s *ExpandSuite) expandCommitted(files, committed map[string]string, changes ...types.EditChange) Expansion {
	return Expand(s.T().Context(), writeFiles(s.T(), files), changes, committedFiles(committed))
}

func committedFiles(files map[string]string) CommittedFile {
	return func(rel string) (string, error) {
		text, ok := files[rel]
		if !ok {
			return "", errors.New("absent at the revision")
		}
		return text, nil
	}
}

func replace(path, old, repl string) types.EditChange {
	return types.EditChange{Path: path, Old: old, New: repl, Count: 1}
}

func replaceAll(path, old, repl string) types.EditChange {
	return types.EditChange{Path: path, Old: old, New: repl, Count: -1}
}

func replaceRegex(path, pattern, template string) types.EditChange {
	return types.EditChange{Path: path, Old: pattern, New: template, Count: -1, Regex: true}
}

func create(path, content string) types.EditChange {
	return types.EditChange{Path: path, New: content, Create: true}
}

func at(line, col int) types.EditPosition { return types.EditPosition{Line: line, Column: col} }

func (s *ExpandSuite) TestLiteralOnceIsOneSite() {
	x := s.expand(map[string]string{"a.go": "package a\n\nvar x = 1\n"}, replace("./a.go", "x = 1", "x = 2"))

	s.Equal(Expansion{Sites: []Site{{Path: "a.go", Start: at(3, 5), End: at(3, 10), Old: "x = 1", New: "x = 2"}}}, x)
}

func (s *ExpandSuite) TestLiteralSpanningLines() {
	x := s.expand(map[string]string{"a.go": "a\nbc\nd\n"}, replace("a.go", "c\nd", "C\nD"))

	s.Equal([]Site{{Path: "a.go", Start: at(2, 2), End: at(3, 2), Old: "c\nd", New: "C\nD"}}, x.Sites)
}

func (s *ExpandSuite) TestLiteralTwiceListsEveryOccurrence() {
	x := s.expand(map[string]string{"a.go": "f()\n  f()\n"}, replace("a.go", "f()", "g()"))

	s.Empty(x.Sites)
	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "old occurs 2 times, at a.go:1:1, a.go:2:3: pass the line's own text, " +
		"or replace every occurrence (`replaceAll`, or an `old all` block)"}}, x.Refused)
}

func (s *ExpandSuite) TestLiteralAllIsEverySite() {
	x := s.expand(map[string]string{"a.go": "f(); f()\nf()\n"}, replaceAll("a.go", "f()", "g()"))

	s.Equal([]Site{
		{Path: "a.go", Start: at(1, 1), End: at(1, 4), Old: "f()", New: "g()"},
		{Path: "a.go", Start: at(1, 6), End: at(1, 9), Old: "f()", New: "g()"},
		{Path: "a.go", Start: at(2, 1), End: at(2, 4), Old: "f()", New: "g()"},
	}, x.Sites)
	s.Empty(x.Refused)
}

func (s *ExpandSuite) TestLiteralAppliedIsAlready() {
	c := replace("a.go", "x = 1", "x = 2")
	x := s.expand(map[string]string{"a.go": "var x = 2\n"}, c)

	s.Equal(Expansion{Already: []types.EditChange{c}}, x)
}

func (s *ExpandSuite) TestLiteralAllAppliedIsAlready() {
	c := replaceAll("a.go", "f()", "g()")
	x := s.expand(map[string]string{"a.go": "g(); g()\n"}, c)

	s.Equal(Expansion{Already: []types.EditChange{c}}, x)
}

func (s *ExpandSuite) TestNewHoldingOldIsAlreadyOnceApplied() {
	c := replace("a.go", "Plan", "Plan2")
	x := s.expand(map[string]string{"a.go": "var p Plan2\n"}, c)

	s.Equal(Expansion{Already: []types.EditChange{c}}, x)
}

func (s *ExpandSuite) TestNewHoldingOldReplacesOnlyBareOld() {
	x := s.expand(map[string]string{"a.go": "Plan Plan2 Plan\n"}, replaceAll("a.go", "Plan", "Plan2"))

	s.Equal([]Site{
		{Path: "a.go", Start: at(1, 1), End: at(1, 5), Old: "Plan", New: "Plan2"},
		{Path: "a.go", Start: at(1, 12), End: at(1, 16), Old: "Plan", New: "Plan2"},
	}, x.Sites)
}

func (s *ExpandSuite) TestDeletionTheCommittedRevisionHoldsIsAlready() {
	c := replace("a.go", "// gone\n", "")
	x := s.expandCommitted(map[string]string{"a.go": "package a\n"}, map[string]string{"a.go": "package a\n// gone\n"}, c)

	s.Equal(Expansion{Already: []types.EditChange{c}}, x)
}

func (s *ExpandSuite) TestDeletionTheCommittedRevisionLacksRefused() {
	x := s.expandCommitted(map[string]string{"a.go": "package a\n"}, map[string]string{"a.go": "package a\n"},
		replace("a.go", "// gone\n", ""))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "old is absent here and in the committed revision, " +
		"so whether this deletion was applied cannot be told: name a line that stays, in old and in new"}}, x.Refused)
}

func (s *ExpandSuite) TestDeletionOfAFileTheCommittedRevisionLacksRefused() {
	x := s.expandCommitted(map[string]string{"a.go": "package a\n"}, nil, replace("a.go", "// gone\n", ""))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "old is absent and the committed revision cannot be read " +
		"(absent at the revision), so whether this deletion was applied cannot be told: name a line that stays, in old and in new"}}, x.Refused)
}

func (s *ExpandSuite) TestDeletionWithoutVersionControlRefused() {
	x := s.expand(map[string]string{"a.go": "package a\n"}, replace("a.go", "// gone\n", ""))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "old is absent and no version control holds a committed revision to compare, " +
		"so whether this deletion was applied cannot be told: name a line that stays, in old and in new"}}, x.Refused)
}

func (s *ExpandSuite) TestDeletionNamingALineThatStaysIsAlready() {
	c := replace("a.go", "package a\n// gone\n", "package a\n")
	x := s.expand(map[string]string{"a.go": "package a\n"}, c)

	s.Equal(Expansion{Already: []types.EditChange{c}}, x)
}

func (s *ExpandSuite) TestOldAbsentAndNewTwiceRefused() {
	x := s.expand(map[string]string{"a.go": "g()\ng()\n"}, replace("a.go", "f()", "g()"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "old is absent and new occurs 2 times, so whether " +
		"this change was applied cannot be told: pass more of the line's text"}}, x.Refused)
}

func (s *ExpandSuite) TestNeitherOldNorNewNamesTheNearestLine() {
	x := s.expand(map[string]string{"a.go": "package a\n\nfunc Resolve(root string) {}\n"},
		replace("a.go", "func Reslove(root string) {}", "func Resolve(root string, req Request) {}"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Line: 3, Column: 1, Reason: "not found; the file holds neither old nor new; " +
		`the nearest text is line 3: "func Resolve(root string) {}"`}}, x.Refused)
}

func (s *ExpandSuite) TestNeitherOldNorNewNamesTheNearestLines() {
	x := s.expand(map[string]string{"a.go": "a\nif x {\n\treturn\n}\n"}, replace("a.go", "if x {\n    return\n}", "if y {"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Line: 2, Column: 1, Reason: "not found; the file holds neither old nor new; " +
		`the nearest text is line 2: "if x {\n\treturn\n}"`}}, x.Refused)
}

func (s *ExpandSuite) TestNeitherOldNorNewFarFromEveryLine() {
	x := s.expand(map[string]string{"a.go": "package a\n"}, replace("a.go", "something else entirely", "x"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "not found; the file holds neither old nor new"}}, x.Refused)
}

func (s *ExpandSuite) TestCountZeroRefused() {
	x := s.expand(map[string]string{"a.go": "x\n"}, types.EditChange{Path: "a.go", Old: "x", New: "y"})

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "count 0 is neither 1, one occurrence, nor -1, every occurrence"}}, x.Refused)
}

func (s *ExpandSuite) TestEmptyOldRefused() {
	x := s.expand(map[string]string{"a.go": "x\n"}, replace("a.go", "", "y"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "old is empty: name the text to replace; a new file is a creation"}}, x.Refused)
}

func (s *ExpandSuite) TestSameOldAndNewRefused() {
	x := s.expand(map[string]string{"a.go": "x\n"}, replace("a.go", "x", "x"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "old and new are the same text, so the change changes nothing"}}, x.Refused)
}

func (s *ExpandSuite) TestMissingFileNamesCreation() {
	x := s.expand(nil, replace("a.go", "x", "y"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "no such file; a new file is a creation"}}, x.Refused)
}

func (s *ExpandSuite) TestAPathUnderAFileIsNoSuchFile() {
	x := s.expand(map[string]string{"a.go": "x\n"}, replace("a.go/b.go", "x", "y"))

	s.Equal([]types.EditRefusal{{Path: "a.go/b.go", Reason: "no such file; a new file is a creation"}}, x.Refused)
}

func (s *ExpandSuite) TestOutsidePathRefused() {
	x := s.expand(nil, replace("../a.go", "x", "y"))

	s.Equal([]types.EditRefusal{{Path: "../a.go", Reason: "not a path inside the workspace"}}, x.Refused)
}

func (s *ExpandSuite) TestNoChangesRefused() {
	s.Equal([]types.EditRefusal{{Reason: "nothing to edit"}}, s.expand(nil).Refused)
}

func (s *ExpandSuite) TestCreationRefusesCount() {
	c := create("a.go", "new")
	c.Count = 1
	x := s.expand(nil, c)
	s.Require().Len(x.Refused, 1)
	s.Empty(x.Creates)
}

func (s *ExpandSuite) TestCancellationDuringCommittedReadDropsPartialBatch() {
	ctx, cancel := context.WithCancel(s.T().Context())
	defer cancel()
	root := writeFiles(s.T(), map[string]string{"a.go": "present"})
	x := Expand(ctx, root, []types.EditChange{create("first", "first"), replace("a.go", "gone", ""), create("last", "last")},
		func(string) (string, error) {
			cancel()
			return "gone", nil
		})
	s.Require().Len(x.Refused, 1)
	s.Contains(x.Refused[0].Reason, context.Canceled.Error())
	s.Empty(x.Creates)
	s.Empty(x.Already)
}

func (s *ExpandSuite) TestUniqueDeletionCannotRetryAmbiguousCommittedAnchor() {
	x := s.expandCommitted(map[string]string{"a.go": ""}, map[string]string{"a.go": "old old"}, replace("a.go", "old", ""))
	s.Require().Len(x.Refused, 1)
	s.Empty(x.Already)
}

func (s *ExpandSuite) TestEveryRefusalIsCollected() {
	x := s.expand(map[string]string{"a.go": "x\n"}, replace("a.go", "q", "r"), replace("b.go", "x", "y"), replace("a.go", "x", "y"))

	s.Len(x.Refused, 2)
	s.Len(x.Sites, 1)
}

func (s *ExpandSuite) TestRegexExpandsGroups() {
	x := s.expand(map[string]string{"a.go": "f(a, b)\nf(c, d)\n"}, replaceRegex("a.go", `f\((\w+), (\w+)\)`, "g($2, $1)"))

	s.Equal([]Site{
		{Path: "a.go", Start: at(1, 1), End: at(1, 8), Old: "f(a, b)", New: "g(b, a)"},
		{Path: "a.go", Start: at(2, 1), End: at(2, 8), Old: "f(c, d)", New: "g(d, c)"},
	}, x.Sites)
	s.Empty(x.Refused)
}

func (s *ExpandSuite) TestRegexNamedGroups() {
	x := s.expand(map[string]string{"a.go": "v1alpha1\n"}, replaceRegex("a.go", `v(?P<n>\d)alpha1`, "v${n}beta1"))

	s.Equal([]Site{{Path: "a.go", Start: at(1, 1), End: at(1, 9), Old: "v1alpha1", New: "v1beta1"}}, x.Sites)
}

func (s *ExpandSuite) TestRegexNoMatchTheCommittedRevisionMatchesIsAlready() {
	c := replaceRegex("a.go", `old\w+`, "new")
	x := s.expandCommitted(map[string]string{"a.go": "new\n"}, map[string]string{"a.go": "oldName\n"}, c)

	s.Equal(Expansion{Already: []types.EditChange{c}}, x)
}

func (s *ExpandSuite) TestRegexNoMatchHereOrCommittedRefused() {
	x := s.expandCommitted(map[string]string{"a.go": "new\n"}, map[string]string{"a.go": "new\n"}, replaceRegex("a.go", `old\w+`, "new"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "the pattern matches nothing here and in the committed revision, " +
		"so whether this change was applied cannot be told: write the pattern to match its own output too, " +
		"such as `(?:old|new)`, so a rerun reads as applied"}}, x.Refused)
}

func (s *ExpandSuite) TestRegexNoMatchWithoutVersionControlRefused() {
	x := s.expand(map[string]string{"a.go": "new\n"}, replaceRegex("a.go", `old\w+`, "new"))

	s.Require().Len(x.Refused, 1)
	s.Contains(x.Refused[0].Reason, "no version control holds a committed revision")
}

func (s *ExpandSuite) TestRegexMatchingItsOwnOutputIsAlready() {
	c := replaceRegex("a.go", `(?:old|new)Name`, "newName")
	x := s.expand(map[string]string{"a.go": "newName\n"}, c)

	s.Equal(Expansion{Already: []types.EditChange{c}}, x)
}

func (s *ExpandSuite) TestRegexMatchReadingAsItsExpansionIsAlready() {
	c := replaceRegex("a.go", `Count: \d+`, "Count: 1")
	x := s.expand(map[string]string{"a.go": "Count: 1\n"}, c)

	s.Equal(Expansion{Already: []types.EditChange{c}}, x)
}

func (s *ExpandSuite) TestRegexThatIsNotRE2Refused() {
	x := s.expand(map[string]string{"a.go": "x\n"}, replaceRegex("a.go", `a(?=b)`, "c"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "the pattern is not RE2: error parsing regexp: invalid or unsupported Perl syntax: `(?=`"}}, x.Refused)
}

func (s *ExpandSuite) TestRegexOnceMatchingTwiceRefused() {
	c := replaceRegex("a.go", `f\(\)`, "g()")
	c.Count = 1
	x := s.expand(map[string]string{"a.go": "f() f()\n"}, c)

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "the pattern matches 2 times, at a.go:1:1, a.go:1:5: " +
		"anchor it to one match, or set count -1 to replace every match"}}, x.Refused)
}

func (s *ExpandSuite) TestCreationOfANewPath() {
	x := s.expand(nil, create("./a/b.go", "package a\n"))

	s.Equal(Expansion{Creates: []Creation{{Path: "a/b.go", Content: "package a\n"}}}, x)
}

func (s *ExpandSuite) TestCreationHoldingItsContentIsAlready() {
	c := create("a.go", "package a\n")
	x := s.expand(map[string]string{"a.go": "package a\n"}, c)

	s.Equal(Expansion{Already: []types.EditChange{c}}, x)
}

func (s *ExpandSuite) TestCreationOverOtherContentRefused() {
	x := s.expand(map[string]string{"a.go": "package b\n"}, create("a.go", "package a\n"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "exists and holds other content; change it with old and new, or remove it first"}}, x.Refused)
}

func (s *ExpandSuite) TestCreationWithOldRefused() {
	c := create("a.go", "x")
	c.Old = "y"

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "a creation carries no old text or pattern; drop old, or drop create"}},
		s.expand(nil, c).Refused)
}

func (s *ExpandSuite) TestRerunOfAnAppliedPlanHasNothingToDo() {
	root := writeFiles(s.T(), map[string]string{"a.go": "var x = 1\nf(); f()\nconst k = 3\n"})
	changes := []types.EditChange{
		replace("a.go", "x = 1", "x = 2"),
		replaceAll("a.go", "f()", "g()"),
		replaceRegex("a.go", `const (\w+) = 3`, "const $1 = 4"),
		create("b/new.go", "package b\n"),
	}

	committed := committedFiles(map[string]string{"a.go": "var x = 1\nf(); f()\nconst k = 3\n"})
	first := Expand(s.T().Context(), root, changes, committed)
	s.Require().Empty(first.Refused)
	s.Require().Empty(first.Already)
	plan := Resolve(root, Request{Sites: first.Sites, Creates: first.Creates})
	s.Require().NoError(plan.Apply())
	s.Equal("var x = 2\ng(); g()\nconst k = 4\n", readFile(s.T(), root, "a.go"))

	rerun := Expand(s.T().Context(), root, changes, committed)
	s.Empty(rerun.Refused)
	s.Empty(rerun.Sites)
	s.Empty(rerun.Creates)
	s.Equal(changes, rerun.Already)
	again := Resolve(root, Request{Sites: rerun.Sites, Creates: rerun.Creates})
	s.Empty(again.Refused())
	s.Empty(again.Files())
	empty := sha256.Sum256(nil)
	s.Equal("sha256:"+hex.EncodeToString(empty[:]), again.Digest())
	s.Require().NoError(again.Apply())
	s.Equal("var x = 2\ng(); g()\nconst k = 4\n", readFile(s.T(), root, "a.go"))
}

func (s *ExpandSuite) TestARegexWhoseOutputRematchesRefused() {
	x := s.expand(map[string]string{"a.go": "a\n"}, replaceRegex("a.go", `a(b*)`, "ab$1"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "the pattern matches its own output at line 1 of the edited file, " +
		"so a rerun would apply it again: write the pattern so its output does not match it, or matches as itself"}}, x.Refused)
}

func (s *ExpandSuite) TestARegexSwapRefused() {
	x := s.expand(map[string]string{"a.go": "f(a, b)\n"}, replaceRegex("a.go", `f\((\w+), (\w+)\)`, "f($2, $1)"))

	s.Require().Len(x.Refused, 1)
	s.Contains(x.Refused[0].Reason, "the pattern matches its own output")
}

func (s *ExpandSuite) TestARegexWhoseOutputMatchesAsItselfConverges() {
	x := s.expand(map[string]string{"a.go": "Count: 2\n"}, replaceRegex("a.go", `Count: \d+`, "Count: 1"))

	s.Empty(x.Refused)
	s.Len(x.Sites, 1)
}

func (s *ExpandSuite) TestAChainRefused() {
	x := s.expand(map[string]string{"a.go": "one two\n"}, replace("a.go", "one", "two"), replace("a.go", "two", "three"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "another change in the plan writes text this change replaces " +
		"(a chain or a swap) at line 1 of the edited file, so a rerun would apply it again: " +
		"write one change from the text the file holds to the text it should hold"}}, x.Refused)
}

func (s *ExpandSuite) TestASwapRefusesBothChanges() {
	x := s.expand(map[string]string{"a.go": "left right\n"}, replace("a.go", "left", "right"), replace("a.go", "right", "left"))

	s.Len(x.Refused, 2)
	for _, r := range x.Refused {
		s.Contains(r.Reason, "(a chain or a swap)")
	}
}

func (s *ExpandSuite) TestACollapseThatFormsOldAgainRefused() {
	x := s.expand(map[string]string{"a.go": "a\n\n\n\n\nb\n"}, replaceAll("a.go", "\n\n\n", "\n\n"))

	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "the replacement forms old again at line 1 of the edited file, " +
		"so a rerun would apply it again: replace the whole run in one change, such as a pattern that matches every repeat"}}, x.Refused)
}

func (s *ExpandSuite) TestACollapseThatConvergesIsOneSite() {
	x := s.expand(map[string]string{"a.go": "a\n\n\nb\n"}, replaceAll("a.go", "\n\n\n", "\n\n"))

	s.Empty(x.Refused)
	s.Len(x.Sites, 1)
}

func (s *ExpandSuite) TestNewHoldingOldConverges() {
	x := s.expand(map[string]string{"a.go": "a a\n"}, replaceAll("a.go", "a", "ab"))

	s.Empty(x.Refused)
	s.Len(x.Sites, 2)
}

func (s *ExpandSuite) TestDistanceCountsEdits() {
	s.Equal(0, distance("kitten", "kitten", 5))
	s.Equal(3, distance("kitten", "sitting", 5))
	s.Equal(2, distance("kitten", "sitting", 2), "a distance at the limit reads as the limit")
	s.Equal(4, distance("", "abcd", 9))
}
