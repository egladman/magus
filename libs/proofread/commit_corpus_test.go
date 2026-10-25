package proofread

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommitRulesHoldTheForgeShapeThisRepositoryLands is the dogfood lock for the
// commit rules. It reads every commit this repository holds and fails when a rule
// treats the forge's own text as a defect: the " (#123)" a squash merge appends
// to the subject, or the "* " list it writes under it. A new commit of that shape
// keeps passing. A rule that starts flagging the shape fails here, on the corpus
// the rule was measured against.
func TestCommitRulesHoldTheForgeShapeThisRepositoryLands(t *testing.T) {
	root := repositoryRoot(t)

	out, err := exec.Command("git", "-C", root, "log", "-z", "--format=%B").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}

	n := 0
	var suffix, bullets []string
	counts := map[commitShape]int{}

	for _, raw := range bytes.Split(out, []byte{0}) {
		text := strings.TrimLeft(string(raw), "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}

		n++
		subject, _, _ := strings.Cut(text, "\n")

		for _, f := range JudgeText(text, KindCommitMessage, denyCommit(RuleSubjectPunct)...) {
			if f.Match == "(" && !strings.Contains(pullRequestSuffix.ReplaceAllString(subject, ""), "(") {
				suffix = append(suffix, subject)
			}
		}

		for _, f := range JudgeText(text, KindCommitMessage, denyCommit(RuleNoBody)...) {
			if strings.HasPrefix(f.Match, "* ") {
				bullets = append(bullets, f.Match)
			}
		}

		for _, s := range commitShapes(text) {
			counts[s]++
		}
	}

	// 1765 is the corpus measured on 2026-10-25. A shallow clone is smaller and
	// would pass without ever seeing the shape.
	if n < 1765 {
		t.Fatalf("corpus has %d commits, want at least 1765: a shallow clone cannot show the shape these rules were measured on", n)
	}

	if len(suffix) > 0 {
		t.Fatalf("%d subjects flag the forge trailer (#123) as a parenthesis; the first is %q", len(suffix), suffix[0])
	}

	if len(bullets) > 0 {
		t.Fatalf("%d squash-list lines are flagged as a prose body; the first is %q", len(bullets), bullets[0])
	}

	var unnamed []string
	for _, s := range shapesClearing(counts) {
		if namedCommitShapes[s.commitShape] == "" {
			unnamed = append(unnamed, fmt.Sprintf("%s %s holds %d of %d hits (%.2f)", s.Rule, s.Shape, s.Count, s.Hits, s.Share))
		}
	}
	if len(unnamed) > 0 {
		t.Fatalf("a shape is one population and unnamed:\n%s\nName it in namedCommitShapes with the reason, or change the rule.", strings.Join(unnamed, "\n"))
	}
}

// namedCommitShapes are the populations this repository has already decided.
// A shape the gate clears that is absent here fails the corpus test. The
// forge trailer and the squash list are named from the 2026-10-25 measurement
// even while each is under the gate, so crossing it later is not a new decision.
var namedCommitShapes = map[commitShape]string{
	{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer}: "a trailing (#123) is the forge's, and subject-punct leaves it out",
	{Rule: RuleNoBody, Shape: shapeSquashList}:         "a * list is the squash merge, and no-body leaves it out",
}

// repositoryRoot is the magus checkout that holds this module. The corpus is
// that checkout's history, so a tree that is not the checkout cannot stand in
// for it. The test binary is built with -trimpath, so the source path is not
// a filesystem path; the walk starts at the working directory.
func repositoryRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("commit corpus: %v", err)
	}

	dir := wd
	for {
		marker := filepath.Join(dir, "hack", "policy", "proofread.buzz")
		gitDir := filepath.Join(dir, ".git")
		info, markerErr := os.Stat(marker)
		if markerErr == nil && !info.IsDir() {
			if _, gitErr := os.Stat(gitDir); gitErr == nil {
				return dir
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("commit corpus: walked from %s and found no checkout holding hack/policy/proofread.buzz and a .git directory", wd)
		}

		dir = parent
	}
}
