package knowledge

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"weak"

	"github.com/egladman/magus/internal/textrank"
	"github.com/egladman/magus/types"
)

// Position is a cursor in a file, the seed a completion ranks from. Path is
// workspace-relative and slash-separated, Offset the cursor's byte offset in the file.
// Prefix and Suffix are the text before and after the masked middle: they are the only
// text that seeds the ranking, so nothing in the middle can come back as context.
type Position struct {
	Path   string
	Offset int
	Prefix string
	Suffix string
}

// errPositionCoverage is the refusal a position query gets when the symbol index cannot
// vouch for the position's file. An empty answer there would read as "nothing relevant".
var errPositionCoverage = errors.New("the symbol index is partial for the position's file")

// RefusePosition returns an error naming why cov cannot vouch for pos's file, nil when it
// can. It refuses when the probe did not run, when the symbol shards were not merged, when
// the graph holds no file node for the path, when a gap of the file's language belongs to
// a project that owns the path, and when such a project's index is stale for any source
// but the position's own file.
func (g *Graph) RefusePosition(pos Position, cov Coverage) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s, and `magus graph build` rebuilds it", errPositionCoverage, fmt.Sprintf(format, args...))
	}
	switch {
	case !cov.Probed:
		return fail("the declared-index probe did not run")
	case !cov.Seeded:
		return fail("the symbol shards were not loaded")
	}
	file, ok := g.node(fileID(pos.Path))
	if !ok {
		return fail("no file node for %s", pos.Path)
	}
	cov = cov.Narrow(file.Attrs[AttrLanguage])
	for _, gap := range cov.Gaps {
		if ownsPath(gap.Project.Path, pos.Path) {
			return fail("%s", gap.Describe())
		}
	}
	for _, s := range cov.Stale {
		if ownsPath(s.Project, pos.Path) && !onlyChanged(s, pos.Path) {
			return fail("%s is stale", s.Describe())
		}
	}
	return nil
}

// onlyChanged reports whether path is the one source s changed since it was built. The
// position's own file is masked from the ranking, so its being newer than the index
// cannot mislead it; any other change can.
func onlyChanged(s types.KnowledgeStaleIndex, path string) bool {
	return s.ChangedCount == 1 && len(s.Changed) == 1 && s.Changed[0] == path
}

// ownsPath reports whether the project at project holds path.
func ownsPath(project, path string) bool {
	return project == "." || path == project || strings.HasPrefix(path, project+"/")
}

// exclusion is the set of workspace paths a ranking drops before it ranks: a path drops
// itself and everything under it.
type exclusion map[string]bool

func newExclusion(paths []string, pos *Position) exclusion {
	out := exclusion{}
	for _, p := range paths {
		if p = strings.Trim(filepath.ToSlash(p), "/"); p != "" {
			out[p] = true
		}
	}
	// The position's own file is on disk whole, middle included: its edges and its
	// symbols' ranges would carry the middle back.
	if pos != nil {
		out[pos.Path] = true
	}
	return out
}

// drops reports whether n lives at an excluded path: a file or dir node by its id, any
// other node by the path its Source names.
func (x exclusion) drops(n *types.KnowledgeNode) bool {
	if len(x) == 0 {
		return false
	}
	p, ok := strings.CutPrefix(n.ID, types.KindFile+":")
	if !ok {
		p, ok = strings.CutPrefix(n.ID, types.KindDir+":")
	}
	if !ok {
		p = n.Source
		if path, _, ok := splitPathLine(p); ok {
			p = path
		}
	}
	for p != "" {
		if x[p] {
			return true
		}
		i := strings.LastIndexByte(p, '/')
		if i < 0 {
			return false
		}
		p = p[:i]
	}
	return false
}

// dropsID is drops for a node id; an id that names no node drops nothing.
func (g *Graph) dropsID(x exclusion, id string) bool {
	if len(x) == 0 {
		return false
	}
	n, ok := g.node(id)
	return ok && x.drops(&n)
}

// windowIdentifiers weighs each identifier in pos's prefix and suffix by its nearness to
// the cursor: the k-th occurrence out from the cursor on either side adds 1/(1+k). The
// scan is identifiers', which knows no language.
func windowIdentifiers(pos Position) map[string]float64 {
	weights := map[string]float64{}
	before := identifiers(pos.Prefix)
	for k := range before {
		weights[before[len(before)-1-k]] += 1 / float64(1+k)
	}
	for k, id := range identifiers(pos.Suffix) {
		weights[id] += 1 / float64(1+k)
	}
	return weights
}

