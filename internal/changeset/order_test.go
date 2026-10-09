package changeset

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func orderHunk(index, start, count int, symbols ...string) types.DiffHunk {
	return types.DiffHunk{Index: index, Digest: fmt.Sprintf("digest-%d", index), NewStart: start, NewCount: count, Symbols: symbols}
}

// orderFile builds an OrderFile whose Symbols are the ones its hunks name.
func orderFile(path string, hunks ...types.DiffHunk) OrderFile {
	f := OrderFile{Path: path, Hunks: hunks}
	seen := map[string]bool{}
	for _, h := range hunks {
		for _, s := range h.Symbols {
			if !seen[s] {
				seen[s] = true
				f.Symbols = append(f.Symbols, types.DiffSymbol{ID: s, Label: s})
			}
		}
	}
	return f
}

func orderUse(symbol, path string, line int) OrderSite {
	return OrderSite{Symbol: symbol, Path: path, Line: line}
}

func orderIsTest(path string) bool { return strings.HasSuffix(path, "_test.go") }

// orderOutline renders an order as "step path#index relation" lines under "group kind label
// size" headers, the shape the ordering tests assert against.
func orderOutline(o types.DiffOrder) []string {
	var out []string
	for _, g := range o.Groups {
		out = append(out, fmt.Sprintf("group %s %q %d", g.Kind, g.Label, g.Hunks))
		for _, st := range g.Steps {
			for _, h := range st.Hunks {
				out = append(out, fmt.Sprintf("%d %s#%d %s", st.Number, h.Hunk.Path, h.Hunk.Index, h.Why.Relation))
			}
		}
	}
	return out
}

func orderStepHunk(t *testing.T, o types.DiffOrder, path string, index int) types.DiffStepHunk {
	t.Helper()
	for _, g := range o.Groups {
		for _, st := range g.Steps {
			for _, h := range st.Hunks {
				if h.Hunk.Path == path && h.Hunk.Index == index {
					return h
				}
			}
		}
	}
	require.FailNow(t, "hunk not in order", "%s#%d", path, index)
	return types.DiffStepHunk{}
}

func TestOrderPutsDefinitionsFirstAndTestsAfterCode(t *testing.T) {
	t.Parallel()

	// d.go defines B; b.go defines A and uses B; a.go uses A; c_test.go uses B.
	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			orderFile("a.go", orderHunk(0, 1, 5)),
			orderFile("b.go", orderHunk(0, 1, 5, "A")),
			orderFile("c_test.go", orderHunk(0, 1, 5)),
			orderFile("d.go", orderHunk(0, 1, 5, "B")),
		},
		Sites:  []OrderSite{orderUse("A", "a.go", 2), orderUse("B", "b.go", 3), orderUse("B", "c_test.go", 4)},
		IsTest: orderIsTest,
	})

	assert.Equal(t, []string{
		`group connected "B" 4`,
		"1 d.go#0 starts",
		"2 b.go#0 uses",
		"3 a.go#0 uses",
		"4 c_test.go#0 tests",
	}, orderOutline(order), "the test hunk waits though it is ready as early as b.go")

	b := orderStepHunk(t, order, "b.go", 0)
	assert.Equal(t, types.DiffWhy{Relation: types.DiffWhyUses, Step: 1, Symbol: "B", Text: "uses B, defined in step 1"}, b.Why)
	assert.Equal(t, "A", b.Label)
	test := orderStepHunk(t, order, "c_test.go", 0)
	assert.Equal(t, types.DiffWhy{Relation: types.DiffWhyTests, Step: 1, Symbol: "B", Text: "tests B, defined in step 1"}, test.Why)
	assert.True(t, order.Count.Complete)
}

