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
remote=$1 sha=$2 title=$3
tree=$(git --git-dir="$remote" merge-tree --write-tree main "$sha")
commit=$(git --git-dir="$remote" commit-tree "$tree" -p main -m "$title")
git --git-dir="$remote" update-ref refs/heads/main "$commit"
`

// localProvider approves everything and merges through mergeScript.
const localProvider = `
import "os";

export fun list_queue(io: {str: any}) > [any] !> any { throw "the test writes its own changes document"; }
export fun approval_at(io: {str: any}) > any { return {"approved": true, "head": io["sha"]}; }
export fun post_status(io: {str: any}) > bool { return true; }
export fun kick_back(io: {str: any}) > bool { return true; }
export fun merge_change(io: {str: any}) > any {
    final code = os\execute(["sh", "MERGE_SCRIPT", "REMOTE", "{io["sha"]}", "{io["title"]} (#{io["id"]})"]);
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

func runCLI(t *testing.T, stdin string, args ...string) ([]byte, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	t.Logf("mergequeue %s\nstderr:\n%s", strings.Join(args, " "), stderr.String())
	return stdout.Bytes(), err
}

// The CLI end to end, the way a workflow drives it: a changes document with no affected
// sets, an affected hook that answers them, validation writing a verdict per change, and
// landing merging each green change through a Buzz provider.
func TestTheCLIPlansValidatesAndLandsDisjointChanges(t *testing.T) {
	root := t.TempDir()
	remote, dev, queue := filepath.Join(root, "remote.git"), filepath.Join(root, "dev"), filepath.Join(root, "queue")
	gitIn(t, root, "init", "--quiet", "--bare", "-b", "main", remote)
	gitIn(t, root, "clone", "--quiet", remote, dev)
	for _, f := range []string{"app/a.txt", "lib/b.txt"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dev, filepath.Dir(f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dev, f), []byte("x\n"), 0o644))
	}
	gitIn(t, dev, "add", "-A")
	gitIn(t, dev, "commit", "--quiet", "-m", "initial")
	gitIn(t, dev, "push", "--quiet", "origin", "HEAD:main")
	base := gitIn(t, dev, "rev-parse", "HEAD")
	heads := map[string]string{}
	for id, f := range map[string]string{"1": "app/a.txt", "2": "lib/b.txt"} {
		gitIn(t, dev, "checkout", "--quiet", "-B", "pr"+id, base)
		require.NoError(t, os.WriteFile(filepath.Join(dev, f), []byte("change "+id+"\n"), 0o644))
		gitIn(t, dev, "commit", "--quiet", "-am", "change "+id)
		gitIn(t, dev, "push", "--quiet", "origin", "pr"+id)
		heads[id] = gitIn(t, dev, "rev-parse", "HEAD")
	}
	gitIn(t, root, "clone", "--quiet", remote, queue)
	for _, kv := range gitEnv {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}

	changes, err := json.Marshal(mergequeue.Changes{Schema: mergequeue.SchemaChanges, Base: "main", Changes: []mergequeue.Change{
		{ID: "1", Head: heads["1"], Ref: "refs/heads/pr1", Branch: "pr1", Base: "main", Title: "change 1"},
		{ID: "2", Head: heads["2"], Ref: "refs/heads/pr2", Branch: "pr2", Base: "main", Title: "change 2"},
	}})
	require.NoError(t, err)
	planFile, stages := filepath.Join(root, "plan.json"), filepath.Join(root, "stages")

	out, err := runCLI(t, string(changes), "plan", "--repo", queue, "--out", planFile,
		"--affected", `read p; printf '{"affected": ["%s"]}' "${p%%/*}"`)
	require.NoError(t, err)
	evs := events(t, out)
	require.Len(t, evs, 2)
	assert.Equal(t, []string{"1"}, evs[0].Changes)
	assert.Equal(t, []string{"2"}, evs[1].Changes, "disjoint affected sets, separate partitions")

	out, err = runCLI(t, "", "validate", "--repo", queue, "--plan", planFile, "--out", stages,
		"--gate", `test "$(cat app/a.txt)" = "change 1" || test "$MERGEQUEUE_CHANGE" = 2`)
	require.NoError(t, err)
	decided := map[string]mergequeue.Decision{}
	for _, e := range events(t, out) {
		if e.Event == mergequeue.EventDecided {
			decided[e.Change] = e.Decision
		}
	}
	assert.Equal(t, map[string]mergequeue.Decision{"1": mergequeue.DecisionLand, "2": mergequeue.DecisionLand}, decided)
	assert.FileExists(t, filepath.Join(stages, mergequeue.DoneFile))

	prov := filepath.Join(root, "local.buzz")
	script := filepath.Join(root, "merge.sh")
	require.NoError(t, os.WriteFile(script, []byte(mergeScript), 0o755))
	src := strings.NewReplacer("MERGE_SCRIPT", script, "REMOTE", remote).Replace(localProvider)
	require.NoError(t, os.WriteFile(prov, []byte(src), 0o644))
	out, err = runCLI(t, "", "land", "--repo", queue, "--plan", planFile, "--stages", stages, "--provider", prov, "--follow", "--interval", "10ms")
	require.NoError(t, err)
	var merged []string
	for _, e := range events(t, out) {
		if e.Event == mergequeue.EventMerged {
			merged = append(merged, e.Change)
		}
	}
	assert.ElementsMatch(t, []string{"1", "2"}, merged)
	assert.Equal(t, "change 2 (#2)\nchange 1 (#1)", gitIn(t, root, "--git-dir", remote, "log", "--format=%s", base+"..main"))
}

func TestUsageMistakesExitTwo(t *testing.T) {
	_, err := runCLI(t, "", "plan")
	require.ErrorIs(t, err, errUsage)
	_, err = runCLI(t, "", "frobnicate")
	require.ErrorIs(t, err, errUsage)
	_, err = runCLI(t, "", "land", "--plan", "p", "--stages", "s", "--provider", "github", "extra")
	require.ErrorIs(t, err, errUsage)
	_, err = runCLI(t, "", "validate", "-h")
	require.NoError(t, err)
}
