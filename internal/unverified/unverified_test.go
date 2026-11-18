package unverified

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// ReadSuite pins which changed files a read leaves unverified, over a real git repository
// and the output store beside it.
type ReadSuite struct {
	suite.Suite
	repo     *testkit.Repo
	base     string
	cacheDir string
	ws       types.WorkspaceRepository
	runs     int
}

func TestReadSuite(t *testing.T) { suite.Run(t, new(ReadSuite)) }

func (s *ReadSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.repo = testkit.NewRepository(s.T(), map[string]string{".gitignore": ".magus/\n", "a.go": "package a\n", "b.go": "package a\n"})
	s.base = s.repo.Head()
	s.cacheDir = filepath.Join(s.repo.Root(), ".magus")
	project := &types.Project{
		Path: ".", Sources: []types.Glob{{Pattern: "**/*.go"}, {Pattern: "LICENSE"}},
		MagusfileTargets: []string{"test", "generate", "lint", "build", "vet"},
		// build declares no inputs: its runs read only the paths their commands name.
		TargetInputs: map[string][]types.InputRef{
			"test": {{Glob: "**/*.go"}}, "lint": {{Glob: "**/*.go"}}, "vet": {{Glob: "**/*.go"}},
			"generate": {{Glob: "**/*.go"}},
		},
		TargetOutputs: map[string][]types.OutputRef{"generate": {{Glob: "gen/*.jsonl"}}},
	}
	ws := typestest.NewMockWorkspaceRepository(s.T())
	ws.EXPECT().All().Return([]*types.Project{project}).Maybe()
	ws.EXPECT().Get(".").Return(project).Maybe()
	ws.EXPECT().Get(mock.Anything).Return(nil).Maybe()
	s.ws = ws
}

// record persists one run of target at revision, shaped by edit.
func (s *ReadSuite) record(target, revision string, edit func(*cache.OutputDescriptor)) {
	s.runs++
	d := cache.OutputDescriptor{Project: ".", Target: target, Revision: revision, TimestampMs: time.Now().UnixMilli() + int64(s.runs)}
	if edit != nil {
		edit(&d)
	}
	key := fmt.Sprintf("%064x", s.runs)
	_, err := cache.NewOutputStore(s.cacheDir).Persist(testkit.WithEnvResolver(s.T().Context()), key, []byte("ok\n"), d)
	s.Require().NoError(err)
}

// write puts body at rel in the working tree, uncommitted.
func (s *ReadSuite) write(rel, body string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.repo.Root(), filepath.FromSlash(rel)), []byte(body), 0o644))
}

func (s *ReadSuite) input(base, revision string) readInput {
	res, err := vcs.Resolve(s.T().Context(), s.repo.Root(), "", types.VCSOptions{})
	s.Require().NoError(err)
	return readInput{
		Driver: res.VCS, Root: s.repo.Root(), Workspace: s.repo.Root(), Base: base, Revision: revision,
		Projects: func(context.Context) (Projects, error) { return s.ws, nil },
		Runs:     func() ([]cache.OutputDescriptor, error) { return cache.NewOutputStore(s.cacheDir).ListDescriptors() },
	}
}

func (s *ReadSuite) read(revision string) types.UnverifiedReport {
	return weigh(s.T().Context(), s.input(s.base, revision))
}

func paths(files []types.UnverifiedFile) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func (s *ReadSuite) TestACleanPassingRunAtTheRevisionReadsTheFile() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("test", head, nil)

	got := s.read(head)
	s.Empty(got.Unknown)
	s.Equal([]string{"a.go"}, got.Changed)
	s.Empty(got.Unverified)
	s.Empty(got.Runs, "no run claims an unverified file")
}

func (s *ReadSuite) TestACleanRunAtAnOlderRevisionReadsAFileUnchangedSince() {
	older := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n\nvar B = 1\n"})
	s.record("test", older, nil)
	head := s.repo.Commit("again", map[string]string{"b.go": "package a\n\nvar B = 2\n"})

	got := s.read(head)
	s.Empty(got.Unknown)
	s.Equal([]string{"b.go"}, paths(got.Unverified), "a.go is unchanged since the run")
}

func (s *ReadSuite) TestACleanRunAtAnOlderRevisionLeavesAFileChangedSinceUnverified() {
	older := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n\nvar B = 1\n"})
	s.record("test", older, nil)
	head := s.repo.Commit("again", map[string]string{"b.go": "package a\n\nvar B = 2\n"})

	got := s.read(head)
	// The target that ran over the file comes first, then the magusfile targets that claim it.
	s.Equal([]types.UnverifiedFile{{Path: "b.go", Declared: true, Commands: []string{
		hint.Run.With("test", "."), hint.Run.With("lint", "."), hint.Run.With("vet", "."),
	}}}, got.Unverified)
	s.Require().Len(got.Runs, 1)
	s.Equal([]types.RunEvidence{{
		Ref: got.Runs[0].Ref, Project: ".", Target: "test", Revision: older,
		Claims: []string{"a.go", "b.go"}, Reads: []string{"a.go"},
	}}, got.Runs)
}