func TestOrderEdges(t *testing.T) {
	t.Parallel()

	unranked := []string{"group unranked \"\" 2", "1 a.go#0 unranked", "2 b.go#0 unranked"}
	tests := []struct {
		name  string
		files []OrderFile
		sites []OrderSite
		want  []string
	}{
		{
			name: "a reference inside a hunk links it to the definer",
			files: []OrderFile{
				orderFile("a.go", orderHunk(0, 1, 5, "S")),
				orderFile("b.go", orderHunk(0, 10, 3)),
			},
			sites: []OrderSite{orderUse("S", "b.go", 11), orderUse("S", "b.go", 12)},
			want:  []string{`group connected "S" 2`, "1 a.go#0 starts", "2 b.go#0 uses"},
		},
		{
			name: "a use of the definer's own symbol inside its own hunk is no edge",
			files: []OrderFile{
				orderFile("a.go", orderHunk(0, 1, 5, "S")),
				orderFile("b.go", orderHunk(0, 10, 3)),
			},
			sites: []OrderSite{orderUse("S", "a.go", 2)},
			want:  unranked,
		},
		{
			name: "a use outside every hunk is no edge",
			files: []OrderFile{
				orderFile("a.go", orderHunk(0, 1, 5, "S")),
				orderFile("b.go", orderHunk(0, 10, 3)),
			},
			sites: []OrderSite{orderUse("S", "b.go", 40), orderUse("S", "b.go", 9), orderUse("S", "b.go", 13)},
			want:  unranked,
		},
		{
			name: "a use in another file's lines is no edge",
			files: []OrderFile{
				orderFile("a.go", orderHunk(0, 1, 5, "S")),
				orderFile("b.go", orderHunk(0, 10, 3)),
			},
			sites: []OrderSite{orderUse("S", "c.go", 11)},
			want:  unranked,
		},
		{
			name: "a pure deletion holds no use",
			files: []OrderFile{
				orderFile("a.go", orderHunk(0, 1, 5, "S")),
				orderFile("b.go", orderHunk(0, 10, 0)),
			},
			sites: []OrderSite{orderUse("S", "b.go", 10)},
			want:  unranked,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, orderOutline(OrderHunks(OrderInput{Files: tc.files, Sites: tc.sites})))
		})
	}
}

func TestOrderCollapsesACycleIntoOneStep(t *testing.T) {
	t.Parallel()

	// a.go defines X, b.go defines Y, they use each other; b.go also feeds c.go, and d.go
	// defines Z which c.go uses.
	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			orderFile("a.go", orderHunk(0, 1, 5, "X")),
			orderFile("b.go", orderHunk(0, 1, 5, "Y")),
			orderFile("c.go", orderHunk(0, 1, 5)),
			orderFile("d.go", orderHunk(0, 1, 5, "Z")),
		},
		Sites: []OrderSite{orderUse("X", "b.go", 2), orderUse("Y", "a.go", 2), orderUse("Y", "c.go", 2), orderUse("Z", "c.go", 3)},
	})

	assert.Equal(t, []string{
		`group connected "X" 4`,
		"1 a.go#0 starts",
		"1 b.go#0 same_step",
		"2 d.go#0 used_by",
		"3 c.go#0 uses",
	}, orderOutline(order), "the cycle is step 1; the second root says which later step uses it")

	first := orderStepHunk(t, order, "a.go", 0)
	assert.Equal(t, []string{"X", "Y"}, first.Why.Cycle)
	assert.Equal(t, "starts the group; 2 hunks share this step because they use each other through X, Y", first.Why.Text)
	second := orderStepHunk(t, order, "b.go", 0)
	assert.Equal(t, types.DiffWhySameStep, second.Why.Relation)
	assert.Equal(t, 1, second.Why.Step)
	assert.Equal(t, []string{"X", "Y"}, second.Why.Cycle)
	root := orderStepHunk(t, order, "d.go", 0)
	assert.Equal(t, types.DiffWhy{Relation: types.DiffWhyUsedBy, Step: 3, Symbol: "Z", Text: "defines Z, used in step 3"}, root.Why)
	assert.Equal(t, 2, orderStepHunk(t, order, "c.go", 0).Why.Step, "the latest-placed definer explains c.go")
}

