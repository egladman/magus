package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue"
	"github.com/egladman/magus/libs/mergequeue/verdicts"
)

var gitEnv = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitEnv...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// mergeScript squashes a validated head onto the remote's main with plumbing, the way a
// forge's merge button would, printing nothing on stdout.
const mergeScript = `set -e
exec 1>&2
remote=$1 commit=$2 title=$3
tree=$(git --git-dir="$remote" merge-tree --write-tree main "$commit")
commit=$(git --git-dir="$remote" commit-tree "$tree" -p main -m "$title")
git --git-dir="$remote" update-ref refs/heads/main "$commit"
`

// localProvider approves everything and merges through mergeScript.
const localProvider = `
import "os";

export fun list_changes(io: {str: any}) > [any] !> any { throw "the test writes its own changes document"; }
export fun approval_at(io: {str: any}) > any { return {"approved": true, "head": io["commit"]}; }
export fun post_status(io: {str: any}) > bool { return true; }
export fun kick_back(io: {str: any}) > bool { return true; }
export fun merge_change(io: {str: any}) > any {
    final code = os\execute(["sh", "MERGE_SCRIPT", "REMOTE", "{io["commit"]}", "{io["title"]} (#{io["id"]})"]);
    return {"merged": code == 0, "reason": "exit {code}"};
}
`

func events(t *testing.T, out []byte) []mergequeue.Event {
	t.Helper()
	var evs []mergequeue.Event
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		var e mergequeue.Event
		require.NoError(t, json.Unmarshal(sc.Bytes(), &e), "stdout is JSONL only: %q", sc.Text())
		evs = append(evs, e)
	}
	return evs
}

func decided(t *testing.T, out []byte) map[string]mergequeue.Decision {
	t.Helper()
	got := map[string]mergequeue.Decision{}
	for _, e := range events(t, out) {
		if e.Kind == mergequeue.EventDecided {
			got[e.Change] = e.Decision
		}
	}
	return got
}

func runCLI(t *testing.T, stdin string, args ...string) ([]byte, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	t.Logf("mergequeue %s\nstderr:\n%s", strings.Join(args, " "), stderr.String())
	return stdout.Bytes(), err
}

type cliFixture struct {
	root, remote, queue, base string
	changes                   []byte // the changes document for #1 (app/a.txt) and #2 (lib/b.txt)
}