func (s *ReadSuite) TestARunOverOtherUncommittedEditsReadsAFileTheTreeHoldsAsCommitted() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.write("notes.txt", "uncommitted\n")
	s.record("test", head, func(d *cache.OutputDescriptor) { d.Dirty = true })

	got := s.read(head)
	s.Empty(got.Unknown)
	s.Empty(got.Unverified, "a.go held what the commit holds while the run read it")
}

func (s *ReadSuite) TestARunOverAnEditTheCommitDoesNotHoldDoesNotReadTheFile() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.write("a.go", "package a\n\nvar A = 2\n")
	s.record("test", head, func(d *cache.OutputDescriptor) { d.Dirty = true })

	got := s.read(head)
	s.Equal([]string{"a.go"}, paths(got.Unverified))
	s.Require().Len(got.Runs, 1)
	s.True(got.Runs[0].Dirty)
	s.Empty(got.Runs[0].Reads)
}

func (s *ReadSuite) TestARunOverUncommittedEditsDoesNotReadAFileWrittenSinceItStarted() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("test", head, func(d *cache.OutputDescriptor) {
		d.Dirty = true
		d.TimestampMs = time.Now().Add(-time.Hour).UnixMilli()
	})

	got := s.read(head)
	s.Equal([]string{"a.go"}, paths(got.Unverified), "the commit wrote a.go after the run started")
}

func (s *ReadSuite) TestAFailedRunReadsNothingAndIsListedFailed() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("test", head, func(d *cache.OutputDescriptor) { d.Failed = true })

	got := s.read(head)
	s.Equal([]string{"a.go"}, paths(got.Unverified))
	s.Require().Len(got.Runs, 1)
	s.True(got.Runs[0].Failed)
	s.Empty(got.Runs[0].Reads)
}

func (s *ReadSuite) TestARunWithNoRevisionReadsAFileTheTreeHoldsAsCommitted() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("test", "", nil)

	got := s.read(head)
	s.Empty(got.Unverified)
}

func (s *ReadSuite) TestARunOfATargetThatDeclaresNoInputsReadsNothingItsCommandDoesNotName() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("build", head, nil)

	got := s.read(head)
	s.Equal([]types.UnverifiedFile{{Path: "a.go", Declared: true, Commands: []string{
		hint.Run.With("test", "."), hint.Run.With("lint", "."), hint.Run.With("vet", "."),
	}}}, got.Unverified, "build is never named: no declaration says a.go is its")
	s.Empty(got.Runs, "build claims no file")
}

func (s *ReadSuite) TestARunOfATargetThatDeclaresNoInputsReadsThePathsItsCommandNames() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n\nvar B = 1\n"})
	s.record("build", head, func(d *cache.OutputDescriptor) { d.ExtraArgs = []string{"./a.go"} })

	got := s.read(head)
	s.Equal([]string{"b.go"}, paths(got.Unverified))
}

func (s *ReadSuite) TestADotOnATargetThatDeclaresNoInputsNamesNothing() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("build", head, func(d *cache.OutputDescriptor) { d.ExtraArgs = []string{"."} })

	got := s.read(head)
	s.Equal([]string{"a.go"}, paths(got.Unverified), "`build . -- .` may have done nothing")
}

func (s *ReadSuite) TestAnOptionsValueIsNotANamedPath() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("build", head, func(d *cache.OutputDescriptor) { d.ExtraArgs = []string{"-o", "a.go"} })

	got := s.read(head)
	s.Equal([]string{"a.go"}, paths(got.Unverified), "a.go is the value of -o")
}

func (s *ReadSuite) TestATrailingEllipsisNamesThePackageAndBelow() {
	head := s.repo.Commit("change", map[string]string{"pkg/a.go": "package pkg\n", "pkg/sub/b.go": "package sub\n", "c.go": "package a\n"})
	s.record("build", head, func(d *cache.OutputDescriptor) { d.ExtraArgs = []string{"./pkg/..."} })

	got := s.read(head)
	s.Equal([]string{"c.go"}, paths(got.Unverified))
}

func (s *ReadSuite) TestAGeneratorIsNotNamedForAFileItOnlyReads() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("generate", head, func(d *cache.OutputDescriptor) { d.Failed = true })

	got := s.read(head)
	s.Require().Len(got.Unverified, 1)
	s.True(got.Unverified[0].Declared)
	s.NotContains(got.Unverified[0].Commands, hint.Run.With("generate", "."), "generate writes gen/ and checks nothing about a.go")
}

