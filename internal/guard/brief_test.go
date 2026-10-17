package guard

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

func TestBriefForAnIsolatedWorkerHoldsItToPlainCommands(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		brief    string
		isolated bool
		want     bool
	}{
		{"says it", "Fix the bug. Run one plain command per call; no `$()`, loops or exec.", true, false},
		{"says it in other words", "Use plain commands only.", true, false},
		{"never says it", "Fix the bug and report the sha.", true, true},
		{"teaches a substitution", "Run one command per call. Then `git log $(git merge-base HEAD main)..HEAD`.", true, true},
		{"teaches a loop", "Run one command per call:\n```bash\nfor f in a b; do ./magus describe file $f; done\n```\n", true, true},
		{"a shared checkout", "Fix the bug and report the sha.", false, false},
	} {
		v := judgeBriefPlainCommands(briefInput{text: tt.brief, isolated: tt.isolated})
		if tt.want {
			assert.Equal(t, denyRuleBriefPlainCommands, v.Rule.Name, tt.name)
			assert.NotEmpty(t, v.Deny, tt.name)
		} else {
			assert.Empty(t, v.Deny, tt.name)
		}
	}
}

// TestBriefThatTeachesADeniedCommandIsRefused: a worker runs its brief's commands as
// written. Measured 2026-09-24, 33 briefs seeded 462 prefixes of a retired variable. A
// command the brief presents as one to run is graded like a shell line; a command it
// names to forbid is not.
func TestBriefThatTeachesADeniedCommandIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name  string
		brief string
		arg   denyRuleName // the inline rule the brief teaches; "" for none
	}{
		{"inline span", "Build with `MAGUS_NO_WAIT=1 ./magus run go-build .` first.", denyRuleUnknownEnv},
		{"fenced block", "Validate:\n```bash\n./magus run lint . 2>&1 | tail -30\n```\n", denyRuleOutputPipe},
		{"unlabeled fence", "Run:\n```\ngit add -A\n```", denyRuleStageAll},
		{"prompt marker in a block", "Then:\n```console\n$ magus run test . > out.log\n```", denyRuleOutputRedirect},
		{"exit echo", "Check it with `./magus run test .; echo \"EXIT $?\"`.", denyRuleExitStatusEcho},
		{"placeholder is a word", "Run `MAGUS_NO_WAIT=1 magus run <target> <project>`.", denyRuleUnknownEnv},

		// Named in order to forbid it.
		{"never", "Never run `git add -A`; stage explicit paths.", ""},
		{"do not", "Do not prefix `MAGUS_NO_WAIT=1 ./magus run lint .`, it was removed.", ""},
		{"denied", "The guard denies `sed -i`, so use the editor tool.", ""},
		{"block after a negation", "Do not run this:\n```bash\ngit add -A\n```", ""},
		// A placeholder a shell would read as redirects is not a redirected magus call.
		{"placeholder", "Run `magus refs <symbol> --occurrences` for each name.", ""},
		{"clean", "Run `./magus run go::go-test . -s -- -count=1 ./internal/guard/...` and report.", ""},
		{"not shell", "Example:\n```go\nfunc main() { exec(\"git add -A\") }\n```", ""},
		{"prose", "The file lives at `internal/guard/shell.go`.", ""},
		// A lone word in a span names a program; it teaches no command line.
		{"single word", "The script pipes the list into `cat`, then `grep`.", ""},
		// Judged as the worker's fresh tree meets it, which has no binary yet.
		{"bootstrap span", "Bootstrap with `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .` first.", ""},
		{"bootstrap block", "Setup:\n```bash\nGOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .\n./magus run go-build . -s\n```\n", ""},
		{"bootstrap without its prefix", "Bootstrap with `go run -trimpath ./cmd/magus run go-build --no-cache .` first.", denyRuleRawTool},
		{"bootstrap sharing its line", "Run `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache . && ls`.", denyRuleRawTool},
		{"bootstrap beside a denied line", "Setup:\n```bash\nGOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .\ngit add -A\n```\n", denyRuleStageAll},
		{"go run of anything else", "Run `go run ./cmd/magus-docs` to render.", denyRuleRawTool},
	} {
		v := judgeBriefCommand(strict(testDependencies()), briefInput{text: tt.brief})
		if tt.arg == "" {
			assert.Empty(t, v.Deny, tt.name)
			continue
		}
		assert.Equal(t, denyRule{Name: denyRuleBriefCommand, Arg: string(tt.arg)}, v.Rule, tt.name)
		assert.Contains(t, v.Deny, string(tt.arg), "the deny names the rule the brief would trip: %s", tt.name)
	}
}

