package hint

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
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
		Path: rel, Args: args, Effect: effect,
		Summary: rel + " answers it.", MtimeNs: info.ModTime().UnixNano(), Size: info.Size(),
	}
}

func writeScriptsIndex(t *testing.T, root string, serves map[string][]ScriptServe) string {
	t.Helper()
	cacheDir := t.TempDir()
	require.NoError(t, WriteScriptsIndex(cacheDir, ScriptsIndex{Root: root, Serves: serves}))
	return cacheDir
}

func TestParseScriptDeclarationReadsSummaryServesAndEffect(t *testing.T) {
	src := []byte(`import "std";

final SUMMARY = "free disk by removing finished worktrees, largest first";
export final SERVES = [
    "rule:worktree-remove -- --go-cache",
    "replaces:git  worktree remove",
];
final EFFECT = "read";

fun main(args: [str]) > void {}
`)
	d, ok, err := parseScriptDeclaration(src)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, scriptDeclaration{
		Summary: "free disk by removing finished worktrees, largest first",
		Effect:  ScriptEffectRead,
		Serves: []scriptSituation{
			{Situation: "rule:worktree-remove", Args: []string{"--go-cache"}},
			{Situation: "replaces:git worktree remove"},
		},
	}, d)

	d, ok, err = parseScriptDeclaration([]byte("final USAGE = \"x\";\nfun main(args: [str]) > void {}\n"))
	require.NoError(t, err)
	assert.False(t, ok, "a script that declares nothing")
	assert.Equal(t, scriptDeclaration{}, d)

	d, ok, err = parseScriptDeclaration([]byte("final SUMMARY = \"settle a file\";\nfinal SERVES = [\"rule:x\"];\n"))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, ScriptEffectWrite, d.Effect, "no EFFECT is a write")
}

func TestParseScriptDeclarationRefusesWhatItCannotServe(t *testing.T) {
	for name, src := range map[string]string{
		"serves without summary": `final SERVES = ["rule:stage-all"];`,
		"summary without serves": `final SUMMARY = "x";`,
		"empty serves":           "final SUMMARY = \"x\";\nfinal SERVES = [];",
		"unknown family":         "final SUMMARY = \"x\";\nfinal SERVES = [\"stage-all\"];",
		"unknown event":          "final SUMMARY = \"x\";\nfinal SERVES = [\"event:disk-full\"];",
		"unknown effect":         "final SUMMARY = \"x\";\nfinal SERVES = [\"rule:x\"];\nfinal EFFECT = \"reads\";",
		"unknown placeholder":    "final SUMMARY = \"x\";\nfinal SERVES = [\"rule:x -- --session {session}\"];",
	} {
		_, ok, err := parseScriptDeclaration([]byte(src))
		assert.True(t, ok, name)
		assert.Error(t, err, name)
	}
}

func TestBuildScriptsIndexIndexesDeclaredScripts(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) os.FileInfo {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
		info, err := os.Stat(abs)
		require.NoError(t, err)
		return info
	}
	show := write("hack/dev/show-x.buzz", "final SUMMARY = \"show x\";\nfinal SERVES = [\"rule:a\", \"mgs:MGS1\"];\nfinal EFFECT = \"read\";\n")
	edited := write("hack/dev/ls-y.buzz", "final SUMMARY = \"list y\";\nfinal SERVES = [\"rule:a\"];\nfinal EFFECT = \"read\";\n")
	write("hack/dev/plain.buzz", "fun main(args: [str]) > void {}\n")
	write("hack/dev/broken.buzz", "final SERVES = [\"rule:a\"];\n")
	write("README.md", "final SUMMARY = \"not a script\";\n")

	x, err := BuildScriptsIndex(root,
		[]string{"hack/dev/show-x.buzz", "hack/dev/ls-y.buzz", "hack/dev/plain.buzz", "hack/dev/broken.buzz", "README.md", "hack/dev/gone.buzz"},
		func(p string) bool { return p != "hack/dev/ls-y.buzz" })
	require.ErrorContains(t, err, "hack/dev/broken.buzz: declares SERVES without a SUMMARY line")
	stamp := func(info os.FileInfo) (int64, int64) { return info.ModTime().UnixNano(), info.Size() }
	showMtime, showSize := stamp(show)
	editedMtime, editedSize := stamp(edited)
	assert.Equal(t, ScriptsIndex{Root: root, Serves: map[string][]ScriptServe{
		"rule:a": {
			{Path: "hack/dev/ls-y.buzz", Effect: ScriptEffectWrite, Summary: "list y", MtimeNs: editedMtime, Size: editedSize},
			{Path: "hack/dev/show-x.buzz", Effect: ScriptEffectRead, Summary: "show x", MtimeNs: showMtime, Size: showSize},
		},
		"mgs:MGS1": {{Path: "hack/dev/show-x.buzz", Effect: ScriptEffectRead, Summary: "show x", MtimeNs: showMtime, Size: showSize}},
	}}, x, "an uncommitted read is indexed as a write")
}

