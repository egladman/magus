package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	out, err := runCLI(t, string(f.changes), "plan", "--repo", f.queue, "--out", planFile,
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

	out, err := runCLI(t, "", "validate", "--repo", f.queue, "--plan", planFile, "--verdicts", dir,
		"--gate", `test "$(cat app/a.txt)" = "change 1" || test "$MERGEQUEUE_CHANGE" = 2`)
	require.NoError(t, err)
	assert.Equal(t, map[string]mergequeue.Decision{"1": mergequeue.DecisionLand, "2": mergequeue.DecisionLand}, decided(t, out))
	assert.FileExists(t, filepath.Join(dir, verdicts.DoneFile))

	prov := filepath.Join(f.root, "local.buzz")
	script := filepath.Join(f.root, "merge.sh")
	require.NoError(t, os.WriteFile(script, []byte(mergeScript), 0o755))
	src := strings.NewReplacer("MERGE_SCRIPT", script, "REMOTE", f.remote).Replace(localProvider)
	require.NoError(t, os.WriteFile(prov, []byte(src), 0o644))
	out, err = runCLI(t, "", "land", "--repo", f.queue, "--plan", planFile, "--verdicts", dir, "--provider", prov, "--follow", "--interval", "10ms")
	require.NoError(t, err)
	var merged []string
	for _, e := range events(t, out) {
		if e.Kind == mergequeue.EventMerged {
			merged = append(merged, e.Change)
		}
	}
	assert.ElementsMatch(t, []string{"1", "2"}, merged)
	assert.Equal(t, "change 2 (#2)\nchange 1 (#1)", gitIn(t, f.root, "--git-dir", f.remote, "log", "--format=%s", f.base+"..main"))
}

// Before, a regenerate hook failing on one change's code ended validation with no
// verdict and no .done, so that change wedged its partition on every run.
func TestAFailingRegenerationKicksItsChangeBackAndTheRunFinishes(t *testing.T) {
	f := newCLIFixture(t, map[string]string{".gitattributes": "lib/** linguist-generated\n"})
	planFile, dir := f.plan(t), filepath.Join(f.root, "verdicts")

	out, err := runCLI(t, "", "validate", "--repo", f.queue, "--plan", planFile, "--verdicts", dir,
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
	_, err = runCLI(t, "", "land", "--plan", "p", "--verdicts", "s", "--provider", "github", "extra")
	require.ErrorIs(t, err, errUsage)
	_, err = runCLI(t, "", "validate", "-h")
	require.NoError(t, err)
	_, err = runCLI(t, "", "land", "--plan", filepath.Join(t.TempDir(), "missing.json"), "--verdicts", "s", "--provider", "github")
	require.ErrorContains(t, err, "land: open ")
	assert.NotContains(t, err.Error(), "mergequeue:")
}