// Both a spawn and a continuation carry a brief, so both reach the rule through Judge.
func TestJudgeRefusesASpawnWhoseBriefTeachesADeniedCommand(t *testing.T) {
	envelope := func(tool, key, text string) string {
		input := map[string]any{key: text}
		if key == "message" {
			input["to"] = "brisk-heron"
		}
		raw, err := json.Marshal(map[string]any{
			"session_id": "s1", "hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input,
		})
		require.NoError(t, err)
		return string(raw)
	}
	for _, input := range []string{
		envelope("Agent", "prompt", "Build with `MAGUS_NO_WAIT=1 ./magus run go-build .` first."),
		envelope("SendMessage", "message", "Now run `MAGUS_NO_WAIT=1 ./magus run lint .` again."),
	} {
		ctx, _ := spawnFixture(t)
		v := Judge(ctx, strict(Dependencies{}), Request{Input: input, Host: "claude-code"})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleBriefCommand)), unworded(v), input)
		assert.Contains(t, v.Reason, "MAGUS_NO_WAIT")
	}

	ctx, _ := spawnFixture(t)
	v := Judge(ctx, strict(Dependencies{}), Request{Input: envelope("Agent", "prompt", "Never use `MAGUS_NO_WAIT=1`."), Host: "claude-code"})
	assert.NotEqual(t, "deny", v.Decision)

	ctx, _ = spawnFixture(t)
	// A command the default still refuses, since a brief is only as strict as the rules it teaches.
	taught := envelope("Agent", "prompt", "Clean up first with `git stash`.")
	first := Judge(ctx, Dependencies{}, Request{Input: taught, Host: "claude-code"})
	assert.Equal(t, verdictWithRule("advise", string(denyRuleBriefCommand)), unworded(first),
		"by default the brief is advised, not refused")
	assert.Contains(t, first.Context, "git stash")

	again := Judge(ctx, Dependencies{}, Request{Input: taught, Host: "claude-code"})
	assert.NotEqual(t, string(denyRuleBriefCommand), again.Rule, "once per session")
}

// TestBriefOffCheckIsRefused: the 2026-09-29 briefs told figure workers they "may also run"
// lint, and two ran it at once. A brief naming a live row is held to that row's check before
// any worker exists. Only a run the brief teaches, in a code span or a shell block, is held
// to it: on 2026-10-14 a brief that only described a refused run in running text was refused.
// A run the row allows is still held to the write paths under the rw charm, as the worker's
// own run would be.
func TestBriefOffCheckIsRefused(t *testing.T) {
	row := figuresLease()
	produces := func() func(target, project string) bool {
		return func(target, project string) bool {
			return targetName(target) == "figures-generate" && project == "docs"
		}
	}
	deps := figuresWorkspace(t)
	judgeWith := func(row types.Job, defaults []string) rowJudge {
		d := deps
		d.DefaultCharms = defaults
		d.Spells = spellCatalog
		judge := newRowJudge(t.Context(), d, row, location{}, lazyWorkspace(t.Context(), d, ""))
		judge.produces = produces
		return judge
	}
	for _, tt := range []struct {
		name, brief, target string // target is what the refusal names; "" for none
	}{
		{"may also run", "You may also run `./magus run lint docs` beside the check.", "lint"},
		{"a shell block", "Validate:\n```bash\n./magus run diagrams_generate docs\n./magus run lint docs\n```", "lint"},
		{"the gate", "Finish with `./magus affected ci`.", "ci"},

		{"may also run, in prose", "You may also run ./magus run lint docs, lint-files . and observe.", ""},
		{"run the X target", "Then run the lint target and report.", ""},
		{"a run described in prose", "The guard refused magus run go-build . for the last worker; report it.", ""},
		{"never run", "Never run `./magus run lint docs`; the orchestrator does.", ""},
		{"do not run, in prose", "Do not run lint docs: validations serialize.", ""},
		{"the row's own check", "Run `./magus run diagrams_generate docs` once at the end.", ""},
		{"the check with a charm, in prose", "Run ./magus run diagrams-generate:rw docs -- -v and report.", ""},
		{"a target regenerating past the write paths", "Regenerate with `./magus run figures-generate:rw docs`.", "figures-generate:rw"},
		{"a target regenerating the write paths, read-only", "Check with `./magus run figures-generate docs`.", ""},
		{"a word the workspace defines as no target", "Run the check target once, then magus run takes over.", ""},
		{"a placeholder", "Run `./magus run <target> <project>` for the row's check.", ""},
		{"a read", "Read `./magus describe job figures/flow` first.", ""},
	} {
		runs := briefRuns(tt.brief)
		off, v, found := briefOffCheck(runs, judgeWith(row, nil))
		if tt.target == "" {
			assert.False(t, found, "%s: refused over %q", tt.name, off.line)
			if slices.ContainsFunc(runs, func(r briefRun) bool { return r.prose }) {
				assert.NotEmpty(t, briefPassNotes(runs, judgeWith(row, nil)), "%s: a pass says why the run was not graded", tt.name)
			}
			continue
		}
		require.True(t, found, tt.name)
		assert.Equal(t, tt.target, off.run.target, tt.name)
		deny := briefOffCheckDeny(row, off, v)
		assert.Contains(t, deny, strconv.Quote(strings.TrimSpace(off.line)), "%s: the refusal quotes the line", tt.name)
		assert.Contains(t, deny, "["+string(denyRuleWorkerCheckOnly)+"]", tt.name)
		if len(v.outside) > 0 {
			assert.Contains(t, deny, "rewrites files outside its write paths", tt.name)
			why := briefOffCheckWhy(row, v, nil)
			assert.Contains(t, why, "`docs/gen/figures/*.svg`", "%s: the refusal names the glob", tt.name)
			assert.Contains(t, why, "Narrow the brief's command to `", "%s: the spawner is told what to change", tt.name)
			assert.Contains(t, why, "widen the row's write paths", tt.name)
			assert.NotContains(t, why, "unresolved risks", "%s: the Why is not the worker's", tt.name)
			continue
		}
		assert.Contains(t, deny, "`magus run diagrams_generate docs`", "%s: the refusal names the row's check", tt.name)
	}

	plain := briefRuns("Check with `./magus run figures-generate docs`.")
	_, v, found := briefOffCheck(plain, judgeWith(row, []string{"rw"}))
	require.True(t, found, "default_charms [rw] rewrite a plain run")
	assert.NotEmpty(t, v.outside)
	_, _, found = briefOffCheck(briefRuns("Check with `./magus run figures-generate docs --no-default-charms`."), judgeWith(row, []string{"rw"}))
	assert.False(t, found, "--no-default-charms keeps the defaults off")

	vet := figuresLease()
	vet.Check = &types.LeaseCheck{Target: "go-vet", Project: "docs"}
	_, _, found = briefOffCheck(briefRuns("Check with `./magus run go::go-vet docs`."), judgeWith(vet, []string{"rw"}))
	assert.False(t, found, "a check declaring no write passes under default rw")
}

