package forecast

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/types"
)

// benchModel is projects each with build, lint, test and vet, every unit
// needing its project's build and a shared generate that every shard may
// replay. Peaks force the memory loop to add shards.
func benchModel(projects int) (Model, []types.Unit) {
	h := History{Constants: Constants{SetupP50Ms: Millis(20_000)}, Projects: map[string]map[string]Stats{}}
	edges := map[types.Unit][]types.Unit{}
	gen := types.Unit{Project: ".", Target: "generate"}
	h.Projects["."] = map[string]Stats{"generate": {P75Ms: 30_000, Samples: 50}}
	var units []types.Unit
	for p := range projects {
		name := fmt.Sprintf("libs/p%04d", p)
		h.Projects[name] = map[string]Stats{}
		build := types.Unit{Project: name, Target: "build"}
		for t, target := range []string{"build", "lint", "test", "vet"} {
			ms := int64(5_000 + (p*7919+t*104729)%240_000)
			h.Projects[name][target] = Stats{P75Ms: ms, Samples: 50}
			seedPeak(&h, name, target, int64(1+(p+t)%6)<<30)
			u := types.Unit{Project: name, Target: target}
			units = append(units, u)
			edges[u] = []types.Unit{u, gen}
			if target != "build" {
				edges[u] = append(edges[u], build)
			}
		}
	}
	return Model{
		History:           h,
		Needs:             func(u types.Unit) []types.Unit { return edges[u] },
		Repeatable:        func(u types.Unit) bool { return u == gen },
		Slots:             4,
		MemoryBudgetBytes: 12 << 30,
	}, units
}

func BenchmarkPlan(b *testing.B) {
	for _, projects := range []int{100, 500} {
		f, units := benchModel(projects)
		b.Run(fmt.Sprintf("units=%d", len(units)), func(b *testing.B) {
			for b.Loop() {
				f.Plan(units, 256)
			}
		})
	}
}

func BenchmarkSufficientShards(b *testing.B) {
	for _, projects := range []int{100, 500} {
		f, units := benchModel(projects)
		b.Run(fmt.Sprintf("units=%d", len(units)), func(b *testing.B) {
			for b.Loop() {
				f.SufficientShards(units, 256)
			}
		})
	}
}
