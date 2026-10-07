package hint

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptFixture writes a script under root and returns the entry the graph build would
// record for it: stamped with the file as written.
func scriptFixture(t *testing.T, root, rel, effect string, args ...string) ScriptServe {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte("export fun main() > void {}\n"), 0o644))
	info, err := os.Stat(abs)
	require.NoError(t, err)
	return ScriptServe{
		Path: rel, Args: args, Effect: effect, Blob: "0123abcd",
		Summary: rel + " answers it.", MtimeNs: info.ModTime().UnixNano(), Size: info.Size(),
	}
}

func writeScriptsIndex(t *testing.T, root string, serves map[string][]ScriptServe) string {
	t.Helper()
	cacheDir := t.TempDir()
	require.NoError(t, WriteScriptsIndex(cacheDir, ScriptsIndex{Root: root, Revision: "abc123", Serves: serves}))
	return cacheDir
}

func TestScriptsForServesTheIndexedScripts(t *testing.T) {
	root := t.TempDir()
	show := scriptFixture(t, root, "hack/dev/show-guard-health.buzz", ScriptEffectRead)
	prune := scriptFixture(t, root, "hack/dev/prune-worktrees.buzz", ScriptEffectWrite, "--older-than", "7d")
	cacheDir := writeScriptsIndex(t, root, map[string][]ScriptServe{
		"doctor:guard-wiring": {show, prune},
		EventDiskLow:          {prune},
	})

	next, err := MatchScripts(cacheDir, "doctor:guard-wiring", nil)
	require.NoError(t, err)
	assert.Equal(t, []Next{
		{
			ID: "script-show-guard-health", Run: "magus buzz hack/dev/show-guard-health.buzz",
			Argv: []string{"magus", "buzz", "hack/dev/show-guard-health.buzz"},
			Why:  "hack/dev/show-guard-health.buzz answers it.", Reads: true,
		},
		{
			ID: "script-prune-worktrees", Run: "magus buzz hack/dev/prune-worktrees.buzz -- --older-than 7d",
			Argv: []string{"magus", "buzz", "hack/dev/prune-worktrees.buzz", "--", "--older-than", "7d"},
			Why:  "hack/dev/prune-worktrees.buzz answers it.",
		},
	}, next)

	next, err = MatchScripts(cacheDir, EventSessionEnd, nil)
	require.NoError(t, err)
	assert.Nil(t, next, "a situation no script serves")
}

func TestScriptsForBindsPlaceholdersOrServesNothing(t *testing.T) {
	root := t.TempDir()
	failures := scriptFixture(t, root, "hack/dev/ls-test-failures.buzz", ScriptEffectRead, "--ref", "{ref}")
	scoped := scriptFixture(t, root, "hack/dev/count-refusals.buzz", ScriptEffectRead, "--rule={rule}", "--in={project}")
	unknown := scriptFixture(t, root, "hack/dev/show-feedback.buzz", ScriptEffectRead, "{session}")
	cacheDir := writeScriptsIndex(t, root, map[string][]ScriptServe{"next:run-output": {failures, scoped, unknown}})

	next, err := MatchScripts(cacheDir, "next:run-output", map[string]string{"ref": "out84fea3b6ae30", "session": "s1"})
	require.NoError(t, err)
	require.Len(t, next, 1, "an unbound {rule}/{project} and a placeholder outside the set both withhold their script")
	assert.Equal(t, "magus buzz hack/dev/ls-test-failures.buzz -- --ref out84fea3b6ae30", next[0].Run)

	next, err = MatchScripts(cacheDir, "next:run-output", map[string]string{
		"ref": "out1", "rule": "stage-all", "project": "docs site",
	})
	require.NoError(t, err)
	require.Len(t, next, 2)
	assert.Equal(t, []string{"magus", "buzz", "hack/dev/count-refusals.buzz", "--", "--rule=stage-all", "--in=docs site"}, next[1].Argv)
	assert.Equal(t, `magus buzz hack/dev/count-refusals.buzz -- --rule=stage-all "--in=docs site"`, next[1].Run)
	for _, n := range next {
		assert.NotRegexp(t, placeholderRe, n.Run, "Run never carries a placeholder")
	}

	next, err = MatchScripts(cacheDir, "next:run-output", map[string]string{"ref": ""})
	require.NoError(t, err)
	assert.Nil(t, next, "an empty fact binds nothing")
}

