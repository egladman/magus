package magus

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

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

// testRun is what one run in a copy did. A cached run replayed an answer
// and ran no test. failure is what a failed run's result says went wrong.
type testRun struct {
	passed, cached bool
	failure        string
}

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

func (sc scratch) copy(ctx context.Context, rev string, links []string) (string, error) {
	trees := filepath.Join(sc.store, scratchTreesDir)
	return treeCopier{root: sc.root, trees: trees, vcs: sc.vcs, links: links, rev: rev}.copy(ctx)
}

// magusRunner runs a target through bin, a magus, in a scratch copy. It
// judges from the target's own result record: an exit status cannot tell a
// failed test from a run that never reached one.
type magusRunner struct{ bin string }

// errNoResult marks a run that recorded no result for its target: the tree
// did not compile or load, or the run stopped before any step.
var errNoResult = errors.New("so no test ran")

// errNoSeat marks a run the scheduler could not seat. It measured nothing,
// and is an error for a caller to see, not a gap to report.
var errNoSeat = errors.New("no seat")

// run runs c's target in dir. The copy's cache is its own, so only the
// remote tier can answer it from an earlier run.
func (r magusRunner) run(ctx context.Context, dir string, c coverageSource) (testRun, error) {
	args := []string{"run", c.target, "--no-volatility-retry", "-o", "jsonl", c.project}
	opts := spawn.ExecOptions{Dir: dir, Env: []string{copyCacheEnv(dir)}, Capture: true, Quiet: true}
	res, err := spawn.Exec(ctx, r.bin, args, opts)
	line := strings.Join(slices.Concat([]string{r.bin}, args), " ")
	if !res.Started {
		return testRun{}, fmt.Errorf("%s did not start: %w", line, err)
	}
	if res.Code == exit.TempFail {
		return testRun{}, fmt.Errorf("%s exited %d: the run found no seat or its project held, so it ran no test: %w", line, res.Code, errNoSeat)
	}
	result, ok := recordedResult(res.Stdout, c.project, c.target)
	if !ok && res.Code < 0 {
		return testRun{}, fmt.Errorf("%s was signaled with no result for %s: %s", line, c.target, lastLine(res.Stderr))
	}
	if !ok {
		return testRun{}, fmt.Errorf("%s exited %d with no result for %s (%s): %w", line, res.Code, c.target, lastLine(res.Stderr), errNoResult)
	}
	run := testRun{passed: result.Status != targetFailed, cached: result.CacheHit}
	if !run.passed {
		run.failure = cmp.Or(result.Error, lastLine(res.Stderr))
	}
	return run, nil
}

// targetFailed is the Status a report.TargetResult carries for a step that
// failed.
const targetFailed = "failed"