// rankPosition ranks the defined symbols the window's identifiers name. A symbol's exact
// score is its label's window weight split evenly over every admitted symbol of that label,
// so a name defined in forty packages seeds each one weakly. When a reader path names a
// node, a personalized PageRank seeded by it and the exact hits, walking no excluded node,
// reranks them, and the two lists fuse by weighted reciprocal rank.
func (g *Graph) rankPosition(ix *searchIndex, cq *compiledQuery, opts RetrieveOptions, x exclusion, ranking *types.KnowledgeRanking) ([]types.KnowledgeMatch, error) {
	weights := windowIdentifiers(*opts.Position)
	cands := g.positionCandidates(ix, cq, opts, x, weights)
	if len(cands) == 0 {
		return nil, nil
	}
	exact := textrank.RankedList{Name: listExact, Weight: listWeight(opts.Weights, listExact), IDs: make([]string, len(cands))}
	byID := make(map[string]*candidate, len(cands))
	for i := range cands {
		exact.IDs[i] = cands[i].hit.ID
		byID[cands[i].hit.ID] = &cands[i]
	}
	lists := []textrank.RankedList{exact}
	seedsOf := map[string][]string{}
	// As in rankText, the walk runs only when a reader path seeds it.
	if seeds := g.readerSeeds(opts.Reader, x); len(seeds) > 0 {
		graph, err := g.graphList(cands, opts, seeds, seedsOf, x)
		if err != nil {
			return nil, err
		}
		lists = append(lists, *graph)
	} else {
		ranking.Skipped = append(ranking.Skipped, listGraph+": no reader path names a node")
	}
	fused, err := textrank.Fuse(lists, opts.Weights.Fusion.K)
	if err != nil {
		return nil, err
	}
	matches := make([]types.KnowledgeMatch, 0, len(fused))
	for _, f := range fused {
		c := byID[f.ID]
		w := &types.KnowledgeMatchWhy{Prior: c.prior, Seeds: seedsOf[f.ID]}
		for _, name := range []string{listExact, listGraph} {
			if rank, ok := f.Ranks[name]; ok {
				w.Lists = append(w.Lists, types.KnowledgeListRank{List: name, Rank: rank})
			}
		}
		matches = append(matches, newMatch(&ix.entries[c.pos].node, f.Score, w))
	}
	ranking.Note = fmt.Sprintf("position %s:%d seeded %d identifier(s) naming %d defined symbol(s)", opts.Position.Path, opts.Position.Offset, len(weights), len(cands))
	return matches, nil
}

// positionCandidates is the admitted defined symbols weights' labels name, by exact score,
// then kindRank, then id.
func (g *Graph) positionCandidates(ix *searchIndex, cq *compiledQuery, opts RetrieveOptions, x exclusion, weights map[string]float64) []candidate {
	g.ensureAdj()
	defined := func(id string) bool {
		return slices.ContainsFunc(g.in[id], func(e types.KnowledgeEdge) bool { return e.Relation == types.RelationDefines })
	}
	byLabel := symbolsByLabel(ix)
	var admitted []int
	perLabel := map[string]int{}
	for _, label := range slices.Sorted(maps.Keys(weights)) {
		for _, i := range byLabel[label] {
			n := &ix.entries[i].node
			if x.drops(n) || !g.admits(ix, int(i), cq) || !defined(n.ID) {
				continue
			}
			admitted = append(admitted, int(i))
			perLabel[label]++
		}
	}
	if len(admitted) == 0 {
		return nil
	}
	cands := make([]candidate, 0, len(admitted))
	for _, i := range admitted {
		n := &ix.entries[i].node
		prior := g.prior(n, opts.Weights)
		score := weights[n.Label] / float64(perLabel[n.Label]) * prior
		cands = append(cands, candidate{pos: i, hit: textrank.Hit{ID: n.ID, Score: score}, prior: prior, score: score})
	}
	slices.SortFunc(cands, func(a, b candidate) int {
		if a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}
		ka, kb := kindRank(ix.entries[a.pos].node.Kind), kindRank(ix.entries[b.pos].node.Kind)
		return cmp.Or(cmp.Compare(kb, ka), cmp.Compare(a.hit.ID, b.hit.ID))
	})
	return cands
}