func TestOrderRanksGroupsBySizeThenReachThenPath(t *testing.T) {
	t.Parallel()

	reach := orderFile("f2.go", orderHunk(0, 1, 5, "Q"))
	reach.Reach = 9
	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			orderFile("f0.go", orderHunk(0, 1, 5, "N")),
			orderFile("f1.go", orderHunk(0, 1, 5)),
			reach,
			orderFile("f3.go", orderHunk(0, 1, 5)),
			orderFile("f4.go", orderHunk(0, 1, 5, "P")),
			orderFile("f5.go", orderHunk(0, 1, 5)),
			orderFile("f6.go", orderHunk(0, 1, 5)),
		},
		Sites: []OrderSite{
			orderUse("N", "f1.go", 2),
			orderUse("Q", "f3.go", 2),
			orderUse("P", "f5.go", 2), orderUse("P", "f6.go", 2),
		},
	})

	var labels []string
	for _, g := range order.Groups {
		labels = append(labels, fmt.Sprintf("%s:%d:%d", g.Label, g.Hunks, g.Reach))
	}
	assert.Equal(t, []string{"P:3:0", "Q:2:9", "N:2:0"}, labels, "largest first, then reach, then path")
}

func TestOrderMergesConsecutiveStepsOfOneFile(t *testing.T) {
	t.Parallel()

	// a#0 defines S; a#1 uses S and defines T; b#0 uses T and defines U; a#2 uses U.
	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			orderFile("a.go", orderHunk(0, 1, 3, "S"), orderHunk(1, 10, 3, "T"), orderHunk(2, 20, 3)),
			orderFile("b.go", orderHunk(0, 1, 3, "U")),
		},
		Sites: []OrderSite{orderUse("S", "a.go", 11), orderUse("T", "b.go", 2), orderUse("U", "a.go", 21)},
	})

	assert.Equal(t, []string{
		`group connected "S" 4`,
		"1 a.go#0 starts",
		"1 a.go#1 uses",
		"2 b.go#0 uses",
		"3 a.go#2 uses",
	}, orderOutline(order))
	assert.Equal(t, "uses S, defined above", orderStepHunk(t, order, "a.go", 1).Why.Text)
	assert.Equal(t, "uses U, defined in step 2", orderStepHunk(t, order, "a.go", 2).Why.Text)
	assert.Len(t, order.Groups[0].Steps, 3)
	assert.Equal(t, 3, order.Groups[0].Steps[2].Number)
}

func TestOrderChainsHunksOfOneDefinition(t *testing.T) {
	t.Parallel()

	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			orderFile("a.go", orderHunk(1, 30, 3, "F"), orderHunk(0, 1, 3, "F")),
			orderFile("b.go", orderHunk(0, 1, 3, "G")),
		},
	})

	assert.Equal(t, []string{
		`group connected "F" 2`,
		"1 a.go#0 starts",
		"1 a.go#1 continues",
		`group unranked "" 1`,
		"2 b.go#0 unranked",
	}, orderOutline(order))
	assert.Equal(t, types.DiffWhy{Relation: types.DiffWhyContinues, Step: 1, Symbol: "F", Text: "continues F from above"}, orderStepHunk(t, order, "a.go", 1).Why)
	assert.Equal(t, "unlinked: no other changed hunk uses, implements or continues what it changes", orderStepHunk(t, order, "b.go", 0).Why.Text)
}

func TestOrderPlacesAnInterfaceBeforeItsImplementation(t *testing.T) {
	t.Parallel()

	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			orderFile("impl.go", orderHunk(0, 1, 5, "Foo")),
			orderFile("iface.go", orderHunk(0, 1, 5, "Doer")),
		},
		Implements: []OrderLink{{Implementer: "Foo", Interface: "Doer"}},
	})

	assert.Equal(t, []string{
		`group connected "Doer" 2`,
		"1 iface.go#0 starts",
		"2 impl.go#0 implements",
	}, orderOutline(order))
	assert.Equal(t, types.DiffWhy{Relation: types.DiffWhyImplements, Step: 1, Symbol: "Doer", Text: "implements Doer, declared in step 1"}, orderStepHunk(t, order, "impl.go", 0).Why)
}

