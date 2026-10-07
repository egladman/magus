package knowledge

import (
	"cmp"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

// refResolver is the pre-index Resolve, kept verbatim in behavior as the oracle the
// index-backed Resolve is held to: a linear scan over g.nodes that lowercases, scores
// through interactive.LeafScore and finds a node's project by a longest-first prefix scan
// of every project path, per node, per call. It is slow on purpose. Do not "improve" it:
// its only job is to be obviously the old semantics.
type refResolver struct {
	g *Graph
	// projects is every project node's path, longest first then lexical, the order the old
	// projectPaths scan relied on.
	projects []string
}

func newRefResolver(g *Graph) *refResolver {
	var paths []string
	for id := range g.nodes {
		if p, ok := strings.CutPrefix(id, types.KindProject+":"); ok {
			paths = append(paths, p)
		}
	}
	slices.SortFunc(paths, func(a, b string) int {
		if c := cmp.Compare(len(b), len(a)); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	return &refResolver{g: g, projects: paths}
}

func (r *refResolver) resolve(input string, limit int) []types.KnowledgeMatch {
	q := r.g.normalizePaths(parseQuery(input))
	var matches []types.KnowledgeMatch
	for id, n := range r.g.nodes {
		score, ok := r.scoreNode(n, id, q)
		if !ok {
			continue
		}
		m := types.KnowledgeMatch{ID: id, Kind: n.Kind, Label: n.Label, Score: score}
		m.Staleness, m.OutrunDays = stalenessLabel(n.Attrs)
		matches = append(matches, m)
	}
	slices.SortFunc(matches, func(a, b types.KnowledgeMatch) int {
		if a.Score != b.Score {
			return cmp.Compare(b.Score, a.Score)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func (r *refResolver) projectOf(n types.KnowledgeNode, id string) (string, bool) {
	if p, ok := nodeProjectPath(id); ok {
		return p, true
	}
	src := n.Source
	if i := strings.IndexByte(src, ':'); i >= 0 {
		src = src[:i]
	}
	if src == "" {
		return "", false
	}
	for _, p := range r.projects {
		if p == "." || src == p || strings.HasPrefix(src, p+"/") {
			return p, true
		}
	}
	return "", false
}

func refContainsAny(hay string, needles []string) bool {
	lh := strings.ToLower(hay)
	for _, n := range needles {
		if hasWildcard(n) {
			if refGlobMatch(n, hay) {
				return true
			}
		} else if strings.Contains(lh, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

func refGlobMatch(pattern, s string) bool {
	p, str := strings.ToLower(pattern), strings.ToLower(s)
	parts := strings.Split(p, "*")
	if len(parts) == 1 {
		return p == str
	}
	if !strings.HasPrefix(str, parts[0]) {
		return false
	}
	str = str[len(parts[0]):]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(str, mid)
		if i < 0 {
			return false
		}
		str = str[i+len(mid):]
	}
	return strings.HasSuffix(str, parts[len(parts)-1])
}

func (r *refResolver) touchesRelation(id string, rels []string) bool {
	r.g.ensureAdj()
	relSet := toSet(rels)
	for _, e := range r.g.out[id] {
		if relSet[string(e.Relation)] {
			return true
		}
	}
	for _, e := range r.g.in[id] {
		if relSet[string(e.Relation)] {
			return true
		}
	}
	return false
}

func (r *refResolver) scoreNode(n types.KnowledgeNode, id string, q parsedQuery) (int, bool) {
	if vals, ok := q.fields["kind"]; ok && !matchesKind(n.Kind, vals) {
		return 0, false
	}
	if vals := q.negFields["kind"]; matchesKind(n.Kind, vals) {
		return 0, false
	}
	if res := q.reFields["kind"]; len(res) > 0 && !matchesAnyRe(n.Kind, res) {
		return 0, false
	}
	if vals, ok := q.fields["project"]; ok {
		proj, owned := r.projectOf(n, id)
		if !owned || !matchesProject(proj, vals) {
			return 0, false
		}
	}
	if vals := q.negFields["project"]; len(vals) > 0 {
		if proj, owned := r.projectOf(n, id); owned && matchesProject(proj, vals) {
			return 0, false
		}
	}
	if res := q.reFields["project"]; len(res) > 0 {
		if proj, owned := r.projectOf(n, id); !owned || !matchesAnyRe(proj, res) {
			return 0, false
		}
	}
	if vals, ok := q.fields["id"]; ok && !refContainsAny(id, vals) {
		return 0, false
	}
	if vals := q.negFields["id"]; refContainsAny(id, vals) {
		return 0, false
	}
	if res := q.reFields["id"]; len(res) > 0 && !matchesAnyRe(id, res) {
		return 0, false
	}
	if vals, ok := q.fields["language"]; ok && !slices.Contains(vals, n.Attrs["language"]) {
		return 0, false
	}
	if vals := q.negFields["language"]; slices.Contains(vals, n.Attrs["language"]) {
		return 0, false
	}
	if res := q.reFields["language"]; len(res) > 0 && !matchesAnyRe(n.Attrs["language"], res) {
		return 0, false
	}
	if vals, ok := q.fields["role"]; ok && !slices.Contains(vals, n.Attrs[attrRole]) {
		return 0, false
	}
	if vals := q.negFields["role"]; slices.Contains(vals, n.Attrs[attrRole]) {
		return 0, false
	}
	if res := q.reFields["role"]; len(res) > 0 && !matchesAnyRe(n.Attrs[attrRole], res) {
		return 0, false
	}
	for field, attr := range map[string]string{"layer": types.AttrLayer, "family": types.AttrMarkerFamily} {
		if vals, ok := q.fields[field]; ok && !slices.Contains(vals, n.Attrs[attr]) {
			return 0, false
		}
		if vals := q.negFields[field]; slices.Contains(vals, n.Attrs[attr]) {
			return 0, false
		}
		if res := q.reFields[field]; len(res) > 0 && !matchesAnyRe(n.Attrs[attr], res) {
			return 0, false
		}
	}
	if !matchesStamp(n, q) {
		return 0, false
	}

	hay := strings.ToLower(id + " " + n.Label + " " + n.Doc)
	for _, t := range q.negTerms {
		if hasWildcard(t) {
			if refGlobMatch(t, id) || refGlobMatch(t, n.Label) {
				return 0, false
			}
		} else if strings.Contains(hay, strings.ToLower(t)) {
			return 0, false
		}
	}

	if len(q.terms) == 0 {
		if _, relOnly := q.fields["relation"]; relOnly && !r.touchesRelation(id, q.fields["relation"]) {
			return 0, false
		}
		return 1 + kindRank(n.Kind) + r.g.citedRank(id, n.Kind), true
	}

	total := 0
	for _, t := range q.terms {
		if hasWildcard(t) {
			if !refGlobMatch(t, id) && !refGlobMatch(t, n.Label) {
				return 0, false
			}
			total += wildcardTermScore
			continue
		}
		best := max(interactive.LeafScore(id, t), interactive.LeafScore(n.Label, t))
		if best <= 0 {
			switch lt := strings.ToLower(t); {
			case strings.Contains(strings.ToLower(id), lt),
				strings.Contains(strings.ToLower(n.Doc), lt):
				best = 1
			default:
				return 0, false
			}
		}
		total += best
	}
	return total + kindRank(n.Kind) + r.g.citedRank(id, n.Kind), true
}

// requireResolveMatchesReference holds g.Resolve to the reference for every query, at no
// limit and at a small one (the limit cuts after the sort, so it checks the order too). It
// returns how many queries matched anything, so a caller can refuse a vacuous run.
func requireResolveMatchesReference(t *testing.T, g *Graph, queries []string) int {
	t.Helper()
	ref := newRefResolver(g)
	nonEmpty := 0
	for _, in := range queries {
		all := ref.resolve(in, 0)
		require.Equalf(t, all, g.Resolve(in, 0), "Resolve(%q, 0) diverged from the reference scan", in)
		first := all[:min(7, len(all))]
		if len(all) == 0 {
			first = nil
		}
		require.Equalf(t, first, g.Resolve(in, 7), "Resolve(%q, 7) diverged from the reference scan", in)
		if len(all) > 0 {
			nonEmpty++
		}
	}
	return nonEmpty
}

// withQueryCorpus adds nodes to g that the synthetic fixture lacks and the filters need:
// language, role, layer and marker-stamp attrs, a doc and a file under a project, mixed-case
// and non-ASCII text, a node with no source, and a stale doc. It also adds a root "."
// project and a one-character project, the pair whose relative order the old prefix scan
// made a quirk (see owningProject).
func withQueryCorpus(g *Graph) {
	add := func(n types.KnowledgeNode) { g.AddNode(n) }
	add(types.KnowledgeNode{ID: "project:.", Kind: types.KindProject, Label: "root"})
	add(types.KnowledgeNode{ID: "project:a", Kind: types.KindProject, Label: "a"})
	add(types.KnowledgeNode{ID: "project:pkg/p00010/inner", Kind: types.KindProject, Label: "inner"})
	add(types.KnowledgeNode{ID: "file:pkg/p00010/inner/x.go", Kind: types.KindFile, Label: "x.go", Source: "pkg/p00010/inner/x.go",
		Attrs: map[string]string{"language": "go"}})
	add(types.KnowledgeNode{ID: "file:pkg/p00010/y.py", Kind: types.KindFile, Label: "y.py", Source: "pkg/p00010/y.py:12",
		Attrs: map[string]string{"language": "python"}})
	add(types.KnowledgeNode{ID: "file:a/z.go", Kind: types.KindFile, Label: "z.go", Source: "a/z.go", Attrs: map[string]string{"language": "go"}})
	add(types.KnowledgeNode{ID: "file:loose/w.go", Kind: types.KindFile, Label: "w.go", Source: "loose/w.go", Attrs: map[string]string{"language": "go"}})
	add(types.KnowledgeNode{ID: "doc:README.md", Kind: types.KindDoc, Label: "README", Doc: "Overview of the Workspace and its BUILD story",
		Source: "README.md", Attrs: map[string]string{attrRole: "readme"}})
	add(types.KnowledgeNode{ID: "doc:docs/guide.md", Kind: types.KindDoc, Label: "Guide", Doc: "How to build; mentions Straße and İstanbul",
		Source: "docs/guide.md", Attrs: map[string]string{attrRole: "guide", AttrStaleness: StalenessOutrun, AttrOutrunDays: "400"}})
	add(types.KnowledgeNode{ID: "dir:pkg/p00010", Kind: types.KindDir, Label: "p00010", Source: "pkg/p00010",
		Attrs: map[string]string{types.AttrLayer: "core"}})
	add(types.KnowledgeNode{ID: "marker:one", Kind: types.KindMarker, Label: "magus:lint", Source: "pkg/p00010/y.py:3",
		Attrs: map[string]string{types.AttrMarkerFamily: "lint", "owner": "alice"}})
	add(types.KnowledgeNode{ID: "marker:two", Kind: types.KindMarker, Label: "magus:gen", Source: "a/z.go:9",
		Attrs: map[string]string{types.AttrMarkerFamily: "gen", "owner": "bob"}})
	add(types.KnowledgeNode{ID: "symbol:pkg/p00010 Foo().", Kind: types.KindSymbol, Label: "Foo", Doc: "Foo builds things", Source: "pkg/p00010/inner/x.go:4"})
	add(types.KnowledgeNode{ID: "symbol:ext Bar().", Kind: types.KindSymbol, Label: "Bar"}) // no source: owned by nothing
	// A target in the corpus's own project ties a file, a doc and a target to "build".
	add(types.KnowledgeNode{ID: "target:pkg/p00010:build", Kind: types.KindTarget, Label: "build", Doc: "Builds p00010"})
	g.AddEdge(types.KnowledgeEdge{Source: "doc:docs/guide.md", Target: "doc:README.md", Relation: types.RelationReferences, Confidence: types.ConfidenceExtracted})
	g.AddEdge(types.KnowledgeEdge{Source: "file:pkg/p00010/y.py", Target: "doc:docs/guide.md", Relation: types.RelationReferences, Confidence: types.ConfidenceExtracted})
	g.AddEdge(types.KnowledgeEdge{Source: "file:pkg/p00010/inner/x.go", Target: "doc:README.md", Relation: types.RelationReferences, Confidence: types.ConfidenceExtracted})
}

// resolveQueries cover each filter the grammar has, alone and combined: field filters
// (=, :, !=, =~) on every known field, negated and wildcard terms, phrases, multi-term AND,
// case, non-ASCII, relation-only queries and project=.
var resolveQueries = []string{
	"t003", "T003", "kind:target t003", "kind=target t003 project=pkg/p00010", "kind=target t00 t003", "build", "BUILD", "build -test",
	"build -t003", "-build", "-\"overview of\"", "-\"p00010 build\"", "-\"x.go\"", "\"pkg/p00010 t003\"", "\"workspace and\"",
	"kind=*", "kind=sp*", "kind!=target", "kind!=target kind!=project", "kind=target kind=spell", "kind=~^(proj|spe)", "kind=~^(proj|spe) go",
	"kind=file", "kind=file language=go", "kind=file language!=go", "language=~^py", "language=go project=a", "language=go project=.",
	"kind=doc", "kind=doc role=readme", "kind=doc role!=readme", "role=~gui", "kind=doc stale", "kind=doc BUILD",
	"layer=core", "layer!=core kind=dir", "layer=~^co", "family=lint", "family!=lint kind=marker", "family=~^g", "kind=marker stamp=owner=alice",
	"stamp=alice", "stamp!=alice kind=marker", "stamp=~^owner=b", "stamp=owner=carol",
	"project=pkg/p00010", "project=pkg/p00010 kind=file", "project=pkg/p00010/inner", "project=pkg/p0001*", "project=pkg/*", "project=a", "project=.",
	"project!=pkg/p00010 kind=file", "project!=. kind=file", "project=~p0001 kind=target", "project=~^a$", "project=pkg/p00010 -x.go",
	"project=nosuch", "project=pkg/p00010 build", "project=pkg/p00010 foo",
	"id=t003 kind=target", "id!=p000 kind=project", "id=~t00[1-3]$", "id=*p0001* kind=target", "id=x.go", "id!=x.go kind=file", "id:README",
	"pkg/p0000*", "*t003", "*t003 -*p0001*", "p0000* t003", "*", "doc:*guide*", "x.go", "go", "GO", "foo", "Bar", "ext", "straße", "STRASSE", "istanbul", "İstanbul",
	"relation=uses", "relation:contains kind=project", "relation=references", "relation=references kind=doc", "relation=nosuch",
	"relation=uses t003", "relation=references README", "relation=~references", "relation!=uses",
	"kind=project", "kind=project p0001", "kind=project pkg/p00010", "kind=spell go", "kind=diagnostic", "kind=module m01", "kind=op build",
	"spell:go", "mod01", "p00010/inner", "pkg/p00010/", "/", "a", "p", "",
}

func TestResolveMatchesReferenceOnLargeGraph(t *testing.T) {
	g := largeGraph(t)
	withQueryCorpus(g)
	nonEmpty := requireResolveMatchesReference(t, g, resolveQueries)
	// A guard against the corpus drifting into a vacuous pass: most queries must match.
	assert.Greater(t, nonEmpty, len(resolveQueries)*2/3, "too few queries matched anything; the differential would prove little")
}

// TestResolveMatchesReferenceOnQuirkGraph is the same differential on a small graph built
// to hit every oddity at once, so a regression names a specific shape rather than a score.
func TestResolveMatchesReferenceOnQuirkGraph(t *testing.T) {
	g := NewGraph()
	withQueryCorpus(g)
	g.AddNode(types.KnowledgeNode{ID: "project:web", Kind: types.KindProject, Label: "Web"})
	g.AddNode(types.KnowledgeNode{ID: "file:web/ui/App.tsx", Kind: types.KindFile, Label: "App.tsx", Source: "web/ui/App.tsx:3"})
	g.AddNode(types.KnowledgeNode{ID: "file:foo", Kind: types.KindFile, Label: "Bar baz", Doc: "qux"})
	g.AddNode(types.KnowledgeNode{ID: "file:/abs", Kind: types.KindFile, Label: "abs", Source: "/abs/f.go"})
	g.AddNode(types.KnowledgeNode{ID: "project:", Kind: types.KindProject, Label: "empty"})
	queries := append([]string{
		"project=web", "project=. kind=file", "project!=web", "kind=file -\"foo bar\"", "-\"foo bar baz\"", "-\"bar baz qux\"", "-\"foo bar baz qux\"",
		"file:foo", "bar", "baz qux", "\"bar baz\"", "-qux", "qux", "app.tsx", "APP", "project=web kind=file", "project=",
	}, resolveQueries...)
	requireResolveMatchesReference(t, g, queries)
}

func TestResolveMatchesReferenceOnRandomQueries(t *testing.T) {
	g := largeGraph(t)
	withQueryCorpus(g)
	rng := rand.New(rand.NewSource(1))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	kinds := []string{"target", "project", "file", "doc", "spell", "marker", "symbol", "dir", "op", "kind"}
	terms := []string{"t003", "t00", "build", "go", "foo", "README", "p0001", "x", "pkg/p00010", "magus", "owner", "*t00*", "p000*"}
	projects := []string{"pkg/p00010", "pkg/p00010/inner", "a", ".", "pkg/*", "web", "loose"}
	var queries []string
	for range 80 {
		var parts []string
		if rng.Intn(2) == 0 {
			parts = append(parts, "kind="+pick(kinds))
		}
		if rng.Intn(3) == 0 {
			parts = append(parts, pick([]string{"project=", "project!=", "project=~"})+pick(projects))
		}
		if rng.Intn(4) == 0 {
			parts = append(parts, "-"+pick(terms))
		}
		for range rng.Intn(3) {
			parts = append(parts, pick(terms))
		}
		queries = append(queries, strings.Join(parts, " "))
	}
	requireResolveMatchesReference(t, g, queries)
}

// TestQueryPageMatchesReference holds the page Query builds to the reference's matches:
// its Matches and MatchCount are what MCP and the CLI show.
func TestQueryPageMatchesReference(t *testing.T) {
	g := largeGraph(t)
	withQueryCorpus(g)
	ref := newRefResolver(g)
	for _, in := range []string{"t003", "kind:target t003", "project=pkg/p00010 kind=file", "relation=references"} {
		want := ref.resolve(in, 0)
		out := g.QueryPage(in, 50, 0, 10)
		assert.Equalf(t, len(want), out.MatchCount, "MatchCount for %q", in)
		assert.Equalf(t, want[:min(10, len(want))], out.Matches, "first page for %q", in)
	}
}

func TestResolveLeafScoreLowerMatchesInteractive(t *testing.T) {
	paths := []string{
		"", "a", "target:pkg/p00001:t003", "Target:PKG/P00001:T003", "function:docs/f.buzz:x", "x/y/z/leaf", "leaf", "/", "//", "a/", "/a",
		"İstanbul/Straße", "ÀÉÎ/ÕÜ", "kKelvin/K", "bad\xffutf/\xc3(x", "file:web/ui/App.tsx", "t003", "tt003",
	}
	queries := []string{"", "t003", "T003", "x", "leaf", "L", "/", "straße", "ISTANBUL", "k", "é", "\xff", "ui/app", "tsx"}
	for _, p := range paths {
		for _, q := range queries {
			want := interactive.LeafScore(p, q)
			got := leafScoreLower(strings.ToLower(p), strings.ToLower(q), strings.Count(p, "/"))
			assert.Equalf(t, want, got, "LeafScore(%q, %q)", p, q)
		}
	}
}

// TestResolveGlobLoweringIsIdempotent pins the assumption the index rests on: the scan
// lowercases a glob's subject once, at build time, where globMatch lowercases it per call.
// The two agree only when lowering twice is lowering once.
func TestResolveGlobLoweringIsIdempotent(t *testing.T) {
	for _, s := range []string{"", "abc", "ABC", "İstanbul", "Straße", "ÀÉÎÕÜ", "kKelvin", "ǅ", "ᾈ", "bad\xffutf", "Σίσυφος ΑΣ"} {
		once := strings.ToLower(s)
		assert.Equalf(t, once, strings.ToLower(once), "ToLower is not idempotent for %q", s)
	}
}

func TestResolveProjectOwnerMatchesPrefixScan(t *testing.T) {
	sets := [][]string{
		{".", "a", "web", "web/ui", "pkg/x"},
		{".", "-", "b", "~"}, // "." sorts before "~" and "b", after "-": the one-character tie
		{"a", "a/b", "a/b/c"},
		{"", "x"},
		{"web/", "web"},
		{"."},
		{},
	}
	srcs := []string{
		"a", "a/b", "a/b/c", "a/b/c/d.go", "a/bc", "b", "~", "-", "web", "web/ui/x.go", "web/uix", "web/", "web//x", "/x", "/", "x", "x/y", "pkg/x/y", "pkg", "other/z", ".", "./x",
	}
	rng := rand.New(rand.NewSource(2))
	// Random set members drawn from the same alphabet widen the sample beyond the fixed sets.
	alphabet := []string{".", "a", "b", "-", "~", "a/b", "a/b/c", "web", "x/y", "", "ab"}
	for range 40 {
		var set []string
		for range 1 + rng.Intn(5) {
			set = append(set, alphabet[rng.Intn(len(alphabet))])
		}
		sets = append(sets, set)
	}
	for _, set := range sets {
		g := NewGraph()
		projects := map[string]struct{}{}
		for _, p := range set {
			g.AddNode(types.KnowledgeNode{ID: types.KindProject + ":" + p, Kind: types.KindProject})
			projects[p] = struct{}{}
		}
		ref := newRefResolver(g)
		for _, src := range srcs {
			wantP, wantOK := ref.projectOf(types.KnowledgeNode{ID: "file:" + src, Source: src}, "file:"+src)
			gotP, gotOK := owningProject(src, projects)
			assert.Equalf(t, wantOK, gotOK, "owned: projects %q, src %q", set, src)
			assert.Equalf(t, wantP, gotP, "owner: projects %q, src %q", set, src)
		}
	}
}

// TestResolveSearchIndexInvalidation: a write after a query must show up in the next one, and a
// write to a graph that shares its content must not change what the other still serves.
func TestResolveSearchIndexInvalidation(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "project:p", Kind: types.KindProject, Label: "p"})
	g.AddNode(types.KnowledgeNode{ID: "file:p/a.go", Kind: types.KindFile, Label: "a.go", Source: "p/a.go"})
	require.Len(t, g.Resolve("project=p kind=file", 0), 1)
	require.Empty(t, g.Resolve("zebra", 0))

	g.AddNode(types.KnowledgeNode{ID: "file:p/b.go", Kind: types.KindFile, Label: "b.go", Source: "p/b.go", Doc: "a zebra"})
	assert.Len(t, g.Resolve("project=p kind=file", 0), 2, "a node added after a query is found")
	assert.Len(t, g.Resolve("zebra", 0), 1, "its text is searchable")

	// A node that fills an existing one's empty doc changes its text without adding an ID.
	g.AddNode(types.KnowledgeNode{ID: "file:p/a.go", Kind: types.KindFile, Doc: "a quokka"})
	assert.Len(t, g.Resolve("quokka", 0), 1, "an upgraded node's new text is searchable")

	// A project added after the first project= query owns the files under it.
	g.AddNode(types.KnowledgeNode{ID: "project:p/sub", Kind: types.KindProject, Label: "sub"})
	g.AddNode(types.KnowledgeNode{ID: "file:p/sub/c.go", Kind: types.KindFile, Label: "c.go", Source: "p/sub/c.go"})
	assert.Len(t, g.Resolve("project=p/sub kind=file", 0), 1)
	assert.Len(t, g.Resolve("project=p kind=file", 0), 2, "the nested project's files leave the outer one")

	// share hands the cache a graph with the same index; a write on the original must not leak.
	cached := g.share()
	require.Len(t, cached.Resolve("kind=file", 0), 3)
	g.AddNode(types.KnowledgeNode{ID: "file:p/d.go", Kind: types.KindFile, Label: "d.go", Source: "p/d.go"})
	assert.Len(t, g.Resolve("kind=file", 0), 4)
	assert.Len(t, cached.Resolve("kind=file", 0), 3, "a write to the original leaves the cached graph's answer alone")

	// adopt gives another graph the cache's content and its index.
	adopter := NewGraph()
	adopter.adopt(cached)
	assert.Len(t, adopter.Resolve("kind=file", 0), 3)
	adopter.AddNode(types.KnowledgeNode{ID: "file:p/e.go", Kind: types.KindFile, Label: "e.go", Source: "p/e.go"})
	assert.Len(t, adopter.Resolve("kind=file", 0), 4)
	assert.Len(t, cached.Resolve("kind=file", 0), 3, "a write to an adopter leaves the cache's graph alone")
}

// TestResolveSearchIndexBuiltOnceUnderConcurrency: many first-queries on one graph must share
// one build. Run with -race, it also trips on an unguarded index.
func TestResolveSearchIndexBuiltOnceUnderConcurrency(t *testing.T) {
	g := buildConcurrencyFixture()
	const goroutines = 32
	var wg sync.WaitGroup
	got := make([]*searchIndex, goroutines)
	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			_ = g.Resolve(fmt.Sprintf("project=proj%d kind=file", i%10), 0)
			got[i] = g.searchIdx()
		}()
	}
	wg.Wait()
	for i := 1; i < goroutines; i++ {
		assert.Same(t, got[0], got[i], "every reader sees the one index")
	}
}
