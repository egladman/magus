package magus

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// scopeAtBase is scope less the entries tree does not hold, as head's scope
// may name a test file the change added. A scope left with none is a reason.
func scopeAtBase(tree, project string, scope []string) ([]string, string) {
	kept := existingEntries(filepath.Join(tree, filepath.FromSlash(project)), scope)
	if len(scope) > 0 && len(kept) == 0 {
		return nil, "no test_scope entry names a file at base"
	}
	return kept, ""
}

// existingEntries is the entries naming a file or dir under dir.
func existingEntries(dir string, entries []string) []string {
	return slices.DeleteFunc(slices.Clone(entries), func(entry string) bool {
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(entry)))
		return err != nil
	})
}

// subjectScope is test's TestScope for name, as args relative to project.
// An entry naming no file is dropped. A declared scope left with none is a
// reason: running the whole project instead would hide it.
func subjectScope(root, project, name string, test spells.Command) ([]string, string) {
	dir := filepath.Join(root, filepath.FromSlash(project))
	scope := existingEntries(dir, test.ExpandTestScope(projectRelative(project, name)))
	if len(test.TestScope) > 0 && len(scope) == 0 {
		return nil, fmt.Sprintf("no test_scope entry names a file for %s", name)
	}
	return scope, ""
}

func projectRelative(project, name string) string {
	if project == "." {
		return name
	}
	return strings.TrimPrefix(name, project+"/")
}

func projectLinks(project string, dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, path.Join(project, d))
	}
	return out
}

// workspaceLinks is every project's ignore dirs, such as node_modules. A test
// in one project may load a tool another project installed.
func (m *Magus) workspaceLinks() []string {
	dirs := map[string][]string{}
	for _, p := range m.ws.All() {
		for _, sp := range p.ResolvedSpells {
			dirs[p.Path] = append(dirs[p.Path], sp.IgnoreDirs()...)
		}
	}
	return ignoreLinks(dirs)
}