// TestBriefAndWorkerShareOneVerdict: a brief is refused exactly when its worker's run would
// be. A slice of the check, an op it composes and an install pass both; a target off the
// check is refused by both.
func TestBriefAndWorkerShareOneVerdict(t *testing.T) {
	deps := composingWorkspace(t)
	row := types.Job{
		ID: "guard/slice", Criteria: "fix the guard", WritePaths: []string{"internal/guard/**"},
		Check: &types.LeaseCheck{Target: "test", Project: "."}, State: types.StateRunning, Registered: 1,
	}
	ctx, root := fleetFixture(t, row)
	judge := newRowJudge(ctx, deps, row, location{workspace: root, dir: root}, lazyWorkspace(ctx, deps, root))
	for _, tt := range []struct {
		name, command string
		refused       bool
	}{
		{"a slice of the check", "./magus run go-test . -- -run TestX", false},
		{"an op the check composes", "./magus run go::go-vet .", false},
		{"an install", "./magus run install .", false},
		{"a target off the check", "./magus run lint .", true},
	} {
		worker := denyWorkerCheckOnly(ctx, deps, row.ID, tt.command)
		_, _, briefRefused := briefOffCheck(briefRuns("Run `"+tt.command+"`."), judge)
		assert.Equal(t, tt.refused, worker.refused(), "%s: the worker", tt.name)
		assert.Equal(t, tt.refused, briefRefused, "%s: the brief", tt.name)
	}
}

// agentEnvelope is a spawn in a session of its own, since a repeated deny is shortened.
func agentEnvelope(t *testing.T, description, prompt string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session_id": strings.ReplaceAll(t.Name(), "/", "-"), "hook_event_name": "PreToolUse", "tool_name": "Agent",
		"tool_input": map[string]any{"description": description, "prompt": prompt, "name": "brisk-heron"},
	})
	require.NoError(t, err)
	return string(raw)
}