func TestMatchReplacedServesTheScriptsReplacingALine(t *testing.T) {
	root := t.TempDir()
	prune := scriptFixture(t, root, "hack/dev/prune-worktrees.buzz", ScriptEffectWrite)
	merge := scriptFixture(t, root, "hack/dev/merge-job-branches.buzz", ScriptEffectWrite)
	base := scriptFixture(t, root, "hack/dev/merge-base-into-branches.buzz", ScriptEffectWrite, "--branches", "{ref}")
	cacheDir := writeScriptsIndex(t, root, map[string][]ScriptServe{
		"replaces:git worktree remove": {prune},
		"replaces:git merge":           {base, merge},
		"replaces:git cherry-pick":     {merge},
		"rule:stage-all":               {prune},
	})

	run := func(next []types.NextStep) []string {
		var out []string
		for _, n := range next {
			out = append(out, n.Run)
		}
		return out
	}
	assert.Equal(t, []string{"magus buzz hack/dev/prune-worktrees.buzz"},
		run(MatchReplaced(cacheDir, []Invocation{{Name: "/usr/bin/git", Args: []string{"worktree", "remove", "--force", "x"}}}, nil)))
	assert.Equal(t, []string{"magus buzz hack/dev/merge-job-branches.buzz"},
		run(MatchReplaced(cacheDir, []Invocation{{Name: "git", Args: []string{"merge", "main"}}, {Name: "git", Args: []string{"cherry-pick", "abc"}}}, nil)),
		"merge-base-into-branches needs a {ref} the facts do not bind, and merge-job-branches is named once")
	assert.Equal(t, []string{"magus buzz hack/dev/merge-base-into-branches.buzz -- --branches feat", "magus buzz hack/dev/merge-job-branches.buzz"},
		run(MatchReplaced(cacheDir, []Invocation{{Name: "git", Args: []string{"merge", "main"}}}, map[string]string{"ref": "feat"})))
	assert.Nil(t, MatchReplaced(cacheDir, []Invocation{{Name: "git", Args: []string{"worktree", "list"}}}, nil))
	assert.Nil(t, MatchReplaced("", []Invocation{{Name: "git", Args: []string{"merge"}}}, nil), "no cache dir")
}

func TestMissingScriptsNamesWhatTheIndexServesAndIsGone(t *testing.T) {
	root := t.TempDir()
	kept := scriptFixture(t, root, "hack/dev/show-x.buzz", ScriptEffectRead)
	gone := scriptFixture(t, root, "hack/dev/prune-worktrees.buzz", ScriptEffectWrite)
	require.NoError(t, os.Remove(filepath.Join(root, gone.Path)))
	cacheDir := writeScriptsIndex(t, root, map[string][]ScriptServe{"rule:a": {kept, gone}, "event:disk-low": {gone}})

	missing, ok := MissingScripts(cacheDir)
	assert.True(t, ok)
	assert.Equal(t, []string{"hack/dev/prune-worktrees.buzz"}, missing)

	missing, ok = MissingScripts(t.TempDir())
	assert.False(t, ok, "no index")
	assert.Nil(t, missing)
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
	assert.Equal(t, []types.NextStep{
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
	assert.Equal(t, types.NextStep{
		ID:    ScriptIDPrefix + "count-refusals",
		Run:   `magus buzz hack/dev/count-refusals.buzz -- --rule=stage-all "--in=docs site"`,
		Argv:  []string{"magus", "buzz", "hack/dev/count-refusals.buzz", "--", "--rule=stage-all", "--in=docs site"},
		Why:   scoped.Summary,
		Reads: true, // a read script whose file is unchanged since it was indexed
	}, next[1])
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
		[]byte(fmt.Sprintf(`{"format":999,"root":"%s","serves":{}}`, root)), 0o600))
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
func TestFilterServableHonorsReadsForScriptsOnly(t *testing.T) {
	read := breadcrumb("script-show-guard-health", Buzz, "why", "hack/dev/show-guard-health.buzz")
	read.Reads = true
	write := breadcrumb("script-prune-worktrees", Buzz, "why", "hack/dev/prune-worktrees.buzz")
	notAScript := breadcrumb("clean", Clean, "why")
	notAScript.Reads = true
	piped := NextForDenyRemedyPipeline("chained-run", [][]string{read.Argv, Clean.Argv()}, "why")
	piped.Reads = true
	all := []types.NextStep{read, write, notAScript, piped}

	assert.Equal(t, []types.NextStep{read, write, notAScript}, FilterServable(RoleUnbound, nil, all))
	assert.Equal(t, []types.NextStep{read}, FilterServable(RoleReviewer, nil, all))
	assert.Equal(t, []types.NextStep{read}, FilterServable(RoleWorker, []string{"hack/**"}, all))
	assert.True(t, mutatesTree(write.Argv), "buzz stays off readCommands")
}