func TestOrderNamesAnInterfaceThatIsNotFirst(t *testing.T) {
	t.Parallel()

	// a.go defines S, which c.go uses while implementing Doer from b.go. a.go and b.go are
	// both roots; b.go is placed second and says which step implements it.
	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			orderFile("a.go", orderHunk(0, 1, 5, "S")),
			orderFile("b.go", orderHunk(0, 1, 5, "Doer")),
			orderFile("c.go", orderHunk(0, 1, 5, "Foo")),
		},
		Sites:      []OrderSite{orderUse("S", "c.go", 2)},
		Implements: []OrderLink{{Implementer: "Foo", Interface: "Doer"}},
	})

	assert.Equal(t, []string{
		`group connected "S" 3`,
		"1 a.go#0 starts",
		"2 b.go#0 implemented_by",
		"3 c.go#0 implements",
	}, orderOutline(order))
	assert.Equal(t, "declares Doer, implemented in step 3", orderStepHunk(t, order, "b.go", 0).Why.Text)
}

func TestOrderPlacesTestsAfterCodeThatIsReadyAlongside(t *testing.T) {
	t.Parallel()

	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			orderFile("a_test.go", orderHunk(0, 1, 5)),
			orderFile("m.go", orderHunk(0, 1, 5)),
			orderFile("z.go", orderHunk(0, 1, 5, "Q")),
		},
		Sites:  []OrderSite{orderUse("Q", "a_test.go", 2), orderUse("Q", "m.go", 2)},
		IsTest: orderIsTest,
	})

	assert.Equal(t, []string{
		`group connected "Q" 3`,
		"1 z.go#0 starts",
		"2 m.go#0 uses",
		"3 a_test.go#0 tests",
	}, orderOutline(order))
}

func TestOrderPutsGeneratedThenUnrankedGroupsLast(t *testing.T) {
	t.Parallel()

	gen := orderFile("gen/out.go", orderHunk(0, 1, 5, "Out"), orderHunk(1, 20, 5))
	gen.Generated = true
	moved := orderFile("moved.go", orderHunk(0, 1, 5, "Moved"))
	moved.Moved = true
	withSymbols := orderFile("withsyms.go", orderHunk(0, 1, 5))
	withSymbols.Symbols = []types.DiffSymbol{{ID: "K"}}
	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			moved,
			gen,
			orderFile("noindex.go", orderHunk(0, 1, 5)),
			orderFile("a.go", orderHunk(0, 1, 5, "S")),
			orderFile("b.go", orderHunk(0, 1, 5)),
			withSymbols,
		},
		Sites: []OrderSite{orderUse("S", "b.go", 2), orderUse("Out", "a.go", 2), orderUse("Moved", "a.go", 3)},
	})

	assert.Equal(t, []string{
		`group connected "S" 2`,
		"1 a.go#0 starts",
		"2 b.go#0 uses",
		`group generated "" 2`,
		"3 gen/out.go#0 generated",
		"3 gen/out.go#1 generated",
		`group unranked "" 3`,
		"4 moved.go#0 unranked",
		"5 noindex.go#0 unranked",
		"6 withsyms.go#0 unranked",
	}, orderOutline(order), "generated hunks share a screen per file and never link; moved hunks never link")
	assert.Equal(t, "the working tree no longer matches the range head for this file", orderStepHunk(t, order, "moved.go", 0).Why.Text)
	assert.Equal(t, "no symbol index covers this file", orderStepHunk(t, order, "noindex.go", 0).Why.Text)
	assert.Equal(t, "defines no changed symbol and uses none", orderStepHunk(t, order, "withsyms.go", 0).Why.Text)
	assert.Equal(t, "generated output; its source carries the review", orderStepHunk(t, order, "gen/out.go", 0).Why.Text)
	assert.True(t, order.Count.Complete)
	assert.Equal(t, 7, order.Count.Hunks)
}

func TestOrderNamesAFileWithNoHunkInBare(t *testing.T) {
	t.Parallel()

	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			{Path: "logo.png"},
			orderFile("a.go", orderHunk(0, 1, 5)),
			{Path: "also.bin"},
		},
	})

	assert.Equal(t, []string{"also.bin", "logo.png"}, order.Count.Bare)
	assert.Equal(t, 1, order.Count.Hunks)
	assert.True(t, order.Count.Complete)
}

