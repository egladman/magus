package knowledge

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/internal/textrank"
	"github.com/egladman/magus/types"
)

func BenchmarkPersonalizedPageRank(b *testing.B) {
	const n = 5000
	g := NewGraph()
	for i := range n {
		g.AddNode(types.KnowledgeNode{ID: fmt.Sprintf("sym:%04d", i), Kind: types.KindSymbol})
	}
	for i := range n {
		from := fmt.Sprintf("sym:%04d", i)
		g.AddEdge(pprEdge(from, fmt.Sprintf("sym:%04d", (i*7+1)%n), types.RelationCalls))
		g.AddEdge(pprEdge(from, fmt.Sprintf("sym:%04d", (i*13+5)%n), types.RelationReferences))
		g.AddEdge(pprEdge(from, fmt.Sprintf("sym:%04d", (i+1)%n), types.RelationImplements))
	}
	weights, err := textrank.LoadWeights()
	if err != nil {
		b.Fatal(err)
	}
	seeds := map[string]float64{"sym:0000": 1, "sym:1234": 0.8, "sym:2500": 0.6, "sym:3999": 0.4, "sym:4321": 0.2}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := g.PersonalizedPageRank(seeds, weights.PPR); err != nil {
			b.Fatal(err)
		}
	}
}
