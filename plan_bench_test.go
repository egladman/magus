package magus

import (
	"context"
	"fmt"
	"testing"
)

// planBenchWorkspace is projects whose ci asks for lint and test, both
// needing build, with every project changed.
func planBenchWorkspace(b *testing.B, projects int) (*Magus, []string) {
	files := map[string]string{"magusfile.buzz": ""}
	var changed []string
	for p := range projects {
		dir := fmt.Sprintf("p%03d", p)
		files[dir+"/magusfile.buzz"] = `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void !> any { ctx.readsFiles(["*.go"]); }
export fun lint(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(build); }
export fun test(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(build); }
export fun ci(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(lint, test); }
`
		files[dir+"/main.go"] = "package main\n"
		changed = append(changed, dir+"/main.go")
	}
	m, err := Open(context.Background(), writeWorkspace(b, files), WithSpawnRule(TierCeiling))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = m.Close() })
	return m, changed
}

func BenchmarkPlan(b *testing.B) {
	for _, projects := range []int{10, 40} {
		m, changed := planBenchWorkspace(b, projects)
		b.Run(fmt.Sprintf("projects=%d", projects), func(b *testing.B) {
			for b.Loop() {
				if _, err := m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: changed, MaxShards: -1}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