// TestBriefGradingExplainsAPass: four identical briefs once told workers to also run lint,
// and only the first was refused. Whatever the grader skips, the verdict now says why, so
// two identical lines never differ without a reason on the page.
func TestBriefGradingExplainsAPass(t *testing.T) {
	const offCheck = "Also run `./magus run lint docs -s` when done."
	ctx, root := fleetFixture(t, figuresLease())
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), nil, 0o644))
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	// The brief grader finds the job store through the dependencies, not the pinned context.
	strict := func(d Dependencies) Dependencies {
		d.CacheDir = func(string) (string, error) { return cacheDir, nil }
		return strict(d)
	}

	t.Run("a brief naming no job", func(t *testing.T) {
		v := Judge(ctx, strict(Dependencies{}), Request{Input: agentEnvelope(t, "orchestrator/worker draft", offCheck), Host: "claude-code"})
		assert.Equal(t, verdictWithRule("advise", string(denyRuleBriefCommand)), unworded(v))
		assert.Contains(t, v.Context, "no row was held to a check")
		assert.Contains(t, v.Context, "names no job")
		assert.Contains(t, v.Context, `"Also run `+"`./magus run lint docs -s`"+` when done." is not graded: no row was held to a check`)
	})
	t.Run("a brief naming a job the store does not hold live", func(t *testing.T) {
		v := Judge(ctx, strict(Dependencies{}), Request{Input: agentEnvelope(t, "orchestrator/worker draft", "JOB ID: ghost/job\n"+offCheck), Host: "claude-code"})
		assert.Contains(t, v.Context, "job ghost/job is not in the store")
	})
	t.Run("the same brief is refused once the row is named", func(t *testing.T) {
		v := Judge(ctx, strict(Dependencies{}), Request{Input: agentEnvelope(t, "orchestrator/worker draft", "JOB ID: figures/flow\n"+offCheck), Host: "claude-code"})
		assert.Equal(t, "deny", v.Decision)
		assert.Contains(t, v.Reason, "job figures/flow's worker to run `lint`")
	})
	t.Run("the spawn title names the row the brief does not", func(t *testing.T) {
		v := Judge(ctx, strict(Dependencies{}), Request{Input: agentEnvelope(t, "orchestrator/worker figures/flow", offCheck), Host: "claude-code"})
		assert.Equal(t, "deny", v.Decision, v.Reason)
		assert.Contains(t, v.Reason, "job figures/flow's worker to run `lint`")
	})
	t.Run("a forbidding line is passed, and says so", func(t *testing.T) {
		v := Judge(ctx, strict(Dependencies{}), Request{Input: agentEnvelope(t, "orchestrator/worker figures/flow", "Never run `./magus run lint docs`; the orchestrator does.\nFinish with `./magus run diagrams_generate docs`."), Host: "claude-code"})
		assert.Equal(t, "advise", v.Decision)
		assert.Contains(t, v.Context, "is not graded: the line forbids the run")
	})
	t.Run("the row's own check needs no words", func(t *testing.T) {
		v := Judge(ctx, strict(Dependencies{}), Request{Input: agentEnvelope(t, "orchestrator/worker figures/flow", "Run `./magus run diagrams_generate docs` once."), Host: "claude-code"})
		assert.Equal(t, "pass", v.Decision, v.Context)
	})
	t.Run("an integrate spawn is not held to its row's check", func(t *testing.T) {
		v := Judge(ctx, strict(Dependencies{}), Request{Input: agentEnvelope(t, "orchestrator/integrate figures/flow", "JOB ID: figures/flow\nRun `./magus run go-build .`, then "+offCheck), Host: "claude-code"})
		assert.NotEqual(t, "deny", v.Decision, v.Reason)
		assert.Contains(t, v.Context, "the spawn's role is integrate")
	})
}

// TestBriefNamesItsRow: the row is found by the lease a brief exports, the job it takes, or
// its JOB ID line, and only a live worker row that names a check is one to hold it to.
func TestBriefNamesItsRow(t *testing.T) {
	brief := "JOB ID: figures/flow. Export `BAGGAGE=magus.lease=figures/flow`, then `./magus job exec figures/flow`."
	assert.Equal(t, []string{"figures/flow"}, briefJobIDs(brief))
	assert.Empty(t, briefJobIDs("Run `magus job exec <its id>` once."))

	row := figuresLease()
	got, ok := briefRow([]string{"figures/flow"}, []types.Job{row})
	require.True(t, ok)
	assert.Equal(t, row.ID, got.ID)

	for name, change := range map[string]func(*types.Job){
		"a terminal row":           func(j *types.Job) { j.State = types.StatePass },
		"a row declaring no check": func(j *types.Job) { j.Check = nil },
	} {
		r := figuresLease()
		change(&r)
		_, ok := briefRow([]string{r.ID}, []types.Job{r})
		assert.False(t, ok, name)
	}
}