// newCLIFixture is a remote with app/a.txt and lib/b.txt, a change to each, and a queue
// checkout. files are added to the initial commit.
func newCLIFixture(t *testing.T, files map[string]string) cliFixture {
	t.Helper()
	root := t.TempDir()
	f := cliFixture{root: root, remote: filepath.Join(root, "remote.git"), queue: filepath.Join(root, "queue")}
	dev := filepath.Join(root, "dev")
	gitIn(t, root, "init", "--quiet", "--bare", "-b", "main", f.remote)
	gitIn(t, root, "clone", "--quiet", f.remote, dev)
	files["app/a.txt"], files["lib/b.txt"] = "x\n", "x\n"
	for p, body := range files {
		require.NoError(t, os.MkdirAll(filepath.Join(dev, filepath.Dir(p)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dev, p), []byte(body), 0o644))
	}
	gitIn(t, dev, "add", "-A")
	gitIn(t, dev, "commit", "--quiet", "-m", "initial")
	gitIn(t, dev, "push", "--quiet", "origin", "HEAD:main")
	f.base = gitIn(t, dev, "rev-parse", "HEAD")
	heads := map[string]string{}
	for id, p := range map[string]string{"1": "app/a.txt", "2": "lib/b.txt"} {
		gitIn(t, dev, "checkout", "--quiet", "-B", "pr"+id, f.base)
		require.NoError(t, os.WriteFile(filepath.Join(dev, p), []byte("change "+id+"\n"), 0o644))
		gitIn(t, dev, "commit", "--quiet", "-am", "change "+id)
		gitIn(t, dev, "push", "--quiet", "origin", "pr"+id)
		heads[id] = gitIn(t, dev, "rev-parse", "HEAD")
	}
	gitIn(t, root, "clone", "--quiet", f.remote, f.queue)
	for _, kv := range gitEnv {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	var buf bytes.Buffer
	require.NoError(t, mergequeue.WriteChanges(&buf, mergequeue.Changes{Base: "main", Changes: []mergequeue.Change{
		{ID: "1", Head: heads["1"], Ref: "refs/heads/pr1", Branch: "pr1", Base: "main", Title: "change 1"},
		{ID: "2", Head: heads["2"], Ref: "refs/heads/pr2", Branch: "pr2", Base: "main", Title: "change 2"},
	}}))
	f.changes = buf.Bytes()
	return f
}

// plan plans the fixture's changes, each affecting its top directory.
func (f cliFixture) plan(t *testing.T) string {
	t.Helper()
	planFile := filepath.Join(f.root, "plan.json")
	out, err := runCLI(t, string(f.changes), "-C", f.queue, "plan", "--changes", "-", "--out", planFile,
		"--affected", `read p; printf '{"affected": ["%s"]}' "${p%%/*}"`)
	require.NoError(t, err)
	evs := events(t, out)
	require.Len(t, evs, 2)
	assert.Equal(t, []string{"1"}, evs[0].Changes)
	assert.Equal(t, []string{"2"}, evs[1].Changes, "disjoint affected sets, separate partitions")
	return planFile
}

// The CLI end to end, the way a workflow drives it: a changes document with no affected
// sets, an affected hook that answers them, validation writing a verdict per change, and
// landing merging each green change through a Buzz provider.
func TestTheCLIPlansValidatesAndLandsDisjointChanges(t *testing.T) {
	f := newCLIFixture(t, map[string]string{})
	planFile, dir := f.plan(t), filepath.Join(f.root, "verdicts")

	out, err := runCLI(t, "", "-C", f.queue, "validate", "--plan", planFile, "--verdicts", dir,
		"--gate", `test "$(cat app/a.txt)" = "change 1" || test "$MERGEQUEUE_CHANGE" = 2`)
	require.NoError(t, err)
	assert.Equal(t, map[string]mergequeue.Decision{"1": mergequeue.DecisionLand, "2": mergequeue.DecisionLand}, decided(t, out))
	assert.FileExists(t, filepath.Join(dir, verdicts.DoneFile))
	assert.FileExists(t, filepath.Join(dir, verdicts.PlanFile), "the directory carries its own plan")

	out, err = runCLI(t, "", "-C", f.queue, "apply", "--provider", f.provider(t), "--interval", "10ms", dir)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1", "2"}, merged(t, out))
	assert.Equal(t, "change 2 (#2)\nchange 1 (#1)", gitIn(t, f.root, "--git-dir", f.remote, "log", "--format=%s", f.base+"..main"))
}

// provider writes the local Buzz provider, merging into the fixture's remote.
func (f cliFixture) provider(t *testing.T) string {
	t.Helper()
	prov := filepath.Join(f.root, "local.buzz")
	script := filepath.Join(f.root, "merge.sh")
	require.NoError(t, os.WriteFile(script, []byte(mergeScript), 0o755))
	src := strings.NewReplacer("MERGE_SCRIPT", script, "REMOTE", f.remote).Replace(localProvider)
	require.NoError(t, os.WriteFile(prov, []byte(src), 0o644))
	return prov
}

func merged(t *testing.T, out []byte) []string {
	t.Helper()
	var ids []string
	for _, e := range events(t, out) {
		if e.Kind == mergequeue.EventMerged {
			ids = append(ids, e.Change)
		}
	}
	return ids
}

// fakeRun serves a completed GitHub Actions run 7 of acme/widgets holding artifacts,
// each the zip of a directory.
func fakeRun(t *testing.T, artifacts map[string]string) *httptest.Server {
	t.Helper()
	type row struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	var rows []row
	zips := map[string][]byte{}
	for name, dir := range artifacts {
		id := strconv.Itoa(len(rows) + 1)
		rows = append(rows, row{len(rows) + 1, name})
		zips["/repos/acme/widgets/actions/artifacts/"+id+"/zip"] = zipDir(t, dir)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/repos/acme/widgets/actions/runs/7":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "completed"})
		case "/repos/acme/widgets/actions/runs/7/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(rows), "artifacts": rows})
		default:
			body, ok := zips[req.URL.Path]
			if !ok {
				http.NotFound(w, req)
				return
			}
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_API_URL", srv.URL)
	return srv
}

func zipDir(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	require.NoError(t, zw.AddFS(os.DirFS(dir)))
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// The landing workflow's shape: apply reads the plan and verdicts from the validation
// run's artifacts, as upload-artifact would have packed validate's output.
func TestApplyFollowsAnActionsRun(t *testing.T) {
	f := newCLIFixture(t, map[string]string{})
	planFile, dir := f.plan(t), filepath.Join(f.root, "verdicts")
	_, err := runCLI(t, "", "-C", f.queue, "validate", "--plan", planFile, "--verdicts", dir, "--gate", "true")
	require.NoError(t, err)
	planDir := filepath.Join(f.root, "plan-artifact")
	require.NoError(t, os.Mkdir(planDir, 0o755))
	require.NoError(t, os.Rename(planFile, filepath.Join(planDir, verdicts.PlanFile)))
	fakeRun(t, map[string]string{
		verdicts.PlanArtifact:                planDir,
		verdicts.VerdictArtifactPrefix + "1": filepath.Join(dir, "1"),
		verdicts.VerdictArtifactPrefix + "2": filepath.Join(dir, "2"),
	})

	out, err := runCLI(t, "", "-C", f.queue, "apply", "--provider", f.provider(t), "--interval", "10ms", "github-actions:acme/widgets/runs/7")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1", "2"}, merged(t, out))
}

func TestApplyFromARunThatPlannedNothingLandsNothing(t *testing.T) {
	f := newCLIFixture(t, map[string]string{})
	fakeRun(t, map[string]string{})
	out, err := runCLI(t, "", "-C", f.queue, "apply", "--provider", f.provider(t), "--interval", "10ms", "github-actions:acme/widgets/runs/7")
	require.NoError(t, err)
	evs := events(t, out)
	require.Len(t, evs, 1)
	assert.Equal(t, mergequeue.EventNotice, evs[0].Kind)
	assert.Equal(t, "run 7 completed without a plan; nothing to apply", evs[0].Reason)
}

// A directory is a whole source: apply reads the plan validate wrote into it, and one
// without a plan is an error rather than an empty queue.
func TestApplyReadsThePlanFromADirectoryAndRefusesOneWithout(t *testing.T) {
	f := newCLIFixture(t, map[string]string{})
	planFile, dir := f.plan(t), filepath.Join(f.root, "verdicts")
	_, err := runCLI(t, "", "-C", f.queue, "validate", "--plan", planFile, "--verdicts", dir, "--gate", "true")
	require.NoError(t, err)
	require.NoError(t, os.Remove(planFile), "apply never reads validate's --plan")

	out, err := runCLI(t, "", "-C", f.queue, "apply", "--provider", f.provider(t), "--once", dir)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1", "2"}, merged(t, out))

	require.NoError(t, os.Remove(filepath.Join(dir, verdicts.PlanFile)))
	for _, mode := range [][]string{{"--once"}, {"--interval", "10ms"}} {
		args := append(append([]string{"-C", f.queue, "apply", "--provider", f.provider(t)}, mode...), dir)
		_, err = runCLI(t, "", args...)
		require.ErrorContains(t, err, "apply: "+dir+" holds no plan.json", "%v", mode)
	}
}

// Without --once apply follows a directory until validate marks it done; with --once it
// lands what is there and leaves the rest queued.
func TestApplyFollowsADirectoryUntilDoneUnlessOnce(t *testing.T) {
	f := newCLIFixture(t, map[string]string{})
	planFile, dir := f.plan(t), filepath.Join(f.root, "verdicts")
	_, err := runCLI(t, "", "-C", f.queue, "validate", "--plan", planFile, "--verdicts", dir, "--only", "1", "--gate", "true")
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(dir, verdicts.DoneFile))

	out, err := runCLI(t, "", "-C", f.queue, "apply", "--provider", f.provider(t), "--once", "--dry-run", dir)
	require.NoError(t, err)
	var waiting []string
	for _, e := range events(t, out) {
		if e.Kind == mergequeue.EventWaiting {
			waiting = append(waiting, e.Change)
		}
	}
	assert.Equal(t, []string{"2"}, waiting, "one pass: #2 is not validated yet")

	type result struct {
		out []byte
		err error
	}
	followed := make(chan result, 1)
	go func() {
		out, err := runCLI(t, "", "-C", f.queue, "apply", "--provider", f.provider(t), "--interval", "10ms", dir)
		followed <- result{out, err}
	}()
	_, err = runCLI(t, "", "-C", f.queue, "validate", "--plan", planFile, "--verdicts", dir, "--only", "2", "--gate", "true")
	require.NoError(t, err)
	select {
	case r := <-followed:
		t.Fatalf("apply returned before the directory was done: %v", r.err)
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, (&verdicts.Dir{Path: dir}).MarkDone())
	r := <-followed
	require.NoError(t, r.err)
	assert.ElementsMatch(t, []string{"1", "2"}, merged(t, r.out))
}

// -C is the checkout and what every relative path resolves against, provider included.
// Like git's, it is global: it goes before the command.
func TestDashCResolvesRelativePathsAgainstTheCheckout(t *testing.T) {
	f := newCLIFixture(t, map[string]string{})
	require.NoError(t, os.WriteFile(filepath.Join(f.queue, "changes.json"), f.changes, 0o644))
	require.NoError(t, os.Rename(f.provider(t), filepath.Join(f.queue, "local.buzz")))
	t.Chdir(f.root)

	_, err := runCLI(t, "", "plan", "-C", "queue", "--changes", "changes.json", "--out", "plan.json")
	require.ErrorIs(t, err, errUsage, "-C after the command")
	assert.NoFileExists(t, filepath.Join(f.queue, "plan.json"))

	_, err = runCLI(t, "", "-C", "queue", "plan", "--changes", "changes.json", "--out", "plan.json")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(f.queue, "plan.json"))
	_, err = runCLI(t, "", "-C", "queue", "validate", "--plan", "plan.json", "--verdicts", "verdicts", "--gate", "true")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(f.queue, "verdicts", verdicts.PlanFile))
	out, err := runCLI(t, "", "-C", "queue", "apply", "--provider", "local.buzz", "--once", "verdicts")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1", "2"}, merged(t, out))
}

