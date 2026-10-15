package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// BenchmarkJudgeWithinInertCall is one hook call for a tool that touches nothing, now that
// every tool reaches the guard: a todo list, as Claude Code's catch-all entry forwards it.
// The judge callback stands in for the rule loading the CLI does inside it, so a call the
// read-only fast path answers costs what deciding that it may costs. Each call reads the
// tool rows directory, as each hook process does.
func BenchmarkJudgeWithinInertCall(b *testing.B) {
	testkit.Isolate(b)
	root := testkit.NewWorkspace(b).Root()
	for _, f := range (agent.HarnessTools{Dir: "tools", Rows: testToolRows}).Files() {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f.Path)), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f.Path), []byte(f.Content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	ctx := WithLocation(b.Context(), b.TempDir(), root, root)
	req := Request{
		Input: `{"hook_event_name":"PreToolUse","session_id":"bench","cwd":"` + root + `","tool_name":"TodoWrite",` +
			`"tool_input":{"todos":[{"content":"read the guard","status":"pending"}]}}`,
		Host: "claude-code", Form: "buzz", Session: "bench",
	}
	b.ReportAllocs()
	for b.Loop() {
		req.Tools = agent.ReadToolRows(root, "tools")
		judge := func(ctx context.Context) types.GuardJudgment { return Judge(ctx, Dependencies{}, req) }
		if v := JudgeWithin(ctx, Dependencies{}, req, judge); v.Decision != decisionPass {
			b.Fatalf("a todo list was not passed: %+v", v)
		}
	}
}