// ignoreLinks is each project's dirs as workspace paths, sorted and unique.
func ignoreLinks(dirs map[string][]string) []string {
	out := make([]string, 0, len(dirs))
	for project, d := range dirs {
		out = append(out, projectLinks(project, d)...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// testRun is what one scoped run did. A cached run replayed an answer and
// ran no test.
type testRun struct{ passed, cached bool }

// scratch runs targets in copies of the tree: this magus, the store holding
// the copies, and the version control exporting them.
type scratch struct {
	root  string
	vcs   types.VCSDriver
	store string
	bin   string
}

func (m *Magus) newScratch(ctx context.Context) (scratch, error) {
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil || res.VCS == nil {
		return scratch{}, types.DiagnosticErrorf(types.VCSCapabilityMissing,
			"version control is disabled, so magus cannot copy the tree to measure it")
	}
	store, err := config.StoreDir(m.ws.Root, m.cfg)
	if err != nil {
		return scratch{}, err
	}
	bin, err := os.Executable()
	if err != nil {
		return scratch{}, err
	}
	return scratch{root: m.ws.Root, vcs: res.VCS, store: store, bin: bin}, nil
}

// copier exports rev, or HEAD with the working tree's edits when rev is "".
func (sc scratch) copier(rev string, links []string) treeCopier {
	return treeCopier{root: sc.root, trees: filepath.Join(sc.store, scratchTreesDir), vcs: sc.vcs, links: links, rev: rev}
}

func (sc scratch) copy(ctx context.Context, rev string, links []string) (string, error) {
	return sc.copier(rev, links).copy(ctx)
}

// magusRunner runs a scoped target through bin, a magus, in a scratch copy.
// It judges from the target's own result record: an exit status cannot tell
// a failed test from a run that never reached one.
type magusRunner struct{ bin string }

// scopedRun runs target over c's scope in c's project, inside dir, a scratch
// copy.
type scopedRun func(ctx context.Context, dir, target string, c coverageScope) (testRun, error)

// errNoResult marks a run that recorded no result for its target: the tree
// did not compile or load, or the run stopped before any step.
var errNoResult = errors.New("so no test ran")

// errNoSeat marks a run the scheduler could not seat. It measured nothing,
// and is an error for a caller to see, not a gap to report.
var errNoSeat = errors.New("no seat")

func (r magusRunner) run(ctx context.Context, dir, target string, s coverageScope) (testRun, error) {
	args := slices.Concat([]string{"run", target, "--no-cache", "--no-volatility-retry", "-o", "jsonl", s.project, "--"}, s.scope)
	opts := spawn.ExecOptions{Dir: dir, Env: []string{copyCacheEnv(dir)}, Capture: true, Quiet: true}
	res, err := spawn.Exec(ctx, r.bin, args, opts)
	line := strings.Join(slices.Concat([]string{r.bin}, args), " ")
	if !res.Started {
		return testRun{}, fmt.Errorf("%s did not start: %w", line, err)
	}
	if res.Code == exit.TempFail {
		return testRun{}, fmt.Errorf("%s exited %d: the run found no seat or its project held, so it ran no test: %w", line, res.Code, errNoSeat)
	}
	result, ok := recordedResult(res.Stdout, s.project, target)
	if !ok && res.Code < 0 {
		return testRun{}, fmt.Errorf("%s was signaled with no result for %s: %s", line, target, lastLine(res.Stderr))
	}
	if !ok {
		return testRun{}, fmt.Errorf("%s exited %d with no result for %s (%s): %w", line, res.Code, target, lastLine(res.Stderr), errNoResult)
	}
	return testRun{passed: result.Status != targetFailed, cached: result.CacheHit}, nil
}

// targetFailed is the Status a report.TargetResult carries for a step that
// failed.
const targetFailed = "failed"

// recordedResult is the result stdout records for target in project. A
// record names the op without its spell.
func recordedResult(stdout, project, target string) (report.TargetResult, bool) {
	_, op, _ := strings.Cut(target, "::")
	for _, line := range strings.Split(stdout, "\n") {
		r, ok := decodeTargetResult(line)
		if ok && r.Project == project && r.Target == op {
			return r, true
		}
	}
	return report.TargetResult{}, false
}

func decodeTargetResult(line string) (report.TargetResult, bool) {
	rec, err := report.ParseLine([]byte(line))
	if err != nil || rec.Type != report.TypeTargetResult {
		return report.TargetResult{}, false
	}
	var r report.TargetResult
	return r, rec.Decode(&r) == nil
}

func lastLine(text string) string {
	text = strings.TrimSpace(text)
	return text[strings.LastIndexByte(text, '\n')+1:]
}

// copyCacheDir is the cache dir of the magus run in a scratch copy. Pinning
// it keeps each copy's entries and coverage report its own.
func copyCacheDir(dir string) string { return filepath.Join(dir, ".magus") }

func copyCacheEnv(dir string) string {
	return fmt.Sprintf("%s=%s", config.EnvName("MAGUS", "cache", "dir"), copyCacheDir(dir))
}

// scratchReport is the report c's coverage run left in dir. The copy is read
// as a workspace of its own, as ChangeRisk reads a kept report.
func scratchReport(dir string, c coverageScope) ([]coverage.File, error) {
	data, err := os.ReadFile(filepath.Join(coverage.Dir(copyCacheDir(dir), c.project), c.coverage.File))
	if err != nil {
		return nil, fmt.Errorf("its coverage run left no report: %w", err)
	}
	scratch := &Magus{ws: &types.Workspace{Root: dir}}
	files, err := scratch.parseReport(c.project, c.coverage, data)
	if err != nil {
		return nil, fmt.Errorf("its coverage run's report: %w", err)
	}
	return files, nil
}

func hitLines(files []coverage.File, name string) map[int]bool {
	hit := fileNamed(files, name).Lines()
	maps.DeleteFunc(hit, func(_ int, ran bool) bool { return !ran })
	return hit
}

// fileNamed is the report's file at name, or an empty one.
func fileNamed(files []coverage.File, name string) coverage.File {
	i := slices.IndexFunc(files, func(f coverage.File) bool { return f.Path == name })
	if i < 0 {
		return coverage.File{Path: name}
	}
	return files[i]
}

// scratchTreesDir holds the scratch copies inside the workspace store. A
// sandboxed gate may write the store but not the user cache.
const scratchTreesDir = "scratch-trees"

// treeCopier exports HEAD into a new dir under trees and lays the working
// tree's uncommitted edits over it. A set rev is exported alone. links are
// workspace-relative dirs, such as node_modules, linked rather than copied; a
// dir the tests write is not one.
type treeCopier struct {
	root  string
	trees string
	vcs   types.VCSDriver
	links []string
	rev   string
}

func (c treeCopier) copy(ctx context.Context) (string, error) {
	if err := os.MkdirAll(c.trees, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(c.trees, "")
	if err != nil {
		return "", err
	}
	if err := c.export(ctx, dir); err != nil {
		return dir, err
	}
	if c.rev == "" {
		if err := c.overlay(ctx, dir); err != nil {
			return dir, err
		}
	}
	return dir, linkDirs(c.root, dir, c.links)
}

func (c treeCopier) export(ctx context.Context, dir string) error {
	rev := c.rev
	if rev == "" {
		head, err := c.vcs.FindCommit(ctx, c.root, "")
		if err != nil {
			return err
		}
		rev = head.ID
	}
	return c.vcs.ExportRevision(ctx, c.root, rev, dir)
}

// overlay copies each uncommitted path into dir, removing one the tree
// deleted. DirtyFiles names paths from the repository root, which may sit
// above the workspace.
func (c treeCopier) overlay(ctx context.Context, dir string) error {
	repo, err := c.vcs.Root(ctx, c.root)
	if err != nil {
		return err
	}
	dirty, err := c.vcs.DirtyFiles(ctx, c.root, nil)
	if err != nil {
		return err
	}
	for _, p := range dirty {
		if err := overlayFile(c.root, dir, filepath.Join(repo, filepath.FromSlash(p))); err != nil {
			return err
		}
	}
	return nil
}

// overlayFile mirrors abs, a path in the working tree, into dir. A path
// outside root is not the workspace's, so it is skipped.
func overlayFile(root, dir, abs string) error {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	data, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return removeIfPresent(filepath.Join(dir, rel))
	}
	if err != nil {
		return err
	}
	return writeCopy(filepath.Join(dir, rel), data, abs)
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeCopy(dst string, data []byte, src string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

// linkDirs links each of links that root holds into dir. One the copy
// already holds is tracked, so the export wrote the right copy of it.
func linkDirs(root, dir string, links []string) error {
	for _, l := range links {
		src := filepath.Join(root, filepath.FromSlash(l))
		dst := filepath.Join(dir, filepath.FromSlash(l))
		if !exists(src) || exists(dst) {
			continue
		}
		if err := os.Symlink(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
