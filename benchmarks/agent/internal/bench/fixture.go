package bench

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/benchmarks/agent/internal/pycompat"
)

type armSpec struct {
	turns, input, output, cacheRead, cacheWrite, resultBytes, distinctReads, wallMs int64
}

// The numbers are fixed constants so the tests can assert exact token,
// dollar and byte totals by hand rather than by re-running the generator's
// arithmetic.
var fixtureArms = map[string]armSpec{
	"full":    {turns: 4, input: 1000, output: 200, cacheRead: 8000, cacheWrite: 1200, resultBytes: 500, distinctReads: 4, wallMs: 120000},
	"rampant": {turns: 7, input: 1500, output: 300, cacheRead: 12000, cacheWrite: 1500, resultBytes: 900, distinctReads: 3, wallMs: 240000},
}

var fixtureTasks = []string{"task-a", "task-b"}

var fixtureReps = []int64{1, 2, 3}

// fixtureKey names one synthetic run; the shapes below are matched on it.
type fixtureKey struct {
	arm, task string
	rep       int64
}

type fixtureRun struct {
	fixtureKey
	// model is the alias every synthetic run reports, given by the caller: a
	// model name is host-specific, and none is written into this package.
	model string
}

// The shapes the extractor has to survive, one run each.
var (
	splitTTLRun     = fixtureKey{"full", "task-b", 1}
	noTrailRun      = fixtureKey{"full", "task-a", 3}
	failingRun      = fixtureKey{"rampant", "task-b", 2}
	testDeletingRun = fixtureKey{"rampant", "task-a", 1}
)

const fixtureDiff = `diff --git a/src/app.ts b/src/app.ts
index 1111111..2222222 100644
--- a/src/app.ts
+++ b/src/app.ts
@@ -1,3 +1,4 @@
 export function app() {
+  return 1;
 }
`

const deletedTestDiff = `diff --git a/src/app.test.ts b/src/app.test.ts
deleted file mode 100644
index 3333333..0000000
--- a/src/app.test.ts
+++ /dev/null
@@ -1,3 +0,0 @@
-test("app", () => {
-  expect(app()).toBe(1);
-});
`

// fixtureRunID names a synthetic run the way the runner names a real one.
func fixtureRunID(arm, task string, rep int64) string {
	return fmt.Sprintf("%s-%s-r%d-20260909T120000Z", arm, task, rep)
}

func fixtureUsage(spec armSpec, run fixtureRun, perInput int64) map[string]any {
	usage := map[string]any{
		"input_tokens":                perInput,
		"output_tokens":               spec.output,
		"cache_read_input_tokens":     spec.cacheRead,
		"cache_creation_input_tokens": spec.cacheWrite,
	}
	if run.fixtureKey == splitTTLRun {
		usage["cache_creation"] = map[string]any{
			"ephemeral_5m_input_tokens": spec.cacheWrite * 2 / 3,
			"ephemeral_1h_input_tokens": spec.cacheWrite / 3,
		}
	}
	return usage
}

func assistantTurn(spec armSpec, run fixtureRun, turn, perInput int64) map[string]any {
	return map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":    fmt.Sprintf("msg_%s_%d", fixtureRunID(run.arm, run.task, run.rep), turn),
			"role":  "assistant",
			"model": run.model,
			"usage": fixtureUsage(spec, run, perInput),
			"content": []any{
				map[string]any{"type": "text", "text": fmt.Sprintf("turn %d", turn)},
				map[string]any{
					"type": "tool_use", "id": fmt.Sprintf("tu_%d_a", turn), "name": "Bash",
					"input": map[string]any{"command": "magus run test ."},
				},
				map[string]any{
					"type": "tool_use", "id": fmt.Sprintf("tu_%d_b", turn), "name": "Read",
					"input": map[string]any{"file_path": fmt.Sprintf("src/mod%d.ts", turn%spec.distinctReads)},
				},
			},
		},
	}
}

func toolResults(spec armSpec, turn int64) map[string]any {
	return map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{
					"type": "tool_result", "tool_use_id": fmt.Sprintf("tu_%d_a", turn),
					"content": strings.Repeat("o", int(spec.resultBytes)),
				},
				map[string]any{
					"type": "tool_result", "tool_use_id": fmt.Sprintf("tu_%d_b", turn),
					"content": []any{map[string]any{"type": "text", "text": strings.Repeat("r", int(spec.resultBytes))}},
				},
			},
		},
	}
}

// transcriptLines builds a stream-json transcript with per-turn usage, tool
// calls and results.
func transcriptLines(run fixtureRun) []any {
	spec := fixtureArms[run.arm]
	perInput := spec.input + 100*(run.rep-1)
	lines := []any{
		map[string]any{"type": "system", "subtype": "init", "session_id": fixtureRunID(run.arm, run.task, run.rep), "model": run.model},
	}
	for turn := int64(1); turn <= spec.turns; turn++ {
		lines = append(lines, assistantTurn(spec, run, turn, perInput), toolResults(spec, turn))
	}
	// The host's own total, which extract prefers for output: a real record's
	// per-turn output counts one streamed chunk each.
	lines = append(lines, map[string]any{
		"type":           "result",
		"subtype":        "success",
		"duration_ms":    spec.wallMs + 1000*run.rep,
		"num_turns":      spec.turns,
		"total_cost_usd": 0.0,
		"usage": map[string]any{
			"input_tokens":            perInput * spec.turns,
			"output_tokens":           spec.output * spec.turns,
			"cache_read_input_tokens": spec.cacheRead * spec.turns,
		},
	})
	return lines
}