func (s *ReadSuite) TestAPathThatNamesNothingScopesTheRunToNothing() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("test", head, func(d *cache.OutputDescriptor) { d.ExtraArgs = []string{"./gone"} })

	got := s.read(head)
	s.Equal([]string{"a.go"}, paths(got.Unverified))
	s.Equal([]string{hint.Run.With("test", ".", "--", "a.go")}, got.Unverified[0].Commands[:1])
}

func (s *ReadSuite) TestAnArgSpelledInAnotherCaseNamesNoPath() {
	head := s.repo.Commit("change", map[string]string{"pkg/a.go": "package pkg\n"})
	s.record("test", head, func(d *cache.OutputDescriptor) { d.ExtraArgs = []string{"-run", "PKG"} })

	got := s.read(head)
	s.Empty(got.Unverified, "PKG is a test name, not pkg/, so the run read every Go file")
}

func (s *ReadSuite) TestACachedRunCountsAndIsListedCached() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n\nvar B = 1\n"})
	s.record("test", head, func(d *cache.OutputDescriptor) {
		d.Cached = true
		d.ExtraArgs = []string{"a.go"}
	})

	got := s.read(head)
	s.Equal([]string{"b.go"}, paths(got.Unverified))
	s.Require().Len(got.Runs, 1)
	s.True(got.Runs[0].Cached)
	s.Equal([]string{"a.go"}, got.Runs[0].Reads, "a replay is evidence for the files it names")
}

func (s *ReadSuite) TestAGeneratedOutputIsReadByTheRunThatWroteIt() {
	head := s.repo.Commit("generated", map[string]string{"gen/out.jsonl": "{}\n"})
	s.record("generate:rw", head, nil)

	got := s.read(head)
	s.Empty(got.Unverified)
}

func (s *ReadSuite) TestARunOfATargetWithoutDeclaredOutputsReadsNoOutput() {
	head := s.repo.Commit("generated", map[string]string{"gen/out.jsonl": "{}\n"})
	s.record("test", head, nil)

	got := s.read(head)
	s.Equal([]types.UnverifiedFile{{Path: "gen/out.jsonl", Declared: true, Commands: []string{hint.Run.With("generate", ".")}}}, got.Unverified)
}

func (s *ReadSuite) TestAFileScopedRunReadsOnlyThePathsItNamed() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n\nvar B = 1\n"})
	s.record("test", head, func(d *cache.OutputDescriptor) { d.ExtraArgs = []string{"a.go"} })

	got := s.read(head)
	s.Require().Len(got.Unverified, 1)
	s.Equal("b.go", got.Unverified[0].Path)
	s.Equal([]string{hint.Run.With("test", ".", "--", "b.go")}, got.Unverified[0].Commands[:1])
}

func (s *ReadSuite) TestAFileNoTargetClaimsHasNoCommand() {
	head := s.repo.Commit("licence", map[string]string{"LICENSE": "MIT\n"})

	got := s.read(head)
	s.Equal([]types.UnverifiedFile{{Path: "LICENSE"}}, got.Unverified)
}

func (s *ReadSuite) TestAReadOverNoProjectsNamesEveryFileWithNoCommand() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("test", head, nil)
	in := s.input(s.base, head)
	in.Projects = func(context.Context) (Projects, error) { return NoProjects, nil }

	got := weigh(s.T().Context(), in)
	s.Empty(got.Unknown)
	s.Equal([]types.UnverifiedFile{{Path: "a.go"}}, got.Unverified, "a run of a target no project declares reads nothing")
}

func (s *ReadSuite) TestCommandsNameRunsBeforeMagusfileTargetsAndStopAtThree() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.record("lint", head, func(d *cache.OutputDescriptor) { d.Failed = true })
	s.record("vet", head, func(d *cache.OutputDescriptor) { d.Failed = true })

	got := s.read(head)
	s.Require().Len(got.Unverified, 1)
	s.Equal([]string{hint.Run.With("vet", "."), hint.Run.With("lint", "."), hint.Run.With("test", ".")}, got.Unverified[0].Commands)
}

