package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/mock"
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
// narrowCoverageAt does. No revision is known, so it reads no file and
// measures the matching of files to runs.
func BenchmarkRunEvidenceWeighsALargePush(b *testing.B) {
	ws, runs, files, dir := largePush(b)
	driver := typestest.NewMockVCSDriver(b)
	driver.On("FindCommit", mock.Anything, dir, mock.Anything).Return(types.Commit{}, os.ErrNotExist)
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
