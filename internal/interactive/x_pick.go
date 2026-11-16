package interactive

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/termkit/pick"
	"github.com/egladman/magus/types"
)

// targetChoices is the target set offered after the project picker. Mirrors
// manpage.CommonSubcommands minus "ls" (no-op for the shorthand UX).
var targetChoices = []string{"build", "test", "lint", "format", "clean", "generate", "ci"}

// GraphSearch resolves typed text to knowledge graph matches, at most limit of them.
type GraphSearch func(input string, limit int) []types.KnowledgeMatch

// GraphLoader loads the knowledge graph search the project picker runs. The caller
// owns how: this package cannot reach the workspace, and knowledge imports it for
// hints, so the graph arrives as its search alone.
type GraphLoader func(ctx context.Context) (GraphSearch, error)

// graphLookup returns a live search over the knowledge graph, resolving typed
// text to the PROJECT that owns whatever it names, or nil when no graph is
// available.
//
// This is what makes `x` graph-aware rather than a list filter: the graph knows
// files, symbols, docs, targets and spells, so typing "hasher" finds the project
// that defines it, not just the projects whose PATH happens to contain those
// letters. Everything the picker already does (the mouse, the inline drawing,
// the way out) is unchanged; only where the candidates come from moves.
//
// Degrades to nil rather than failing. A workspace whose graph has never been
// built, or a server that is not running, still gets the path filter it always
// had; a picker that refused to open because a search index was cold would be
// strictly worse than one that searches less.
func graphLookup(ctx context.Context, load GraphLoader, byPath map[string]*types.Project) func(string) []string {
	// Loaded in the BACKGROUND, because loading it takes most of a second and
	// the picker has to be on screen before then. Blocking the open on a search
	// index means typing `magus x` and watching nothing happen, which is worse
	// than searching less, and is the exact input lag this command exists to
	// avoid.
	//
	// Until it arrives, the caller's path filter answers. From the first
	// keystroke after it lands, the graph does. Nothing waits.
	var (
		mu    sync.Mutex
		g     GraphSearch
		ready bool
	)
	go func() {
		loaded, err := load(ctx)
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			g = loaded
		}
		ready = true
	}()

	return func(filter string) []string {
		mu.Lock()
		graph, done := g, ready
		mu.Unlock()
		if !done || graph == nil {
			return nil // not yet, or never: the path filter carries it
		}
		return resolveProjects(graph, filter, byPath)
	}
}

// resolveProjects turns one query into the projects that own its matches.
func resolveProjects(g GraphSearch, filter string, byPath map[string]*types.Project) []string {
	return func(filter string) []string {
		if strings.TrimSpace(filter) == "" {
			return nil // no query yet: the caller's own ordering stands
		}
		seen := map[string]bool{}
		var out []string
		for _, m := range g(filter, graphMatchLimit) {
			p, ok := projectForNode(m, byPath)
			if !ok || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
		return out
	}(filter)
}

// graphMatchLimit bounds one keystroke's search. The picker shows ten rows, and
// resolving far past what can be drawn spends time on results nobody sees.
const graphMatchLimit = 60

// projectForNode maps a graph match back to the project path the picker deals
// in, or reports that it belongs to none.
func projectForNode(m types.KnowledgeMatch, byPath map[string]*types.Project) (string, bool) {
	// The node id carries its owner as a prefix for every kind that has one
	// (target:<project>:<name>, file:<path>, ...), so the longest declared
	// project path that prefixes it is the owner.
	best := ""
	for path := range byPath {
		if path == "." {
			continue
		}
		if strings.Contains(m.ID, path) && len(path) > len(best) {
			best = path
		}
	}
	if best == "" {
		if _, ok := byPath["."]; ok && m.ID != "" {
			return ".", true
		}
		return "", false
	}
	return best, true
}

// pickProject filters all projects by the AND-substring rule, ranks the
// survivors, and either returns the unique top scorer or prompts the picker.
func PickProject(ctx context.Context, load GraphLoader, all []*types.Project, filters []string) (*types.Project, error) {
	scored := ScoreProjects(all, filters)
	if len(scored) == 0 {
		// No matches even before the picker: open the picker over the
		// full set so the user can adjust their filter without re-running.
		scored = ScoreProjects(all, nil)
	}

	if len(scored) == 1 || (len(scored) > 1 && scored[0].Score > scored[1].Score && len(filters) > 0) {
		// Unique-by-score: skip the picker. The "len(filters) > 0" guard
		// stops us from auto-picking a project when the user typed `x`
		// with no filters and just wants to browse.
		if len(scored) == 1 || len(filters) > 0 {
			return scored[0].P, nil
		}
	}

	items := make([]string, len(scored))
	for i, s := range scored {
		items[i] = s.P.Path
	}
	byPath := make(map[string]*types.Project, len(all))
	for _, p := range all {
		byPath[p.Path] = p
	}

	opts := pick.Options{Prompt: "project", InitialFilter: ""}
	// shown is the list the picker last drew. A live query REPLACES the items,
	// so the index it returns is into whatever the final keystroke produced,
	// not into `scored`. Recording it is how the label maps back to a project.
	shown := items
	if q := graphLookup(ctx, load, byPath); q != nil {
		opts.Query = func(_ context.Context, filter string) ([]string, error) {
			hits := q(filter)
			if len(hits) == 0 {
				// No graph match: fall back to the path filter, so typing
				// something the graph has never heard of still narrows the
				// list rather than emptying it.
				hits = filterPaths(items, filter)
			}
			shown = hits
			return hits, nil
		}
	}

	idx, err := pick.One(ctx, os.Stdin, os.Stderr, termkit.SystemProbe, items, opts)
	if err != nil {
		return nil, err
	}
	if opts.Query == nil {
		return scored[idx].P, nil
	}
	if idx < 0 || idx >= len(shown) {
		return nil, fmt.Errorf("picker returned %d for %d shown", idx, len(shown))
	}
	return byPath[shown[idx]], nil
}

// filterPaths is the path-substring narrowing the picker has always done, kept
// as the fallback for text the graph does not know.
func filterPaths(items []string, filter string) []string {
	f := strings.ToLower(strings.TrimSpace(filter))
	if f == "" {
		return items
	}
	var out []string
	for _, it := range items {
		if strings.Contains(strings.ToLower(it), f) {
			out = append(out, it)
		}
	}
	return out
}

// PickTarget shows the target list with last (if any) pre-highlighted.
func PickTarget(ctx context.Context, last string) (string, error) {
	initial := 0
	for i, v := range targetChoices {
		if v == last {
			initial = i
			break
		}
	}
	idx, err := pick.One(ctx, os.Stdin, os.Stderr, termkit.SystemProbe, targetChoices, pick.Options{
		Prompt:  "target",
		Initial: initial,
		MaxRows: len(targetChoices),
	})
	if err != nil {
		return "", err
	}
	return targetChoices[idx], nil
}

// IsInteractiveTTY reports whether stdin and stderr are both terminals.
func IsInteractiveTTY() bool {
	return termkit.StdinIsTerminal() && termkit.IsTerminalWriter(os.Stderr, termkit.SystemProbe)
}

// OutputRefShape matches the refs magus prints. Deliberately a shape test, not a
// lookup: it only decides which of x's two modes the argument selects, and the
// resolver below is what says whether the ref exists.
var OutputRefShape = regexp.MustCompile(`^out[0-9a-f]{8,}$`)