func TestParseSource(t *testing.T) {
	for arg, want := range map[string]source{
		"verdicts":                           {dir: "verdicts"},
		"/tmp/queue/verdicts":                {dir: "/tmp/queue/verdicts"},
		"./x:y":                              {dir: "./x:y"},
		"x:y":                                {dir: "x:y"},
		`C:\queue`:                           {dir: `C:\queue`},
		"dir/a:b":                            {dir: "dir/a:b"},
		"github-actions:acme/widgets/runs/7": {repo: "acme/widgets", runID: "7"},
	} {
		got, err := parseSource(arg)
		require.NoError(t, err, arg)
		assert.Equal(t, want, got, arg)
	}
	for arg, msg := range map[string]string{
		"":                                        "<source> is empty",
		"s3:bucket/verdicts":                      `unknown scheme "s3"`,
		"github:acme/widgets/runs/7":              `unknown scheme "github"`,
		"github-actions:acme/widgets/7":           "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/runs/7":              "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/widgets/runs/":       "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/widgets/runs/007":    "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/widgets/runs/x":      "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:/widgets/runs/7":          "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/widgets/runs/7/x":    "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/widgets/jobs/7":      "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/widgets/runs/0":      "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/widgets/runs/-1":     "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/widgets/runs/+1":     "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/widgets/runs/7 ":     "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme//widgets/runs/7":     "want github-actions:<owner>/<name>/runs/<id>",
		"github-actions:acme/widgets/runs/7/../8": "want github-actions:<owner>/<name>/runs/<id>",
	} {
		_, err := parseSource(arg)
		require.ErrorContains(t, err, msg, "%q", arg)
	}
}

