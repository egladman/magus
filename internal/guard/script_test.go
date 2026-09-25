package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The scripts the 2026-09-24 audit found in scratch directories, as a fixture: each is
// refused inline, and ran through `bash x.sh` or `python3 x.py` unjudged.
var scriptFixtures = map[string]string{
	"retry.sh":    "#!/usr/bin/env bash\nuntil ./magus run test . -s; do sleep 20; done\n",
	"wait":        "#!/bin/sh\nwhile ! test -f done.txt; do\n  sleep 5\ndone\n",
	"run.sh":      "set -e\n./magus run lint . > out.log 2>&1\n",
	"in.sh":       "cd libs/foo && magus run test .\n",
	"nowait.sh":   "MAGUS_NO_WAIT=1 ./magus run test .\n",
	"p3.py":       "p = 'internal/x.go'\ns = open(p).read().replace('A', 'B')\nopen(p, 'w').write(s)\n",
	"scratch.py":  "p = '/tmp/x/notes.md'\ns = open(p).read().replace('A', 'B')\nopen(p, 'w').write(s)\n",
	"ok.sh":       "./magus run test . -s\n",
	"raw.sh":      "go test ./...\n",
	"compute.py":  "print(sum(range(10)))\n",
	"notashebang": "until ./magus run test .; do sleep 20; done\n",
}

func scriptDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scriptFixtures {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755))
	}
	return dir
}

// TestGuardJudgesTheScriptALineRuns: the line is judged by the file it runs, with the
// rules that would judge that file's lines typed inline, and the refusal says the script
// was read.
func TestGuardJudgesTheScriptALineRuns(t *testing.T) {
	dir := scriptDir(t)
	for _, tt := range []struct {
		command string
		rule    denyRuleName // "" for no deny
	}{
		{"bash retry.sh", denyRuleBusyWait},
		{"./retry.sh", denyRuleBusyWait},
		{"sh -x retry.sh", denyRuleBusyWait},
		{"timeout 600 bash retry.sh", denyRuleBusyWait},
		{"bash -c 'bash retry.sh'", denyRuleBusyWait},
		{"S=" + dir + `; bash "$S/retry.sh"`, denyRuleBusyWait},
		{dir + "/wait", denyRuleBusyWait},
		{"bash run.sh", denyRuleOutputRedirect},
		{"bash in.sh", denyRuleCd},
		{"bash nowait.sh", denyRuleUnknownEnv},
		{"python3 p3.py", denyRuleScriptedRewrite},
		{"python3 -u p3.py", denyRuleScriptedRewrite},

		// A script whose content passes, or whose failure is not a script rule.
		{"bash ok.sh", ""},
		{"bash raw.sh", ""},
		{"python3 compute.py", ""},
		{"python3 scratch.py", ""},
		// No shebang and no extension: nothing says what language it is.
		{"./notashebang", ""},
		// The program is not read from a file.
		{"python3 -c 'print(1)'", ""},
		{"python3 -m p3", ""},
		{"bash missing.sh", ""},
		// A variable nobody assigned names no file.
		{`bash "$UNSET/retry.sh"`, ""},
	} {
		v := denyScriptContent(Dependencies{}, dir, tt.command, DialectBash)
		if tt.rule == "" {
			assert.Empty(t, v.Deny, tt.command)
			continue
		}
		assert.Equal(t, tt.rule, v.Rule.Name, tt.command)
		assert.Contains(t, v.Deny, "Judged from the content of", "the refusal says the script was read: %s", tt.command)
	}

	// With no directory to resolve against, only an absolute path is read.
	assert.Empty(t, denyScriptContent(Dependencies{}, "", "bash retry.sh", DialectBash).Deny)
}

// A line that earned its own deny keeps it, and a script's deny outranks an advisory.
func TestRankScriptContent(t *testing.T) {
	script := ShellVerdict{Deny: "script", Rule: denyRule{Name: denyRuleBusyWait}}
	own := ShellVerdict{Deny: "own", Rule: denyRule{Name: denyRuleCd}}
	assert.Equal(t, own, rankScriptContent(own, script))
	assert.Equal(t, script, rankScriptContent(ShellVerdict{Context: "advice"}, script))
	assert.Equal(t, ShellVerdict{Context: "advice"}, rankScriptContent(ShellVerdict{Context: "advice"}, ShellVerdict{}))
}

// TestGuardJudgesAScriptAsItIsWritten: the write is the cheapest moment to refuse, and the
// refusal is owed only when the write introduced it. Editing an unrelated line of a script
// that already carried a denied construct is not blocked on a line it did not touch.
func TestGuardJudgesAScriptAsItIsWritten(t *testing.T) {
	dir := scriptDir(t)
	path := func(name string) string { return filepath.Join(dir, name) }
	for _, tt := range []struct {
		name  string
		file  string
		write writeFields
		rule  denyRuleName
	}{
		{"new busy-wait script", path("new.sh"), writeFields{Content: scriptFixtures["retry.sh"]}, denyRuleBusyWait},
		{"new rewrite script", path("new.py"), writeFields{Content: scriptFixtures["p3.py"]}, denyRuleScriptedRewrite},
		{"shebang decides", path("tool"), writeFields{Content: scriptFixtures["retry.sh"]}, denyRuleBusyWait},
		{"edit introduces the loop", path("ok.sh"),
			writeFields{OldText: "./magus run test . -s", NewText: "until ./magus run test .; do sleep 20; done"}, denyRuleBusyWait},

		{"new clean script", path("clean.sh"), writeFields{Content: scriptFixtures["ok.sh"]}, ""},
		{"scratch-only rewrite", path("s.py"), writeFields{Content: scriptFixtures["scratch.py"]}, ""},
		{"not a script", path("notes.txt"), writeFields{Content: scriptFixtures["notashebang"]}, ""},
		{"edit elsewhere in an already-denied script", path("retry.sh"),
			writeFields{OldText: "#!/usr/bin/env bash", NewText: "#!/usr/bin/env bash\nset -e"}, ""},
		{"edit whose old text is not there", path("ok.sh"), writeFields{OldText: "absent", NewText: "until x; do sleep 1; done"}, ""},
	} {
		v := denyScriptWrite(Dependencies{}, tt.file, tt.write)
		if tt.rule == "" {
			assert.Empty(t, v.Deny, tt.name)
			continue
		}
		assert.Equal(t, tt.rule, v.Rule.Name, tt.name)
		assert.Contains(t, v.Deny, "Judged from what this write leaves in", tt.name)
	}
}

// The command side reaches Judge through the envelope's cwd, which is where a relative
// script path resolves.
func TestJudgeReadsTheScriptAtTheEnvelopeCwd(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := scriptDir(t)
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: dir})
	envelope := `{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"` + dir + `","tool_input":{"command":"bash retry.sh"}}`

	v := Judge(ctx, testDependencies(), Request{Input: envelope})

	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, string(denyRuleBusyWait), v.Rule)
	assert.Contains(t, v.Reason, "retry.sh")
}
