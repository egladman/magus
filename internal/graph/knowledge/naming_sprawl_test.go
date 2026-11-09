package knowledge

import (
	"fmt"
	"github.com/egladman/magus/types/diff"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

const sprawlDir = "internal/obs"

// sprawlMethod is a method of owner in internal/obs taking params.
func sprawlMethod(owner, name, params string) types.KnowledgeSymbol {
	return goSym(sprawlDir, fmt.Sprintf("%s#%s().", owner, name), name, "Method", fmt.Sprintf("func (%s).%s(%s)", owner, name, params), fmt.Sprintf("%s/x.go", sprawlDir))
}

const cacheParams = "ctx context.Context, name string"

func cacheTrio(owner string) []types.KnowledgeSymbol {
	return []types.KnowledgeSymbol{
		sprawlMethod(owner, "RecordCacheHit", cacheParams),
		sprawlMethod(owner, "RecordCacheMiss", cacheParams),
		sprawlMethod(owner, "RecordCacheError", cacheParams),
	}
}

func sprawlMessages(t *testing.T, got map[string][]types.Check) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for id, checks := range got {
		for _, c := range checks {
			require.Equal(t, diff.CheckSiblingSprawl, c.Name)
			require.Equal(t, types.CheckAdvice, c.Status)
			out[id] = append(out[id], c.Message)
		}
	}
	return out
}

const cacheTrioMessage = "`RecordCacheError` joins `RecordCache<X>` on `recorder`: 3 members differ in that one word (error, hit, miss)"

func TestSprawlCheckReportsTheCacheTrio(t *testing.T) {
	trio := cacheTrio("recorder")
	subj := trio[2]
	g := namingGraph(t, trio)

	got := g.ConformanceChecks(ConformanceChange{Conformance: testConformance(), Subjects: subjectIDs(subj), Introduced: introduced(subj)})

	assert.Equal(t, map[string][]types.Check{symID(subj): {advice(diff.CheckSiblingSprawl, cacheTrioMessage,
		"members: RecordCacheError, RecordCacheHit, RecordCacheMiss",
		"3 share the parameters func(ctx, string) none: RecordCacheError, RecordCacheHit, RecordCacheMiss",
	)}}, got)
}

func TestSprawlCheckIsSilent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		syms    []types.KnowledgeSymbol
		subject int
		fresh   []int
	}{
		{
			name: "Get, Put and Has are different operations, not complements",
			syms: []types.KnowledgeSymbol{
				sprawlMethod("remoteBackend", "GetBlob", "ctx context.Context, key string"),
				sprawlMethod("remoteBackend", "PutBlob", "ctx context.Context, key string"),
				sprawlMethod("remoteBackend", "HasBlob", "ctx context.Context, key string"),
			},
			subject: 2,
		},
		{
			name: "a family whose members take different parameters stays under the bar",
			syms: []types.KnowledgeSymbol{
				sprawlMethod("policy", "CheckRead", "path string"),
				sprawlMethod("policy", "CheckWrite", "path string, mode int"),
				sprawlMethod("policy", "CheckExec", "ctx context.Context, path string"),
			},
			subject: 2,
		},
		{
			name: "a change inventing a family joins none",
			syms: append(append(
				[]types.KnowledgeSymbol{sprawlMethod("poolA", "RecordPoolAcquire", cacheParams), sprawlMethod("poolA", "RecordPoolRelease", cacheParams)},
				sprawlMethod("poolB", "RecordPoolAcquire", cacheParams), sprawlMethod("poolB", "RecordPoolRelease", cacheParams)),
				sprawlMethod("poolC", "RecordPoolAcquire", cacheParams), sprawlMethod("poolC", "RecordPoolRelease", cacheParams)),
			subject: 0,
			fresh:   []int{0, 1, 2, 3, 4, 5},
		},
		{
			name: "one-word names are an interface's vocabulary",
			syms: []types.KnowledgeSymbol{
				sprawlMethod("file", "Read", "p []byte"),
				sprawlMethod("file", "Write", "p []byte"),
				sprawlMethod("file", "Exec", "p []byte"),
			},
			subject: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subj := tc.syms[tc.subject]
			fresh := introduced(subj)
			for _, i := range tc.fresh {
				fresh[symID(tc.syms[i])] = true
			}
			g := namingGraph(t, tc.syms)

			assert.Empty(t, g.ConformanceChecks(ConformanceChange{Conformance: testConformance(), Subjects: subjectIDs(subj), Introduced: fresh}))
		})
	}
}

// An interface and its implementers repeat one family; adding the new member to every carrier
// is one finding, on the first carrier by ID.
func TestSprawlCheckReportsACarriedFamilyOnce(t *testing.T) {
	var syms, added []types.KnowledgeSymbol
	for _, owner := range []string{"Provider", "otelProvider", "disabledProvider"} {
		trio := cacheTrio(owner)
		syms = append(syms, trio...)
		added = append(added, trio[2])
	}
	g := namingGraph(t, syms)

	got := g.ConformanceChecks(ConformanceChange{Conformance: testConformance(), Subjects: subjectIDs(added...), Introduced: introduced(added...)})

	assert.Equal(t, map[string][]string{symID(added[0]): {
		"`RecordCacheError` joins `RecordCache<X>` on `Provider`: 3 members differ in that one word (error, hit, miss)",
	}}, sprawlMessages(t, got))
	assert.Equal(t, []string{
		"members: RecordCacheError, RecordCacheHit, RecordCacheMiss",
		"3 share the parameters func(ctx, string) none: RecordCacheError, RecordCacheHit, RecordCacheMiss",
		"3 types carry it: Provider, disabledProvider, otelProvider",
	}, got[symID(added[0])][0].Details)
}