// Before, a regenerate hook failing on one change's code ended validation with no
// verdict and no .done, so that change wedged its partition on every run.
func TestAFailingRegenerationKicksItsChangeBackAndTheRunFinishes(t *testing.T) {
	f := newCLIFixture(t, map[string]string{".gitattributes": "lib/** linguist-generated\n"})
	planFile, dir := f.plan(t), filepath.Join(f.root, "verdicts")

	out, err := runCLI(t, "", "-C", f.queue, "validate", "--plan", planFile, "--verdicts", dir,
		"--gate", "true", "--regenerate", `echo "cannot regenerate $MERGEQUEUE_CHANGE" >&2; exit 1`)
	require.NoError(t, err)
	assert.Equal(t, map[string]mergequeue.Decision{"1": mergequeue.DecisionLand, "2": mergequeue.DecisionKick}, decided(t, out))
	assert.FileExists(t, filepath.Join(dir, verdicts.DoneFile))
}

func TestUsageMistakesExitTwoAndErrorsNameTheirCommandOnce(t *testing.T) {
	_, err := runCLI(t, "", "plan")
	require.ErrorIs(t, err, errUsage)
	_, err = runCLI(t, "", "frobnicate")
	require.ErrorIs(t, err, errUsage)
	for name, args := range map[string][]string{
		"list is ls":                       {"list", "--provider", "github", "--base", "main"},
		"ls without --base":                {"ls", "--provider", "github"},
		"ls with an operand":               {"ls", "--provider", "github", "--base", "main", "extra"},
		"land is apply":                    {"land", "--provider", "github", "s"},
		"apply without a source":           {"apply", "--provider", "github"},
		"apply with two sources":           {"apply", "--provider", "github", "s", "t"},
		"a flag after the source":          {"apply", "--provider", "github", "s", "--once"},
		"apply without a provider":         {"apply", "s"},
		"an unknown scheme":                {"apply", "--provider", "github", "gitlab:a/b/pipelines/7"},
		"a malformed run":                  {"apply", "--provider", "github", "github-actions:a/b/7"},
		"--interval with --once":           {"apply", "--provider", "github", "--once", "--interval", "1s", "s"},
		"a zero --interval":                {"apply", "--provider", "github", "--interval", "0s", "s"},
		"--repo is -C":                     {"apply", "--repo", ".", "--provider", "github", "s"},
		"plan without --out":               {"plan", "--changes", "-"},
		"plan with an operand":             {"plan", "--changes", "-", "--out", "p", "extra"},
		"--repo is -C on plan":             {"plan", "--repo", ".", "--out", "p"},
		"validate without a verdict dir":   {"validate", "--plan", "p", "--gate", "true"},
		"--from-run is a source":           {"apply", "--from-run", "7", "--provider", "github"},
		"--follow is the default":          {"apply", "--follow", "--provider", "github", "s"},
		"--plan is read from the source":   {"apply", "--plan", "p", "--provider", "github", "s"},
		"--verdicts is the source":         {"apply", "--verdicts", "s", "--provider", "github"},
		"--run-repo is part of the source": {"apply", "--run-repo", "a/b", "--provider", "github", "github-actions:a/b/runs/7"},
		"-C after the command":             {"validate", "-C", ".", "--plan", "p", "--gate", "true", "--verdicts", "v"},
		"-C without a path":                {"-C"},
		"-C without a command":             {"-C", "."},
		"an unknown global flag":           {"--remote", "origin", "ls"},
		"validate never talks to a forge":  {"validate", "--provider", "github", "--plan", "p", "--gate", "true", "--verdicts", "v"},
		"ls stages nothing":                {"ls", "--attribute", "x", "--provider", "github", "--base", "main"},
		"ls runs nothing in parallel":      {"ls", "--parallel", "2", "--provider", "github", "--base", "main"},
		"apply runs nothing in parallel":   {"apply", "--parallel", "2", "--provider", "github", "s"},
		"plan lands nothing":               {"plan", "--dry-run", "--out", "p"},
		"validate lands nothing":           {"validate", "--once", "--plan", "p", "--gate", "true", "--verdicts", "v"},
	} {
		_, err = runCLI(t, "", args...)
		require.ErrorIs(t, err, errUsage, name)
	}
	_, err = runCLI(t, "", "validate", "-h")
	require.NoError(t, err)
	_, err = runCLI(t, "", "apply", "-h")
	require.NoError(t, err)
	_, err = runCLI(t, "", "validate", "--plan", filepath.Join(t.TempDir(), "missing.json"), "--verdicts", t.TempDir(), "--gate", "true")
	require.ErrorContains(t, err, "validate: open ")
	assert.NotContains(t, err.Error(), "mergequeue:")
}
