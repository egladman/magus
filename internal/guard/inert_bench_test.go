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

// inertBenchRules is a root magusfile with one rule of each kind a hook loads, so the judged
// arm pays for parsing and registering them as a hook does.
const inertBenchRules = `import "magus";

magus\guard.shell({"name": "no-frobnicate", "decision": "deny", "program": "frobnicate", "reason": "Not here."});

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    return magus\guard.allow();
});
`

// BenchmarkInertCallLoadsNoRules is one hook call for a todo list, as Claude Code's catch-all
// entry forwards it. Each call reads the tool rows directory, as each hook process does.
// fast is the read-only fast path; judged is the same call when no fast path answers it:
// the workspace's rules loaded, as the CLI loads them inside the hook's bound, then judged.
func BenchmarkInertCallLoadsNoRules(b *testing.B) {
	testkit.Isolate(b)
	repo := testkit.NewRepo(b, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": inertBenchRules})
	root := repo.Root()
	for _, f := range (agent.HarnessTools{Dir: "tools", Rows: testToolRows}).Files() {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f.Path)), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f.Path), []byte(f.Content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	cacheDir := b.TempDir()
	ctx := WithLocation(b.Context(), cacheDir, root, root)
	req := Request{
		Input: `{"hook_event_name":"PreToolUse","session_id":"bench","cwd":"` + root + `","tool_name":"TodoWrite",` +
			`"tool_input":{"todos":[{"content":"read the guard","status":"pending"}]}}`,
		Host: "claude-code", Form: "buzz", Session: "bench",
	}
	load := func(ctx context.Context) Dependencies {
		deps, rules := NewDependencies(ctx, Wiring{Root: root, CacheDir: func(string) (string, error) { return cacheDir, nil }})
		if rules == nil || deps.LoadFailure != nil {
			b.Fatalf("the rules did not load: %v", deps.LoadFailure)
		}
		return deps
	}
	b.Run("fast", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			req.Tools = agent.ReadToolRows(root, "tools")
			judge := func(ctx context.Context) types.GuardJudgment { return Judge(ctx, load(ctx), req) }
			if v := JudgeWithin(ctx, Dependencies{}, req, judge); v.Decision != decisionPass {
				b.Fatalf("a todo list was not passed: %+v", v)
			}
		}
	})
	b.Run("judged", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			req.Tools = agent.ReadToolRows(root, "tools")
			if v := Judge(ctx, load(ctx), req); v.Decision != decisionPass {
				b.Fatalf("a todo list was not passed: %+v", v)
			}
		}
	})
}