func (s *ReadSuite) TestChangedFilesAreSinceTheMergeBase() {
	s.repo.Git("branch", "trunk")
	s.repo.Git("checkout", "-q", "-b", "topic")
	head := s.repo.Commit("topic", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.repo.Git("checkout", "-q", "trunk")
	s.repo.Commit("trunk", map[string]string{"b.go": "package a\n\nvar B = 1\n"})
	s.repo.Git("checkout", "-q", "topic")

	got := weigh(s.T().Context(), s.input("trunk", head))
	s.Equal(types.UnverifiedReport{
		Revision: head, Base: "trunk", Changed: []string{"a.go"},
		Unverified: []types.UnverifiedFile{{Path: "a.go", Declared: true, Commands: []string{
			hint.Run.With("test", "."), hint.Run.With("lint", "."), hint.Run.With("vet", "."),
		}}},
	}, got)
}

func (s *ReadSuite) TestAPlatformOnlyGoFileNeedsARunLikeAnyOther() {
	head := s.repo.Commit("plan9", map[string]string{"x_plan9.go": "package a\n"})

	got := s.read(head)
	s.Equal([]string{"x_plan9.go"}, paths(got.Unverified), "no run was recorded")
	s.record("test", head, nil)
	s.Empty(s.read(head).Unverified)
}

func (s *ReadSuite) TestAnEndedContextIsUnknownNamingTheStep() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()

	got := weigh(ctx, s.input(s.base, head))
	s.Contains(got.Unknown, fmt.Sprintf("the files `%s` changed since its merge base with `%s` are unread", head[:12], s.base))
	s.Contains(got.Unknown, "context canceled")
	s.Empty(got.Changed)
	s.Empty(got.Unverified)
}

func (s *ReadSuite) TestAnUnreadableOutputStoreIsUnknown() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	in := s.input(s.base, head)
	in.Runs = func() ([]cache.OutputDescriptor, error) { return nil, errors.New("permission denied") }

	got := weigh(s.T().Context(), in)
	s.Equal("the output store is unread: permission denied", got.Unknown)
	s.Empty(got.Changed)
}

func (s *ReadSuite) TestAWorkspaceThatDidNotOpenIsUnknown() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	in := s.input(s.base, head)
	in.Projects = func(context.Context) (Projects, error) { return nil, errors.New("no magusfile") }

	got := weigh(s.T().Context(), in)
	s.Equal("the workspace did not open, so no target's inputs are known: no magusfile", got.Unknown)
}

func (s *ReadSuite) TestNoChangedFilesReadsNeitherTheWorkspaceNorTheStore() {
	in := s.input(s.base, s.base)
	in.Projects = func(context.Context) (Projects, error) { return nil, errors.New("opened") }
	in.Runs = func() ([]cache.OutputDescriptor, error) { return nil, errors.New("read") }

	got := weigh(s.T().Context(), in)
	s.Empty(got.Unknown)
	s.Empty(got.Changed)
}

// gitCalls puts a git on PATH that logs each argv, one per line, before running the real
// one.
func (s *ReadSuite) gitCalls() func() []string {
	if runtime.GOOS == "windows" {
		s.T().Skip("the logging git is a shell script")
	}
	real, err := exec.LookPath("git")
	s.Require().NoError(err)
	bin := s.T().TempDir()
	calls := filepath.Join(bin, "calls")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexec %q \"$@\"\n", calls, real)
	s.Require().NoError(os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755))
	s.T().Setenv("PATH", fmt.Sprintf("%s%c%s", bin, os.PathListSeparator, os.Getenv("PATH")))
	return func() []string {
		b, err := os.ReadFile(calls)
		if os.IsNotExist(err) {
			return nil
		}
		s.Require().NoError(err)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

// A git process per changed file spent a 733-file branch's whole deadline.
func (s *ReadSuite) TestAPushOfManyChangedFilesIsReadInAFewGitProcesses() {
	files := map[string]string{}
	for i := range 400 {
		files[fmt.Sprintf("f%03d.go", i)] = fmt.Sprintf("package a\n\nvar F%d = %d\n", i, i)
	}
	head := s.repo.Commit("many", files)
	s.record("test", head, nil)
	older := s.repo.Commit("older", map[string]string{"f000.go": "package a\n"})
	s.record("lint", older, nil)
	head = s.repo.Commit("again", map[string]string{"f000.go": files["f000.go"]})
	s.record("test", head, nil)
	calls := s.gitCalls()

	got := s.read(head)
	s.Less(len(calls()), 20, "git processes for 400 files at two revisions")
	s.Empty(got.Unknown)
	s.Empty(got.Unverified)
}

// largeWorkspace answers the two reads evidence reads of a workspace.
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

// BenchmarkEvidenceWeighsALargePush weighs each changed file as
// weigh does. The repository holds the commit and no run's
// revision, so the rest measures matching files to runs.
func BenchmarkEvidenceWeighsALargePush(b *testing.B) {
	ws, runs, files, dir := largePush(b)
	driver := typestest.NewMockVCSDriver(b)
	driver.On("FindCommit", mock.Anything, dir, "c1").Return(types.Commit{ID: "c1"}, nil)
	driver.On("FindCommit", mock.Anything, dir, mock.Anything).Return(types.Commit{}, os.ErrNotExist)
	ctx := b.Context()
	for b.Loop() {
		ev := newEvidence(driver, dir, dir, ws, runs, "c1")
		if err := ev.readRevisions(ctx, files); err != nil {
			b.Fatal(err)
		}
		for _, file := range files {
			if !ev.covered(file) {
				ev.wouldCover(file)
			}
		}
	}
}
