package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/benchmarks/agent/internal/bench"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/xdg"
)

const (
	historyLicense = "MIT"
	historyRepo    = "egladman/magus"
	// A commit wider than this is a sweep, not a task an agent can be graded on.
	maxBugfixFiles       = 12
	maxRegenerationFiles = 30
	// Interrogation bases are spread over the history at least this many commits apart.
	interrogationStride = 25
	// An answer set outside these bounds is too small to score recall on, or too large
	// for a list an agent writes out.
	minAnswer, maxAnswer = 3, 60
)

type historyOptions struct {
	repo, ref, after, out string
	want                  counts
}

type counts struct{ bugfix, interrogation, regeneration int }

func (c counts) total() int { return c.bugfix + c.interrogation + c.regeneration }

type commit struct {
	sha, parent, created, message string
}

// change is a commit's diff split the way a task needs it.
type change struct {
	kind bench.TaskKind
	// sources are root-module non-test Go files, tests the root-module test files and
	// testdata, generated the paths under a gen directory.
	sources, tests, generated []string
}

func writeHistory(ctx context.Context, o historyOptions) error {
	var missing []string
	for flag, v := range map[string]string{"--repo": o.repo, "--ref": o.ref, "--after": o.after, "-o": o.out} {
		if v == "" {
			missing = append(missing, flag)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("history: %s required", strings.Join(missing, ", "))
	}
	if _, err := time.Parse(time.DateOnly, o.after); err != nil {
		return fmt.Errorf("history: --after: %w", err)
	}
	tip, err := command(ctx, o.repo, nil, "git", "rev-parse", "--verify", o.ref+"^{commit}")
	if err != nil {
		return err
	}
	tip = strings.TrimSpace(tip)
	commits, err := firstParentCommits(ctx, o.repo, tip, o.after)
	if err != nil {
		return err
	}
	dir, err := xdg.ScratchCheckoutDir("benchtasks")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := (bench.Task{ID: "benchtasks-scratch", BaseCommit: tip}).CutClone(ctx, o.repo, dir); err != nil {
		return err
	}
	m := &miner{dir: dir, env: testEnv(os.Environ()), log: os.Stderr}
	m.module, err = modulePath(ctx, dir, tip)
	if err != nil {
		return err
	}
	tasks, got := m.mine(ctx, commits, o.want)
	if err := writeAndReload(o.out, tasks); err != nil {
		return err
	}
	if got != o.want {
		return fmt.Errorf("wrote %d tasks to %s but wanted %+v and found %+v among %d commits after %s; move --after earlier",
			len(tasks), o.out, o.want, got, len(commits), o.after)
	}
	return nil
}

func firstParentCommits(ctx context.Context, repo, tip, after string) ([]commit, error) {
	out, err := command(ctx, repo, nil, "git", "log", "--first-parent", "--after="+after,
		"--format=%H%x00%P%x00%cI%x00%B%x1e", tip)
	if err != nil {
		return nil, err
	}
	var commits []commit
	for rec := range strings.SplitSeq(out, "\x1e") {
		f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x00", 4)
		if len(f) != 4 {
			continue
		}
		parents := strings.Fields(f[1])
		if len(parents) == 0 {
			continue
		}
		created, err := time.Parse(time.RFC3339, f[2])
		if err != nil {
			return nil, fmt.Errorf("%s: committer date: %w", f[0], err)
		}
		commits = append(commits, commit{sha: f[0], parent: parents[0], created: created.UTC().Format(time.RFC3339), message: prompt(f[3])})
	}
	return commits, nil
}

// prompt is a commit message without its trailers, which name tools and people rather than
// the problem.
func prompt(message string) string {
	var keep []string
	for line := range strings.SplitSeq(strings.TrimSpace(message), "\n") {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "co-authored-by:") || strings.HasPrefix(lower, "signed-off-by:") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// classify splits a commit's changed paths. nested are the directories of the modules
// nested in the repository, whose packages a root-module `go test` does not reach.
func classify(files, nested []string) change {
	var c change
	var otherSources int
	for _, f := range files {
		root := !slices.ContainsFunc(nested, func(dir string) bool { return strings.HasPrefix(f, dir+"/") })
		switch {
		case isTestPath(f):
			if root {
				c.tests = append(c.tests, f)
			}
		case slices.Contains(strings.Split(path.Dir(f), "/"), "gen"):
			c.generated = append(c.generated, f)
		case root && strings.HasSuffix(f, ".go"):
			c.sources = append(c.sources, f)
		default:
			otherSources++
		}
	}
	switch {
	case len(c.generated) > 0 && len(c.sources)+otherSources > 0 && len(files) <= maxRegenerationFiles:
		c.kind = bench.TaskRegeneration
	case len(c.generated) == 0 && len(c.sources) > 0 && len(c.tests) > 0 && len(files) <= maxBugfixFiles:
		c.kind = bench.TaskBugfix
	case len(c.sources) > 0:
		c.kind = bench.TaskInterrogation
	}
	return c
}

func isTestPath(p string) bool {
	return strings.HasSuffix(p, "_test.go") || slices.Contains(strings.Split(p, "/"), "testdata")
}

// slug is the task ID a subject line gives: lower case, runs of anything else joined by
// one dash, cut at a dash to at most 48 bytes.
func slug(subject string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(subject) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 48 {
		s = s[:48]
		if i := strings.LastIndexByte(s, '-'); i > 0 {
			s = s[:i]
		}
	}
	return s
}

// testEnv is env without the variables an agent session or magus run sets, which would
// change what the mined repository's own tests do.
func testEnv(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		return strings.HasPrefix(kv, "MAGUS_") || strings.HasPrefix(kv, "CLAUDE")
	})
}