func TestOrderLabelsAGroupByItsFirstDefinedSymbol(t *testing.T) {
	t.Parallel()

	a := orderFile("a.go", orderHunk(0, 1, 5, "m/pkg/Type#Method()."))
	a.Symbols = []types.DiffSymbol{{ID: "m/pkg/Type#Method().", Label: "Method", Qualified: "Type.Method"}}
	order := OrderHunks(OrderInput{
		Files: []OrderFile{a, orderFile("b.go", orderHunk(0, 1, 5))},
		Sites: []OrderSite{orderUse("m/pkg/Type#Method().", "b.go", 2)},
	})

	assert.Equal(t, "Type.Method", order.Groups[0].Label)
	assert.Equal(t, "Type.Method", orderStepHunk(t, order, "a.go", 0).Label)
	assert.Equal(t, "", orderStepHunk(t, order, "b.go", 0).Label, "no symbol and no declaration")
	assert.Equal(t, "uses Type.Method, defined in step 1", orderStepHunk(t, order, "b.go", 0).Why.Text)
}

func TestOrderLabelsASymbolTheFileDoesNotListFromItsID(t *testing.T) {
	t.Parallel()

	id := "symbol:gomod m `m/pkg/knowledge`/goShapes#typeClass()."
	order := OrderHunks(OrderInput{
		Files: []OrderFile{
			{Path: "a.go", Hunks: []types.DiffHunk{orderHunk(0, 1, 5, id)}},
			orderFile("b.go", orderHunk(0, 1, 5)),
		},
		Sites: []OrderSite{orderUse(id, "b.go", 2)},
	})
	assert.Equal(t, "goShapes.typeClass", order.Groups[0].Label)
}

func TestOrderLabelsAHunkWithNoSymbolByItsDeclaration(t *testing.T) {
	t.Parallel()

	h := orderHunk(0, 1, 5)
	h.Declaration = "func helper()"
	order := OrderHunks(OrderInput{Files: []OrderFile{orderFile("a.go", h)}})
	assert.Equal(t, "func helper()", orderStepHunk(t, order, "a.go", 0).Label)
}

func TestOrderQualifiesASymbolByItsDescriptors(t *testing.T) {
	t.Parallel()

	tests := []struct{ id, want string }{
		{"symbol:gomod m `m/pkg/knowledge`/goShapes#typeClass().", "goShapes.typeClass"},
		{"symbol:gomod m `m/pkg`/Precedents().", "Precedents"},
		{"weird", "weird"},
		{"pkg/", "fallback"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, qualifySymbolID(tc.id, "fallback"), tc.id)
	}
}

func TestOrderCountFlagsAHunkPlacedTwice(t *testing.T) {
	t.Parallel()

	order := OrderHunks(OrderInput{Files: []OrderFile{
		orderFile("a.go", orderHunk(0, 1, 5)),
		orderFile("a.go", orderHunk(0, 1, 5)),
	}})

	assert.False(t, order.Count.Complete)
	assert.Equal(t, 2, order.Count.Placed)
	assert.Equal(t, []types.DiffHunkRef{{Path: "a.go", Index: 0, Digest: "digest-0"}}, order.Count.Repeated)
	assert.Empty(t, order.Count.Missing)
}

