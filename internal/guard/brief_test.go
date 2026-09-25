package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
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
		{"prompt marker in a block", "Then:\n```console\n$ cd libs/foo && magus run test .\n```", denyRuleCd},
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
	} {
		v := denyBriefCommand(Dependencies{}, tt.brief)
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
		v := Judge(ctx, Dependencies{}, Request{Input: input, Host: "claude-code"})
		assert.Equal(t, "deny", v.Decision, input)
		assert.Equal(t, string(denyRuleBriefCommand), v.Rule, input)
		assert.Contains(t, v.Reason, "MAGUS_NO_WAIT")
	}

	ctx, _ := spawnFixture(t)
	v := Judge(ctx, Dependencies{}, Request{Input: envelope("Agent", "prompt", "Never use `MAGUS_NO_WAIT=1`."), Host: "claude-code"})
	assert.NotEqual(t, "deny", v.Decision)
}