func modulePath(ctx context.Context, dir, rev string) (string, error) {
	gomod, err := command(ctx, dir, nil, "git", "show", rev+":go.mod")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(gomod, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", fmt.Errorf("%s:go.mod names no module", rev)
}

type miner struct {
	dir, module string
	env         []string
	log         io.Writer
	ids         map[string]bool
}

func (m *miner) mine(ctx context.Context, commits []commit, want counts) ([]bench.Task, counts) {
	var tasks []bench.Task
	var got counts
	m.ids = map[string]bool{}
	sinceInterrogation := interrogationStride
	for _, c := range commits {
		if got == want || ctx.Err() != nil {
			break
		}
		sinceInterrogation++
		ch, err := m.change(ctx, c)
		if err != nil {
			fmt.Fprintf(m.log, "skip %.12s: %v\n", c.sha, err)
			continue
		}
		var t bench.Task
		switch {
		case ch.kind == bench.TaskBugfix && got.bugfix < want.bugfix:
			t, err = m.bugfix(ctx, c, ch)
		case ch.kind == bench.TaskRegeneration && got.regeneration < want.regeneration:
			t, err = m.regeneration(ctx, c, ch)
		case ch.kind != "" && got.interrogation < want.interrogation && sinceInterrogation >= interrogationStride:
			t, err = m.interrogation(ctx, c, ch, got.interrogation%2 == 1)
		default:
			continue
		}
		if err != nil {
			fmt.Fprintf(m.log, "skip %.12s %s: %v\n", c.sha, ch.kind, err)
			continue
		}
		t.ID = m.uniqueID(t.ID, c.sha)
		tasks = append(tasks, t)
		switch t.Kind {
		case bench.TaskBugfix:
			got.bugfix++
		case bench.TaskRegeneration:
			got.regeneration++
		case bench.TaskInterrogation:
			got.interrogation++
			sinceInterrogation = 0
		}
		fmt.Fprintf(m.log, "kept %s %s (%d of %d)\n", t.Kind, t.ID, got.total(), want.total())
	}
	return tasks, got
}

func (m *miner) uniqueID(id, sha string) string {
	if m.ids[id] {
		id += "-" + sha[:7]
	}
	m.ids[id] = true
	return id
}

func (m *miner) change(ctx context.Context, c commit) (change, error) {
	files, err := m.lines(ctx, "git", "diff", "--no-renames", "--name-only", c.parent, c.sha)
	if err != nil {
		return change{}, err
	}
	all, err := m.lines(ctx, "git", "ls-tree", "-r", "--name-only", c.sha)
	if err != nil {
		return change{}, err
	}
	var nested []string
	for _, f := range all {
		if dir, ok := strings.CutSuffix(f, "/go.mod"); ok {
			nested = append(nested, dir)
		}
	}
	return classify(files, nested), nil
}

func (m *miner) lines(ctx context.Context, name string, args ...string) ([]string, error) {
	out, err := command(ctx, m.dir, nil, name, args...)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func (m *miner) base(c commit, kind bench.TaskKind) bench.Task {
	subject, _, _ := strings.Cut(c.message, "\n")
	return bench.Task{
		Suite:      bench.SuiteHistoryGo,
		ID:         slug(subject),
		Kind:       kind,
		Tag:        bench.TagCapability,
		Created:    c.created,
		Repo:       historyRepo,
		License:    historyLicense,
		BaseCommit: c.parent,
		FixCommit:  c.sha,
		Prompt:     c.message,
	}
}

// diffs splits the commit's diff into the reference change and the held-out tests.
func (m *miner) diffs(ctx context.Context, c commit) (patch, tests string, err error) {
	testSpecs := []string{":(glob)**/*_test.go", ":(glob)**/testdata/**"}
	patch, err = command(ctx, m.dir, nil, "git", append([]string{"diff", "--binary", "--no-renames", c.parent, c.sha, "--", ".",
		":(exclude,glob)**/*_test.go", ":(exclude,glob)**/testdata/**"})...)
	if err != nil {
		return "", "", err
	}
	tests, err = command(ctx, m.dir, nil, "git", append([]string{"diff", "--binary", "--no-renames", c.parent, c.sha, "--"}, testSpecs...)...)
	return patch, tests, err
}

func (m *miner) bugfix(ctx context.Context, c commit, ch change) (bench.Task, error) {
	t := m.base(c, bench.TaskBugfix)
	var err error
	if t.Patch, t.TestPatch, err = m.diffs(ctx, c); err != nil {
		return t, err
	}
	t.Packages = m.packages(ch.tests)
	if t.FailToPass, err = m.verifyTests(ctx, c, ch.tests, t.Packages, t.TestPatch); err != nil {
		return t, err
	}
	if len(t.FailToPass) == 0 {
		return t, errors.New("no changed test fails at the parent and passes at the fix")
	}
	return t, nil
}

func (m *miner) regeneration(ctx context.Context, c commit, ch change) (bench.Task, error) {
	t := m.base(c, bench.TaskRegeneration)
	var err error
	if t.Patch, t.TestPatch, err = m.diffs(ctx, c); err != nil {
		return t, err
	}
	t.Generated = ch.generated
	t.Packages = m.packages(append(slices.Clone(ch.sources), ch.tests...))
	if len(t.Packages) == 0 {
		return t, nil
	}
	if t.TestPatch != "" {
		t.FailToPass, err = m.verifyTests(ctx, c, ch.tests, t.Packages, t.TestPatch)
		if !errors.Is(err, errNoTests) {
			return t, err
		}
	}
	_, err = m.goTest(ctx, c.sha, "", t.Packages)
	return t, err
}

// packages are the import paths of the root-module directories holding files, sorted.
func (m *miner) packages(files []string) []string {
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		dir := path.Dir(f)
		if dir == "." {
			out = append(out, m.module)
		} else {
			out = append(out, m.module+"/"+dir)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// verifyTests runs the packages' whole test suites at the fix, which must pass, then the
// changed tests at the parent with testPatch applied, and returns the changed tests that
// passed at the fix and not at the parent.
func (m *miner) verifyTests(ctx context.Context, c commit, tests, pkgs []string, testPatch string) ([]string, error) {
	changed, err := m.changedTests(ctx, c, tests)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return nil, errNoTests
	}
	fix, err := m.goTest(ctx, c.sha, "", pkgs)
	if err != nil {
		return nil, err
	}
	if err := m.checkout(ctx, c.parent); err != nil {
		return nil, err
	}
	if err := m.apply(ctx, testPatch); err != nil {
		return nil, err
	}
	parent, err := m.runTests(ctx, runPattern(changed), pkgs)
	if err != nil {
		return nil, err
	}
	return failToPass(changed, parent, fix), nil
}

func (m *miner) changedTests(ctx context.Context, c commit, tests []string) ([]string, error) {
	dirs := map[string]bool{}
	for _, f := range tests {
		if strings.HasSuffix(f, "_test.go") {
			dirs[path.Dir(f)] = true
		}
	}
	var changed []string
	for dir := range dirs {
		before, err := m.testFiles(ctx, c.parent, dir)
		if err != nil {
			return nil, err
		}
		after, err := m.testFiles(ctx, c.sha, dir)
		if err != nil {
			return nil, err
		}
		changed = append(changed, changedTests(before, after)...)
	}
	slices.Sort(changed)
	return slices.Compact(changed), nil
}

func (m *miner) testFiles(ctx context.Context, rev, dir string) (map[string]string, error) {
	names, err := m.lines(ctx, "git", "ls-tree", "--name-only", rev, dir+"/")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, n := range names {
		if !strings.HasSuffix(n, "_test.go") {
			continue
		}
		src, err := command(ctx, m.dir, nil, "git", "show", rev+":"+n)
		if err != nil {
			return nil, err
		}
		files[n] = []byte(src)
	}
	return testFuncs(files)
}

func (m *miner) checkout(ctx context.Context, rev string) error {
	if _, err := command(ctx, m.dir, nil, "git", "checkout", "--quiet", "--force", "--detach", rev); err != nil {
		return err
	}
	_, err := command(ctx, m.dir, nil, "git", "clean", "-fdxq")
	return err
}

func (m *miner) apply(ctx context.Context, patch string) error {
	f, err := os.CreateTemp("", "benchtasks-*.diff")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(patch)
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	_, err = command(ctx, m.dir, nil, "git", "apply", "--binary", f.Name())
	return err
}

// goTest checks out rev and runs pkgs' tests, all of which must pass.
func (m *miner) goTest(ctx context.Context, rev, pattern string, pkgs []string) (testRun, error) {
	if err := m.checkout(ctx, rev); err != nil {
		return testRun{}, err
	}
	run, err := m.runTests(ctx, pattern, pkgs)
	if err != nil {
		return run, err
	}
	if len(run.failedPackages) > 0 {
		return run, fmt.Errorf("tests fail at %.12s in %s", rev, strings.Join(run.failedPackages, ", "))
	}
	return run, nil
}

// runTests runs go test in the checkout as it stands; failing tests are a result, not an
// error.
func (m *miner) runTests(ctx context.Context, pattern string, pkgs []string) (testRun, error) {
	args := []string{"test", "-count=1", "-json", "-timeout=10m"}
	if pattern != "" {
		args = append(args, "-run", pattern)
	}
	out, err := command(ctx, m.dir, m.env, "go", append(args, pkgs...)...)
	run, parseErr := parseTestJSON(strings.NewReader(out))
	if parseErr != nil {
		return run, parseErr
	}
	if err != nil && len(run.outcomes) == 0 && len(run.failedPackages) == 0 {
		return run, err
	}
	return run, nil
}

type listedPackage struct {
	ImportPath string   `json:"ImportPath"`
	Imports    []string `json:"Imports"`
	Deps       []string `json:"Deps"`
}

func (m *miner) interrogation(ctx context.Context, c commit, ch change, transitive bool) (bench.Task, error) {
	t := m.base(c, bench.TaskInterrogation)
	t.BaseCommit, t.FixCommit = c.sha, ""
	if err := m.checkout(ctx, c.sha); err != nil {
		return t, err
	}
	out, err := command(ctx, m.dir, m.env, "go", "list", "-e", "-json=ImportPath,Imports,Deps", "./...")
	if err != nil {
		return t, err
	}
	var listed []listedPackage
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var p listedPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return t, fmt.Errorf("go list: %w", err)
		}
		listed = append(listed, p)
	}
	for _, target := range m.packages(ch.sources) {
		items := dependents(listed, target, transitive)
		if len(items) < minAnswer || len(items) > maxAnswer {
			continue
		}
		field, ask, idPrefix := "Imports", "import %s directly", "importers-of-"
		if transitive {
			field, ask, idPrefix = "Deps", "depend on %s, directly or through other packages, so that their build changes when it changes", "dependents-of-"
		}
		t.ID = idPrefix + slug(strings.TrimPrefix(target, m.module+"/"))
		t.Prompt = fmt.Sprintf("Which packages of module %s "+ask+"? Count only imports from non-test Go files, and leave out %[2]s itself. "+
			"Write each import path on its own line to answer.txt at the repository root, and change nothing else.", m.module, target)
		t.Answer = &bench.AnswerKey{Items: items, Oracle: "go list -e -json ./... (" + field + ")"}
		return t, nil
	}
	return t, fmt.Errorf("no changed package has between %d and %d dependents", minAnswer, maxAnswer)
}

// dependents are the listed packages that import target, or with transitive that have it
// among their dependencies, sorted.
func dependents(listed []listedPackage, target string, transitive bool) []string {
	var out []string
	for _, p := range listed {
		edges := p.Imports
		if transitive {
			edges = p.Deps
		}
		if p.ImportPath != target && slices.Contains(edges, target) {
			out = append(out, p.ImportPath)
		}
	}
	slices.Sort(out)
	return out
}