// labelIndex is the symbol entries of one search index by label.
type labelIndex struct {
	ix      weak.Pointer[searchIndex]
	byLabel map[string][]int32
}

// lastLabelIndex holds the label index of the search index the last position query ranked
// over. A position query runs per keystroke and a label scan of every symbol costs more
// than the rest of the ranking, so the index is built once per search index. The pointer is
// weak, so a replaced graph's index is collected, and the index is read-only once
// published: two queries that race to build it build the same map.
var lastLabelIndex atomic.Pointer[labelIndex]

func symbolsByLabel(ix *searchIndex) map[string][]int32 {
	if last := lastLabelIndex.Load(); last != nil && last.ix.Value() == ix {
		return last.byLabel
	}
	byLabel := map[string][]int32{}
	for _, i := range ix.byKind[types.KindSymbol] {
		label := ix.entries[i].node.Label
		byLabel[label] = append(byLabel[label], i)
	}
	lastLabelIndex.Store(&labelIndex{ix: weak.Make(ix), byLabel: byLabel})
	return byLabel
}

// ContextSite is one verified site of a position query's context and the match it is the
// source of.
type ContextSite struct {
	MatchID, Symbol string
	types.KnowledgeDefinitionSite
}

// QueryPosition is QueryRanked under opts.Position with the page's context filled: the
// source PositionText keeps for the page's matches, at most budget of them, verified
// against fresh. Out.Context holds the sites and the returned slice pairs each with its
// match. Ranking.Note counts the sites that did not verify. It errors when opts carries no
// position, and as QueryRanked does.
func (g *Graph) QueryPosition(input string, budget, offset, limit int, opts RetrieveOptions, fresh func(file string) bool) (types.KnowledgeQueryOutput, []ContextSite, error) {
	if opts.Position == nil {
		return types.KnowledgeQueryOutput{}, nil, errors.New("a position query needs a position")
	}
	out, err := g.QueryRanked(input, budget, offset, limit, opts)
	if err != nil {
		return out, nil, err
	}
	sites, dropped := g.PositionText(out.Matches, out.Budget, fresh)
	for _, site := range sites {
		out.Context = append(out.Context, site.KnowledgeDefinitionSite)
	}
	if dropped > 0 {
		out.Ranking.Note += fmt.Sprintf("; %d site(s) did not verify against the tree and were left out of the context", dropped)
	}
	return out, sites, nil
}

// PositionText is the source of the first budget matches that name a symbol, in match
// order: each one's doc comment and declaration line, read from its defining file under the
// graph's root. A site is kept only when it verifies: its file is one fresh says the index
// reflects, and the symbol's name is still on the recorded line. Every other site is left
// out and counted in dropped, so a short answer says why it is short. A budget of 0 or
// less takes DefaultBudget.
func (g *Graph) PositionText(matches []types.KnowledgeMatch, budget int, fresh func(file string) bool) (sites []ContextSite, dropped int) {
	if budget <= 0 {
		budget = DefaultBudget
	}
	files := map[string][][]byte{}
	read := func(file string) [][]byte {
		lines, seen := files[file]
		if !seen {
			if data, err := os.ReadFile(filepath.Join(g.root, filepath.FromSlash(file))); err == nil {
				lines = types.SplitSourceLines(data)
			}
			files[file] = lines
		}
		return lines
	}
	for _, m := range matches {
		if len(sites) == budget {
			break
		}
		n, ok := g.node(m.ID)
		if !ok || n.Kind != types.KindSymbol {
			continue
		}
		file, line, ok := splitPathLine(n.Source)
		lines := read(file)
		if !ok || line < 1 || fresh == nil || !fresh(file) || line > len(lines) || !containsWord(lines[line-1], n.Label) {
			dropped++
			continue
		}
		site := types.KnowledgeDefinitionSite{File: file, StartLine: line, Status: types.DefinitionVerified}
		if end, err := strconv.Atoi(n.Attrs[attrDefEndLine]); err == nil && end >= line {
			site.EndLine = end
		}
		block := SourceWindow{Show: true}.Text(lines, docStart(lines, line-1), line)
		site.Source, site.SourceStartLine = block.Text, block.StartLine
		sites = append(sites, ContextSite{MatchID: m.ID, Symbol: n.Label, KnowledgeDefinitionSite: site})
	}
	return sites, dropped
}