// recordedResult is the result stdout records for target in project. A
// record names an op without its spell, so spell::op matches by its op.
func recordedResult(stdout, project, target string) (report.TargetResult, bool) {
	if _, op, qualified := strings.Cut(target, "::"); qualified {
		target = op
	}
	for _, line := range strings.Split(stdout, "\n") {
		r, ok := decodeTargetResult(line)
		if ok && r.Project == project && r.Target == target {
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

// copyCacheDir is the cache dir of the magus run in a scratch copy. A
// sandboxed gate may write the store the copy sits in, not the user cache.
func copyCacheDir(dir string) string { return filepath.Join(dir, ".magus") }

func copyCacheEnv(dir string) string {
	return fmt.Sprintf("%s=%s", config.EnvName("MAGUS", "cache", "dir"), copyCacheDir(dir))
}

// fileNamed is the report's file at name, or an empty one.
func fileNamed(files []coverage.File, name string) coverage.File {
	i := slices.IndexFunc(files, func(f coverage.File) bool { return f.Path == name })
	if i < 0 {
		return coverage.File{Path: name}
	}
	return files[i]
}

// baseProfileVersion keys the kept base profiles; bump it when what a
// profile holds changes meaning.
const baseProfileVersion = "1"

// keptProfilesDir holds the base profiles inside the workspace store, which
// every checkout shares.
const keptProfilesDir = "change-risk"

// baseProfiles reads coverage profiles at one base commit. Each is kept in
// the workspace store by commit, so only the first read runs its target.
type baseProfiles struct {
	commit string
	// kept is the dir holding the kept profiles.
	kept  string
	parse func(c coverageSource, data []byte) ([]coverage.File, error)
	// copy exports the base tree; run runs a source's target inside it.
	copy func(ctx context.Context) (string, error)
	run  func(ctx context.Context, dir string, c coverageSource) (testRun, error)
	// start is when the measure began, for the elapsed time a stop names.
	start time.Time
	dir   string
}

func (m *Magus) newBaseProfiles(ctx context.Context, commit string, start time.Time) (*baseProfiles, error) {
	sc, err := m.newScratch(ctx)
	if err != nil {
		return nil, err
	}
	links := m.workspaceLinks()
	return &baseProfiles{
		commit: commit, kept: filepath.Join(sc.store, keptProfilesDir), start: start,
		parse: func(c coverageSource, data []byte) ([]coverage.File, error) {
			return m.parseReport(c.project, c.coverage, data)
		},
		copy: func(ctx context.Context) (string, error) { return sc.copy(ctx, commit, links) },
		run:  magusRunner{bin: sc.bin}.run,
	}, nil
}

func (b *baseProfiles) entry(c coverageSource) string {
	h := sha256.New()
	for _, part := range []string{baseProfileVersion, c.project, c.spell, c.target, string(c.coverage.Format), c.coverage.Profile} {
		fmt.Fprintf(h, "%s\x00", part)
	}
	return filepath.Join(b.kept, fmt.Sprintf("%s-%x", b.commit, h.Sum(nil)[:8]))
}

// read is c's profile at the base commit. A miss says so and runs c's
// target once in a copy of the base, then keeps what it left.
func (b *baseProfiles) read(ctx context.Context, c coverageSource) (profileRead, error) {
	entry := b.entry(c)
	data, err := os.ReadFile(entry)
	switch {
	case err == nil:
		return b.parsed(c, data), nil
	case !errors.Is(err, fs.ErrNotExist):
		return profileRead{}, fmt.Errorf("change risk: the kept base profile: %w", err)
	}
	slog.WarnContext(ctx, "change risk: no coverage profile is kept for the base, so its target runs there once",
		slog.String("base", b.commit), slog.String("command", c.command()))
	data, reason, err := b.runAtBase(ctx, c)
	if err != nil || reason != "" {
		return profileRead{reason: reason}, err
	}
	if err := os.MkdirAll(b.kept, 0o700); err != nil {
		return profileRead{}, fmt.Errorf("change risk: %w", err)
	}
	if err := file.WriteFileAtomic(entry, data, 0o600); err != nil {
		return profileRead{}, fmt.Errorf("change risk: keep the base profile: %w", err)
	}
	return b.parsed(c, data), nil
}

// runAtBase is the profile c's target leaves in the base copy. A red or
// unreadable run is a reason. A stop, or a run with no seat, is an error.
func (b *baseProfiles) runAtBase(ctx context.Context, c coverageSource) ([]byte, string, error) {
	doing := fmt.Sprintf("running `%s` at base %s", c.command(), b.commit)
	if b.dir == "" {
		dir, err := b.copy(ctx)
		b.dir = dir
		if err != nil {
			err = fmt.Errorf("change risk: scratch copy: %w", err)
			return nil, "", stopped(ctx, "change risk", b.start, "copying base "+b.commit, err)
		}
	}
	res, err := b.run(ctx, b.dir, c)
	switch {
	case ctx.Err() != nil:
		return nil, "", stopped(ctx, "change risk", b.start, doing, ctx.Err())
	case errors.Is(err, errNoSeat):
		return nil, "", fmt.Errorf("change risk: %w", err)
	case err != nil:
		return nil, err.Error(), nil
	case !res.passed:
		return nil, strings.TrimSuffix(fmt.Sprintf("a test fails at base %s: `%s`: %s", b.commit, c.command(), res.failure), ": "), nil
	}
	data, err := os.ReadFile(filepath.Join(b.dir, filepath.FromSlash(c.project), filepath.FromSlash(c.coverage.Profile)))
	if err != nil {
		return nil, fmt.Sprintf("`%s` at base %s left no %s", c.command(), b.commit, c.coverage.Profile), nil
	}
	return data, "", nil
}

func (b *baseProfiles) parsed(c coverageSource, data []byte) profileRead {
	files, err := b.parse(c, data)
	if err != nil {
		return profileRead{reason: fmt.Sprintf("the coverage profile of %s at base %s: %s", c.project, b.commit, err)}
	}
	return profileRead{files: files}
}

// remove deletes the base copy, if a read made one.
func (b *baseProfiles) remove() {
	if b.dir != "" {
		_ = os.RemoveAll(b.dir)
	}
}

// scratchTreesDir holds the scratch copies inside the workspace store. A
// sandboxed gate may write the store but not the user cache.
const scratchTreesDir = "scratch-trees"

// treeCopier exports rev into a new dir under trees. links are
// workspace-relative dirs, such as node_modules, linked rather than copied;
// a dir the tests write is not one.
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
	if err := c.vcs.ExportRevision(ctx, c.root, c.rev, dir); err != nil {
		return dir, err
	}
	return dir, linkDirs(c.root, dir, c.links)
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

// freshProfile is a profile one run brought current. A magus nested in the
// same run reads it as it stands: its own probe keys the target from another
// process, can miss, and a run there is refused MGS3007.
type freshProfile struct {
	Project string `json:"project"`
	Profile string `json:"profile"`
	Digest  string `json:"digest"`
}

// freshProfiles is what the run ctx belongs to brought current, as JSON Lines
// in the workspace store. file is "" outside a run, which vouches for nothing.
type freshProfiles struct {
	root string
	file string
}

func (m *Magus) freshProfiles(ctx context.Context) freshProfiles {
	run := types.InvocationRoot(ctx)
	store, err := config.StoreDir(m.ws.Root, m.cfg)
	if run == "" || err != nil {
		return freshProfiles{root: m.ws.Root}
	}
	sum := sha256.Sum256([]byte(run))
	return freshProfiles{root: m.ws.Root, file: filepath.Join(store, keptProfilesDir, fmt.Sprintf("fresh-%x.jsonl", sum[:8]))}
}

func (f freshProfiles) digest(c coverageSource) (string, bool) {
	data, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(c.project), filepath.FromSlash(c.coverage.Profile)))
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), true
}

// vouches reports whether this run brought c's profile current and the
// profile on disk is still the one it left. The last line per profile wins.
func (f freshProfiles) vouches(c coverageSource) bool {
	if f.file == "" {
		return false
	}
	lines, err := file.ReadLines(f.file)
	if err != nil {
		return false
	}
	recorded := ""
	for _, line := range lines.Lines {
		var e freshProfile
		if json.Unmarshal(line.Text, &e) == nil && e.Project == c.project && e.Profile == c.coverage.Profile {
			recorded = e.Digest
		}
	}
	now, ok := f.digest(c)
	return ok && recorded == now
}

// record appends c's profile as this run left it. A profile the target did
// not write is not recorded; reading it says so.
func (f freshProfiles) record(c coverageSource) error {
	now, ok := f.digest(c)
	if f.file == "" || !ok {
		return nil
	}
	line, err := json.Marshal(freshProfile{Project: c.project, Profile: c.coverage.Profile, Digest: now})
	if err != nil {
		return err
	}
	return file.AppendLines(f.file, [][]byte{line}, 0o600)
}