// Two members clear the size gate when three owners carry them.
func TestSprawlCheckReportsAPairThreeOwnersCarry(t *testing.T) {
	var syms []types.KnowledgeSymbol
	for _, owner := range []string{"sessionA", "sessionB", "sessionC"} {
		syms = append(syms, sprawlMethod(owner, "RecordSessionReuse", cacheParams), sprawlMethod(owner, "RecordSessionEviction", "ctx context.Context"))
	}
	subj := syms[5]
	g := namingGraph(t, syms)

	got := g.ConformanceChecks(ConformanceChange{Conformance: testConformance(), Subjects: subjectIDs(subj), Introduced: introduced(subj)})

	assert.Equal(t, map[string][]string{symID(subj): {
		"`RecordSessionEviction` joins `RecordSession<X>` on `sessionC`: 2 members differ in that one word (eviction, reuse)",
	}}, sprawlMessages(t, got))
}

func TestSprawlCheckNeverCountsGeneratedCode(t *testing.T) {
	trio := cacheTrio("recorder")
	trio[0].Source, trio[0].Defs = sprawlDir+"/x_gen.go:1", []string{sprawlDir + "/x_gen.go"}
	trio[1].Source, trio[1].Defs = sprawlDir+"/x_gen.go:2", []string{sprawlDir + "/x_gen.go"}
	subj := trio[2]
	g := namingGraph(t, trio)

	got := g.ConformanceChecks(ConformanceChange{
		Conformance: testConformance(), Subjects: subjectIDs(subj), Introduced: introduced(subj),
		Generated: map[string]bool{sprawlDir + "/x_gen.go": true},
	})

	assert.Empty(t, got, "Hit and Miss are generated, so Error joins no family")
}

// WideTypeSuite pins the wide-type bonus: it reads the configured type-methods anchor, and
// nothing when none is configured, so a thin family clears the bar only on an owner past it.
type WideTypeSuite struct {
	suite.Suite
	g    *Graph
	subj types.KnowledgeSymbol
}

func TestWideTypeSuite(t *testing.T) {
	suite.Run(t, new(WideTypeSuite))
}

func (s *WideTypeSuite) SetupTest() {
	syms := []types.KnowledgeSymbol{
		sprawlMethod("store", "CheckRead", "path string"),
		sprawlMethod("store", "CheckWrite", "path string, mode int"),
		sprawlMethod("store", "CheckExec", "ctx context.Context, path string"),
		sprawlMethod("store", "Open", "path string"),
		sprawlMethod("store", "Close", ""),
	}
	s.subj = syms[2]
	s.g = namingGraph(s.T(), syms)
}

// checks is what the sprawl check says of the subject with anchor configured, or none at 0.
func (s *WideTypeSuite) checks(anchor int) map[string][]types.Check {
	o := testConformance()
	if anchor > 0 {
		key := types.ConformanceKey{Metric: types.ConformanceTypeMethods, Population: types.ConformancePopSource}
		o.Limits = map[types.ConformanceKey]types.ConformanceLimit{key: {Percentile: 95, Anchor: anchor}}
	}
	return s.g.ConformanceChecks(ConformanceChange{Conformance: o, Subjects: subjectIDs(s.subj), Introduced: introduced(s.subj)})
}

func (s *WideTypeSuite) TestNoAnchorConfiguredGivesNoBonus() {
	s.Empty(s.checks(0))
}

func (s *WideTypeSuite) TestAnOwnerUnderTheAnchorGivesNoBonus() {
	s.Empty(s.checks(49))
}

func (s *WideTypeSuite) TestAnOwnerPastTheAnchorClearsTheBar() {
	got := s.checks(4)
	s.Require().Len(got[symID(s.subj)], 1)
	s.Equal([]string{
		"members: CheckExec, CheckRead, CheckWrite",
		"`store` has 5 methods, past the configured anchor of 4 for type-methods source",
	}, got[symID(s.subj)][0].Details)
}

func TestSprawlCheckIsDeterministic(t *testing.T) {
	var syms, added []types.KnowledgeSymbol
	for _, owner := range []string{"Provider", "otelProvider", "disabledProvider"} {
		trio := cacheTrio(owner)
		syms = append(syms, trio...)
		added = append(added, trio[2])
	}
	change := ConformanceChange{Conformance: testConformance(), Subjects: subjectIDs(added...), Introduced: introduced(added...)}
	first := namingGraph(t, syms).ConformanceChecks(change)
	require.NotEmpty(t, first)
	for range 10 {
		assert.Equal(t, first, namingGraph(t, syms).ConformanceChecks(change))
	}
}

func TestSprawlLexiconHoldsEachWordOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, set := range sprawlLexicon {
		for _, w := range set {
			assert.False(t, seen[w], "%s sits in two sets", w)
			seen[w] = true
		}
	}
}