// orderFixture is a changeset with a cycle, an interface, tests, a generated file, a moved
// file and a bare file, so shuffling it exercises every branch of the ordering.
func orderFixture() OrderInput {
	gen := orderFile("gen/out.go", orderHunk(0, 1, 5), orderHunk(1, 9, 5))
	gen.Generated = true
	moved := orderFile("moved.go", orderHunk(0, 1, 5, "Moved"))
	moved.Moved = true
	return OrderInput{
		Files: []OrderFile{
			orderFile("a.go", orderHunk(0, 1, 5, "X"), orderHunk(1, 20, 5, "W"), orderHunk(2, 40, 5)),
			orderFile("b.go", orderHunk(0, 1, 5, "Y")),
			orderFile("c.go", orderHunk(0, 1, 5, "Z")),
			orderFile("d.go", orderHunk(0, 1, 5)),
			orderFile("iface.go", orderHunk(0, 1, 5, "Doer")),
			orderFile("impl.go", orderHunk(0, 1, 5, "Foo")),
			orderFile("n.go", orderHunk(0, 1, 5, "N")),
			orderFile("m.go", orderHunk(0, 1, 5)),
			orderFile("x_test.go", orderHunk(0, 1, 5), orderHunk(1, 10, 5)),
			orderFile("lone.go", orderHunk(0, 1, 5, "Lone")),
			orderFile("nosyms.go", orderHunk(0, 1, 5)),
			{Path: "logo.png"},
			gen,
			moved,
		},
		Sites: []OrderSite{
			orderUse("X", "b.go", 2), orderUse("Y", "a.go", 2), orderUse("Y", "c.go", 2), orderUse("Z", "d.go", 2),
			orderUse("W", "a.go", 42), orderUse("N", "m.go", 3), orderUse("N", "x_test.go", 3), orderUse("X", "x_test.go", 12),
			orderUse("Foo", "x_test.go", 4), orderUse("Doer", "d.go", 3),
		},
		Implements: []OrderLink{{Implementer: "Foo", Interface: "Doer"}},
		IsTest:     orderIsTest,
	}
}

func TestOrderIsDeterministicUnderShuffle(t *testing.T) {
	t.Parallel()

	want := OrderHunks(orderFixture())
	require.True(t, want.Count.Complete)
	require.NotEmpty(t, want.Groups)

	rng := rand.New(rand.NewPCG(1, 2))
	for range 50 {
		in := orderFixture()
		rng.Shuffle(len(in.Files), func(i, j int) { in.Files[i], in.Files[j] = in.Files[j], in.Files[i] })
		for _, f := range in.Files {
			rng.Shuffle(len(f.Hunks), func(i, j int) { f.Hunks[i], f.Hunks[j] = f.Hunks[j], f.Hunks[i] })
			rng.Shuffle(len(f.Symbols), func(i, j int) { f.Symbols[i], f.Symbols[j] = f.Symbols[j], f.Symbols[i] })
		}
		rng.Shuffle(len(in.Sites), func(i, j int) { in.Sites[i], in.Sites[j] = in.Sites[j], in.Sites[i] })
		assert.Equal(t, want, OrderHunks(in))
	}
}

func TestOrderPlacesEveryHunkExactlyOnce(t *testing.T) {
	t.Parallel()

	in := orderFixture()
	order := OrderHunks(in)

	counted := map[string]int{}
	for _, g := range order.Groups {
		total := 0
		for _, st := range g.Steps {
			for _, h := range st.Hunks {
				counted[fmt.Sprintf("%s#%d", h.Hunk.Path, h.Hunk.Index)]++
				total++
			}
		}
		assert.Equal(t, g.Hunks, total, "group size matches its steps")
	}
	hunks := 0
	for _, f := range in.Files {
		for _, h := range f.Hunks {
			hunks++
			assert.Equal(t, 1, counted[fmt.Sprintf("%s#%d", f.Path, h.Index)], "%s#%d", f.Path, h.Index)
		}
	}
	assert.Equal(t, types.DiffOrderCount{Hunks: hunks, Placed: hunks, Complete: true, Bare: []string{"logo.png"}}, order.Count)

	step := 0
	for _, g := range order.Groups {
		for _, st := range g.Steps {
			step++
			assert.Equal(t, step, st.Number, "steps count on across groups")
		}
	}
	last := order.Groups[len(order.Groups)-1]
	assert.Equal(t, types.DiffGroupUnranked, last.Kind)
}

func TestOrderOfNothingIsAnEmptyCompleteOrder(t *testing.T) {
	t.Parallel()

	order := OrderHunks(OrderInput{})
	assert.Empty(t, order.Groups)
	assert.True(t, order.Count.Complete)
}
