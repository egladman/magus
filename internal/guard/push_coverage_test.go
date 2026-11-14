package guard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// largeWorkspace answers the two reads runEvidence makes of a workspace.
type largeWorkspace struct {
	types.WorkspaceRepository
	projects []*types.Project
	byPath   map[string]*types.Project
}

func (w largeWorkspace) All() []*types.Project { return w.projects }

func (w largeWorkspace) Get(path string) *types.Project { return w.byPath[path] }

// largePush is sized like a branch that lapsed the guard's deadline. A
// thousand files meet 1344 runs over twenty projects. A third of the runs
// name a package path.
func largePush(b *testing.B) (largeWorkspace, []cache.OutputDescriptor, []string, string) {
	dir := b.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		b.Fatalf("git init: %v: %s", err, out)
	}
	targets := []string{"test", "lint", "build", "generate"}
	ws := largeWorkspace{byPath: map[string]*types.Project{}}
	root := &types.Project{
		Path:             ".",
		Sources:          []types.Glob{{Pattern: "**/*.go"}, {Pattern: "**/*.buzz"}},
		MagusfileTargets: append([]string{string(types.TargetCI)}, targets...),
		TargetInputs: map[string][]types.InputRef{
			"lint":     {{Glob: "**/*.go"}, {Glob: ".golangci.yml"}},
			"generate": {{Glob: "types/**/*.go"}},
		},
		TargetOutputs: map[string][]types.OutputRef{"generate": {{Glob: "gen/*.go"}}},
	}
	ws.projects = append(ws.projects, root)
	for i := range 19 {
		ws.projects = append(ws.projects, &types.Project{
			Path:             fmt.Sprintf("libs/l%02d", i),
			Sources:          []types.Glob{{Pattern: "**/*.go"}, {Pattern: "**/*.md", Except: []string{"CHANGELOG.md"}}},
			MagusfileTargets: targets,
			TargetInputs:     map[string][]types.InputRef{"lint": {{Glob: "**/*.go"}, {Glob: "../../.golangci.yml"}}},
			TargetOutputs:    map[string][]types.OutputRef{"generate": {{Glob: "gen/**"}}},
		})
	}
	for _, p := range ws.projects {
		ws.byPath[p.Path] = p
		for pkg := range 4 {
			if err := os.MkdirAll(filepath.Join(dir, p.Path, fmt.Sprintf("pkg%02d", pkg)), 0o755); err != nil {
				b.Fatal(err)
			}
		}
	}
	runs := make([]cache.OutputDescriptor, 1344)
	for i := range runs {
		d := cache.OutputDescriptor{
			Project:  ws.projects[i%len(ws.projects)].Path,
			Target:   targets[i%len(targets)],
			Revision: fmt.Sprintf("r%02d", i%16),
			Failed:   i%10 == 0,
		}
		if i%5 == 0 {
			d.Target += ":rw"
		}
		switch i % 3 {
		case 0:
			d.ExtraArgs = []string{"-run", "Push"}
		case 1:
			d.ExtraArgs = []string{fmt.Sprintf("pkg%02d", i%8)}
		}
		runs[i] = d
	}
	files := make([]string, 1000)
	for k := range files {
		p := ws.projects[k%len(ws.projects)].Path
		switch k % 7 {
		case 0:
			files[k] = filepath.ToSlash(filepath.Join(p, "gen", fmt.Sprintf("f%04d.go", k)))
		case 1:
			files[k] = filepath.ToSlash(filepath.Join(p, fmt.Sprintf("pkg%02d", k%8), fmt.Sprintf("f%04d.md", k)))
		default:
			files[k] = filepath.ToSlash(filepath.Join(p, fmt.Sprintf("pkg%02d", k%8), fmt.Sprintf("f%04d.go", k)))
		}
	}
	return ws, runs, files, dir
}

// BenchmarkRunEvidenceWeighsALargePush weighs each changed file as
// narrowCoverageAt does. The repository holds no revision, so one git
// process answers and the rest measures matching files to runs.
func BenchmarkRunEvidenceWeighsALargePush(b *testing.B) {
	ws, runs, files, dir := largePush(b)
	driver := typestest.NewMockVCSDriver(b)
	ctx := b.Context()
	for b.Loop() {
		ev := newRunEvidence(driver, dir, dir, ws, runs, "c1")
		if err := ev.readGitFiles(ctx, files); err != nil {
			b.Fatal(err)
		}
		for _, file := range files {
			if !ev.covered(ctx, file) {
				ev.wouldCover(file)
			}
		}
	}
}

func (s *NarrowPushSuite) TestARunAtAnOlderRevisionCoversAFileUnchangedSince() {
	s.gateAtFirstCommit()
	older := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n"})
	s.passed(strings.Repeat("a", 64), "go-test", older)
	head := s.repo.Commit("again", map[string]string{"b.go": "package a\n\nvar B = 1\n"})
	s.passed(strings.Repeat("b", 64), "lint", head, "b.go")

	ev := s.push(head)
	s.Equal(gateNarrow, ev.cover)
	s.Contains(ev.admitted(), "each of the 2 files")
}

func (s *NarrowPushSuite) TestARunAtAnOlderRevisionLeavesAFileChangedSinceUncovered() {
	s.gateAtFirstCommit()
	older := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n"})
	s.passed(strings.Repeat("a", 64), "go-test", older)
	head := s.repo.Commit("again", map[string]string{"b.go": "package a\n\nvar B = 1\n"})

	ev := s.push(head)
	s.Equal(gateAbsent, ev.cover)
	_, reason := gradePushWithoutGate(ev, "")
	s.Contains(reason, "No passing run covers 1 of the 2 files")
	s.Contains(reason, "  b.go: run `")
	s.NotContains(reason, "  a.go:")
}

// A push weighed past its share of the deadline leaves the rest to the rules.
func (s *NarrowPushSuite) TestAPushWeighedPastItsBudgetSaysSoAndLeavesTheRulesTheirs() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.passed(strings.Repeat("a", 64), "go-test", head)
	s.deps.Revision = func(ctx context.Context, _, _ string) string {
		<-ctx.Done()
		return head[:12]
	}
	deadline, cancel := context.WithTimeout(s.T().Context(), pushCoverageBudget+time.Second)
	defer cancel()
	ctx := withPhaseLog(deadline, trail.Phase{})

	ev := pushCoverage(ctx, s.deps, s.at, "git push", DialectBash, s.repo.Root())
	s.Require().NoError(deadline.Err(), "the rules judged after the push gate keep their share")
	until, _ := deadline.Deadline()
	s.Greater(time.Until(until), judgeReserve)
	s.Equal(gateAbsent, ev.cover)
	_, reason := gradePushWithoutGate(ev, "")
	s.Contains(reason, "Narrow runs were not weighed: weighing the changed files did not finish within the ")
	s.Contains(reason, " the guard gives the push gate.")
	var coverage trail.Phase
	for _, p := range judgmentPhases(ctx).finished() {
		if p.Name == phasePushCoverage {
			coverage = p
		}
	}
	s.True(coverage.Lapsed)
	s.Positive(coverage.BudgetMs)
}
