package guard

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

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
		v := denyBriefCommand(testDependencies(), tt.brief)
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
	taught := envelope("Agent", "prompt", "Build with `MAGUS_NO_WAIT=1 ./magus run go-build .` first.")
	first := Judge(ctx, Dependencies{}, Request{Input: taught, Host: "claude-code"})
	assert.Equal(t, "advise", first.Decision, "by default the brief is advised, not refused")
	assert.Equal(t, string(denyRuleBriefCommand), first.Rule)
	assert.Contains(t, first.Context, "MAGUS_NO_WAIT")

	again := Judge(ctx, Dependencies{}, Request{Input: taught, Host: "claude-code"})
	assert.NotEqual(t, string(denyRuleBriefCommand), again.Rule, "once per session")
}

// TestBriefOffCheckIsRefused: the 2026-09-29 briefs told figure workers they "may also run"
// lint, and two ran it at once. A brief naming a live row is held to that row's check before
// any worker exists.
func TestBriefOffCheckIsRefused(t *testing.T) {
	row := figuresLease()
	produces := func() func(target, project string) bool {
		return func(target, project string) bool {
			return targetName(target) == "figures-generate" && project == "docs"
		}
	}
	defines := func(target string) bool {
		return slices.Contains([]string{"lint", "lint-files", "diagrams-generate", "figures-generate"}, targetName(target))
	}
	for _, tt := range []struct {
		name, brief, target string // target is what the refusal names; "" for none
	}{
		{"may also run", "You may also run `./magus run lint docs` beside the check.", "lint"},
		{"may also run, in prose", "You may also run ./magus run lint docs, lint-files . and observe.", "lint"},
		{"a shell block", "Validate:\n```bash\n./magus run diagrams_generate docs\n./magus run lint docs\n```", "lint"},
		{"the gate", "Finish with `./magus affected ci`.", "ci"},
		{"run the X target", "Then run the lint target and report.", "lint"},

		{"never run", "Never run `./magus run lint docs`; the orchestrator does.", ""},
		{"do not run, in prose", "Do not run lint docs: validations serialize.", ""},
		{"the row's own check", "Run `./magus run diagrams_generate docs` once at the end.", ""},
		{"the check with a charm, in prose", "Run ./magus run diagrams-generate:rw docs -- -v and report.", ""},
		{"a target regenerating the write paths", "Regenerate with `./magus run figures-generate:rw docs`.", ""},
		{"a word the workspace defines as no target", "Run the check target once, then magus run takes over.", ""},
		{"a placeholder", "Run `./magus run <target> <project>` for the row's check.", ""},
		{"a read", "Read `./magus describe job figures/flow` first.", ""},
	} {
		line, target, found := briefOffCheck(tt.brief, row, false, produces, defines)
		if tt.target == "" {
			assert.False(t, found, "%s: refused over %q", tt.name, line)
			continue
		}
		require.True(t, found, tt.name)
		assert.Equal(t, tt.target, target, tt.name)
		deny := briefOffCheckDeny(row, line, target)
		assert.Contains(t, deny, strconv.Quote(strings.TrimSpace(line)), "%s: the refusal quotes the line", tt.name)
		assert.Contains(t, deny, "`magus run diagrams_generate docs`", "%s: the refusal names the row's check", tt.name)
		assert.Contains(t, deny, "["+string(denyRuleWorkerCheckOnly)+"]", tt.name)
	}
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
		"a row that owns the gate": func(j *types.Job) { j.Check = &types.LeaseCheck{Target: "ci", Project: "."} },
		"a row declaring no check": func(j *types.Job) { j.Check = nil },
	} {
		r := figuresLease()
		change(&r)
		_, ok := briefRow([]string{r.ID}, []types.Job{r})
		assert.False(t, ok, name)
	}
}