// trailLines are activity trail events; only the full arm has a guard to fire.
func trailLines(arm string) []any {
	events := []any{
		map[string]any{"kind": "agent_command", "action": "shell.command", "preview": "magus run build ."},
		map[string]any{"kind": "agent_command", "action": "file.read", "path": "src/app.ts", "preview": "read"},
	}
	if arm == "full" {
		events = append(events,
			map[string]any{"kind": "agent_command", "action": "shell.command", "preview": "guard: deny go test ./..."},
			map[string]any{"kind": "agent_command", "action": "shell.command", "preview": "guard: deny go build ./cmd/magus"},
			map[string]any{"kind": "agent_command", "action": "shell.command", "preview": "guard: advisory prefer magus query over grep"},
			map[string]any{"kind": "agent_command", "action": "file.read", "path": ".agents/skills/magus-run/SKILL.md", "preview": "read"},
			map[string]any{"kind": "agent_command", "action": "skill.load", "preview": "magus-query"},
		)
	}
	return events
}

func writeJSON(file string, value any) error {
	raw, err := pycompat.Marshal(value, 2)
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(raw, '\n'), 0o644)
}

func writeJSONL(file string, values []any) error {
	var out []byte
	for _, value := range values {
		raw, err := pycompat.Marshal(value, 0)
		if err != nil {
			return err
		}
		out = append(append(out, raw...), '\n')
	}
	return os.WriteFile(file, out, 0o644)
}

func writeRun(resultsDir string, run fixtureRun) error {
	spec := fixtureArms[run.arm]
	rid := fixtureRunID(run.arm, run.task, run.rep)
	runDir := filepath.Join(resultsDir, rid)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(runDir, "meta.json"), map[string]any{
		"run_id":        rid,
		"arm":           run.arm,
		"task":          run.task,
		"rep":           run.rep,
		"model":         run.model,
		"effort":        "high",
		"max_turns":     60,
		"budget_usd":    5.0,
		"magus_binary":  "/w/bin/magus",
		"magus_version": "v0.4.2",
		"fixture_sha":   "0f1e2d3c4b5a69788796a5b4c3d2e1f001234567",
		"started":       "2026-09-09T12:00:00Z",
		"ended":         "2026-09-09T12:04:00Z",
		"exit_reason":   "done",
	}); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(runDir, "transcript.jsonl"), transcriptLines(run)); err != nil {
		return err
	}
	diff := fixtureDiff
	if run.fixtureKey == testDeletingRun {
		diff += deletedTestDiff
	}
	failed := run.fixtureKey == failingRun
	checkExit, checkText := "0\n", "OK 3 of 3 assertions\n"
	if failed {
		checkExit, checkText = "1\n", "FAIL 1 of 3 assertions\n"
	}
	probe := "skills=0 hooks=0 mcp=off"
	if run.arm == "full" {
		probe = "skills=28 hooks=3 mcp=on"
	}
	for name, text := range map[string]string{
		"final.diff": diff, "check.exit": checkExit, "check.txt": checkText,
		"probe.txt": fmt.Sprintf("arm=%s %s\n", run.arm, probe),
	} {
		if err := os.WriteFile(filepath.Join(runDir, name), []byte(text), 0o644); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(runDir, "timing.json"), map[string]any{
		"wall_ms":               spec.wallMs + 1000*run.rep,
		"time_to_first_edit_ms": spec.wallMs/4 + 500*run.rep,
		"time_to_done_ms":       spec.wallMs - 2000 + 1000*run.rep,
	}); err != nil {
		return err
	}
	if run.fixtureKey != noTrailRun {
		activity := filepath.Join(runDir, "activity")
		if err := os.MkdirAll(activity, 0o755); err != nil {
			return err
		}
		if err := writeJSONL(filepath.Join(activity, "events.jsonl"), trailLines(run.arm)); err != nil {
			return err
		}
	}
	return nil
}

// WriteFixture writes a synthetic results tree under <root>/results: two
// arms, two tasks, three reps, carrying the shapes the extractor has to
// survive (a failing run, a run with no activity trail, a run whose cache
// writes report a 5m/1h split, and a diff that deletes a test file).
func WriteFixture(root, model string) error {
	resultsDir := filepath.Join(root, "results")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return err
	}
	for _, arm := range slices.Sorted(maps.Keys(fixtureArms)) {
		for _, task := range fixtureTasks {
			for _, rep := range fixtureReps {
				if err := writeRun(resultsDir, fixtureRun{fixtureKey{arm, task, rep}, model}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
