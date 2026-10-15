package guard

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// BenchmarkJudgeWithinInertCall is one hook call for a tool that touches nothing, now that
// every tool reaches the guard: a todo list, as Claude Code's catch-all entry forwards it.
// The judge callback stands in for the rule loading the CLI does inside it, so a call the
// read-only fast path answers costs what deciding that it may costs. It uses nothing newer
// than the fast path, so it runs unchanged on the commit before it for a benchstat pair.
func BenchmarkJudgeWithinInertCall(b *testing.B) {
	testkit.Isolate(b)
	root := testkit.NewWorkspace(b).Root()
	ctx := WithLocation(b.Context(), b.TempDir(), root, root)
	req := Request{
		Input: `{"hook_event_name":"PreToolUse","session_id":"bench","cwd":"` + root + `","tool_name":"TodoWrite",` +
			`"tool_input":{"todos":[{"content":"read the guard","status":"pending"}]}}`,
		Host: "claude-code", Form: "buzz", Session: "bench",
	}
	judge := func(ctx context.Context) types.GuardJudgment { return Judge(ctx, Dependencies{}, req) }
	b.ReportAllocs()
	for b.Loop() {
		if v := JudgeWithin(ctx, Dependencies{}, req, judge); v.Decision != decisionPass {
			b.Fatalf("a todo list was not passed: %+v", v)
		}
	}
}