func TestScriptsForGrantsReadsOnlyToAnUnchangedReadScript(t *testing.T) {
	root := t.TempDir()
	read := scriptFixture(t, root, "hack/dev/show-memory-kills.buzz", ScriptEffectRead)
	edited := scriptFixture(t, root, "hack/dev/show-session-figure.buzz", ScriptEffectRead)
	write := scriptFixture(t, root, "hack/dev/merge-job-branches.buzz", ScriptEffectWrite)
	undeclared := scriptFixture(t, root, "hack/dev/ls-uncommitted.buzz", "")
	cacheDir := writeScriptsIndex(t, root, map[string][]ScriptServe{"mgs:MGS3009": {read, edited, write, undeclared}})

	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(root, edited.Path), later, later))

	next, err := MatchScripts(cacheDir, "mgs:MGS3009", nil)
	require.NoError(t, err)
	reads := map[string]bool{}
	for _, n := range next {
		reads[n.ID] = n.Reads
	}
	assert.Equal(t, map[string]bool{
		"script-show-memory-kills":   true,
		"script-show-session-figure": false,
		"script-merge-job-branches":  false,
		"script-ls-uncommitted":      false,
	}, reads, "a stamp that moved means the effect describes bytes no longer there")
}

func TestScriptsForSkipsWhatItCannotServe(t *testing.T) {
	root := t.TempDir()
	gone := scriptFixture(t, root, "hack/dev/prune-worktrees.buzz", ScriptEffectWrite)
	require.NoError(t, os.Remove(filepath.Join(root, gone.Path)))
	kept := scriptFixture(t, root, "hack/dev/render-brief.buzz", ScriptEffectRead)
	twin := scriptFixture(t, root, "hack/other/render-brief.buzz", ScriptEffectRead)
	escape := kept
	escape.Path = "../outside/render-brief.buzz"
	notBuzz := kept
	notBuzz.Path = "hack/dev/render-brief.sh"
	cacheDir := writeScriptsIndex(t, root, map[string][]ScriptServe{
		"rule:spawn-without-job-row": {gone, escape, notBuzz, kept, twin},
	})

	next, err := MatchScripts(cacheDir, "rule:spawn-without-job-row", nil)
	require.NoError(t, err)
	require.Len(t, next, 1, "a missing file, a path leaving the root, a non-Buzz path and a second script- id are all skipped")
	assert.Equal(t, "magus buzz hack/dev/render-brief.buzz", next[0].Run)

	for name, cacheDir := range map[string]string{
		"no cache dir":   "",
		"no index":       t.TempDir(),
		"foreign format": foreignScriptsIndex(t, root),
	} {
		next, err := MatchScripts(cacheDir, "rule:spawn-without-job-row", nil)
		require.NoError(t, err, name)
		assert.Nil(t, next, name)
	}
}

func foreignScriptsIndex(t *testing.T, root string) string {
	t.Helper()
	cacheDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(ScriptsIndexPath(cacheDir)), 0o755))
	require.NoError(t, os.WriteFile(ScriptsIndexPath(cacheDir),
		[]byte(`{"format":999,"root":"`+root+`","serves":{}}`), 0o600))
	return cacheDir
}

func TestScriptsForRejectsUnknownSituations(t *testing.T) {
	for _, s := range AllEvents {
		require.NoError(t, ValidateSituation(s), s)
	}
	for _, s := range []string{"rule:stage-all", "mgs:MGS3009", "doctor:stale-worktrees", "next:jobs-exited"} {
		require.NoError(t, ValidateSituation(s), s)
	}
	for _, s := range []string{"event:disk-full", "event:", "rule:", "stage-all", "", "Event:pre-pr"} {
		_, err := MatchScripts(t.TempDir(), s, nil)
		assert.Error(t, err, "%q", s)
	}
}

// A read script survives for every role; a write script reaches only the unbound
// reader, since `buzz` names no project a worker's write paths could cover.
func TestServableToHonorsReadsForScriptsOnly(t *testing.T) {
	read := breadcrumb("script-show-guard-health", Buzz, "why", "hack/dev/show-guard-health.buzz")
	read.Reads = true
	write := breadcrumb("script-prune-worktrees", Buzz, "why", "hack/dev/prune-worktrees.buzz")
	notAScript := breadcrumb("clean", Clean, "why")
	notAScript.Reads = true
	piped := NextForDenyRemedyPipeline("chained-run", [][]string{read.Argv, Clean.Argv()}, "why")
	piped.Reads = true
	all := []Next{read, write, notAScript, piped}

	assert.Equal(t, []Next{read, write, notAScript}, ServableTo(RoleUnbound, nil, all))
	assert.Equal(t, []Next{read}, ServableTo(RoleReviewer, nil, all))
	assert.Equal(t, []Next{read}, ServableTo(RoleWorker, []string{"hack/**"}, all))
	assert.True(t, mutatesTree(write.Argv), "buzz stays off readCommands")
}
