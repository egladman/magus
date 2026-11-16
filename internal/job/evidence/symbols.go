package evidence

import (
	"cmp"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/clikit/exit"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// LeaseGraphEvidence resolves each write path against the knowledge graph, one line per
// path the graph knows. cold reports a graph that would not load at all.
//
// A path the graph cannot resolve is skipped SILENTLY, because most write paths are
// ordinary source directories the containment tree does not carry, and a "no node" line
// per path would bury the ones that do resolve. A cold graph is different and is
// reported once: "not asked" must not read as "nothing depends on this". It takes the domain
// graph through graph, so a graph that must not build answers cold while a stale symbol
// index may hold changes inside paths.
func LeaseGraphEvidence(ctx context.Context, graph *Graph, paths []string) (evidence []job.TermsEvidence, cold bool) {
	if len(paths) == 0 {
		return nil, false
	}
	g := graph.LoadDomain(ctx, paths)
	if g == nil {
		return nil, true
	}
	for _, p := range paths {
		if e, ok := pathEvidence(g, p); ok {
			evidence = append(evidence, e)
		}
	}
	return evidence, false
}

// pathEvidence answers for one declared path, trying the node ids a path can carry in the
// containment tree.
//
// It accepts only a node whose id IS the ref it asked for. Explain resolves a bare name
// fuzzily, which is right for a person typing `magus explain build` and wrong here: asked
// for "cmd/magus" it answered target:.:release-sign, and a brief that hands a worker the
// blast radius of an unrelated node is worse than one that stays quiet.
func pathEvidence(g *knowledge.Graph, declared string) (job.TermsEvidence, bool) {
	for _, ref := range []string{fmt.Sprintf("%s:%s", types.KindDir, declared), fmt.Sprintf("%s:%s", types.KindFile, declared), declared} {
		out, ok := g.Explain(ref)
		if !ok || out.Node.ID != ref {
			continue
		}
		return job.TermsEvidence{Path: declared, Node: out.Node.ID, BlastRadius: out.BlastRadius}, true
	}
	return job.TermsEvidence{}, false
}

// JobSymbolReader loads the knowledge graph ONCE and answers every symbol a job's gates
// name from it.
//
// Lazy and memoized because a verification reads several names and the graph is the
// expensive part; loading per name would reopen it once per symbol. A load failure is
// remembered too, so a verification does not retry a graph that is not there once per
// gate and report the same failure five times. Without rebuild, a stale symbol index
// answers no name at all: this reader has no write paths to clear it against (see Graph).
func JobSymbolReader(root string, rebuild bool, env Env) job.SymbolReader {
	return GraphSymbolReader(New(root, rebuild, env).Load)
}

// GraphSymbolReader answers each symbol a gate names from the graph load returns.
func GraphSymbolReader(load func(context.Context) *knowledge.Graph) job.SymbolReader {
	return func(ctx context.Context, name, signature string) (job.SymbolFact, bool) {
		g := load(ctx)
		if g == nil {
			return job.SymbolFact{}, false
		}
		return job.GraphSymbols(g)(ctx, name, signature)
	}
}

// CallersEnv is what expanding a callers request asks of the command line: the workspace
// read, the symbol lookups and the config the symbol graph is built under.
type CallersEnv struct {
	Config config.Config
	// OpenWorkspace opens the workspace at root for a read.
	OpenWorkspace func(ctx context.Context, root string) (types.WorkspaceRepository, error)
	// RefsGraph loads the symbol graph of ws under cfg, targeted to the symbol ref names.
	RefsGraph func(ctx context.Context, ws types.WorkspaceRepository, cfg config.Config, refresh bool, ref string) (*knowledge.Graph, error)
	// PickSymbol resolves ref to one symbol id, or lists the ids it is ambiguous between.
	PickSymbol func(g *knowledge.Graph, ref string) (symbol string, ambiguous []string)
	// RenameBlindSpots lists what keeps a symbol's references from being read whole.
	RenameBlindSpots func(id, language string, cov knowledge.Coverage) []types.EditRefusal
	// SymbolCoverage is how much of the workspace the symbol indexes cover; seeded says the
	// input is a symbol id.
	SymbolCoverage func(ctx context.Context, root, input string, seeded bool) knowledge.Coverage
}

// ExpandCallers is one claim request per declaration of the workspace at root holding an
// occurrence of the symbol name names, the [job.ClaimEnv] Callers lookup. It refuses with a
// usage error a name that resolves to no symbol or to several, a symbol whose occurrences
// the indexes cannot list whole, and a workspace with version control off.
func ExpandCallers(ctx context.Context, root, name string, env CallersEnv) ([]jobtypes.ClaimRequest, error) {
	ws, err := env.OpenWorkspace(ctx, root)
	if err != nil {
		return nil, err
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", types.VCSOptions{})
	if err != nil {
		return nil, fmt.Errorf("magus job apply: resolving the version control backend to place callers: %w", err)
	}
	if res.VCS == nil {
		return nil, exit.Usagef("magus job apply: version control is disabled here, so no caller of a symbol can be placed in a declaration")
	}
	declarations := func(file string) ([]jobtypes.RegionChange, error) {
		body, err := os.ReadFile(filepath.Join(ws.Root(), filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		return res.VCS.RegionsBetween(ctx, ws.Root(), file, nil, body)
	}
	g, err := env.RefsGraph(ctx, ws, env.Config, false, name)
	if err != nil {
		return nil, err
	}
	id, err := callersSymbol(g, name, env.PickSymbol)
	if err != nil {
		return nil, err
	}
	language := g.SymbolLanguage(id)
	if blind := env.RenameBlindSpots(id, language, env.SymbolCoverage(ctx, root, id, true)); len(blind) > 0 {
		return nil, callersRefusal(name, blind)
	}
	read, probed := workspacegraph.SymbolOccurrences(ctx, ws, ws.Root(), env.Config, slog.Default(), strings.TrimPrefix(id, types.KindSymbol+":"))
	if !probed {
		return nil, exit.Usagef("magus job apply: callers of %s: cannot read the symbol indexes", name)
	}
	var unreadable []types.EditRefusal
	for _, gap := range knowledge.NarrowGaps(read.Unreadable, language) {
		unreadable = append(unreadable, types.EditRefusal{Reason: fmt.Sprintf("project %s: symbol index %s; rebuild it with `%s`", gap.Project.Path, gap.Describe(), hint.GraphBuild)})
	}
	if len(unreadable) > 0 {
		return nil, callersRefusal(name, unreadable)
	}
	refs, _ := g.Refs(id)
	var defined string
	if len(refs.Defs) > 0 {
		defined = refs.Defs[0].File
	}
	ident := cmp.Or(append(slices.Clone(read.Names), refs.Label)...)
	expanded, err := job.CallerClaims(ident, goPackageName(ws.Root(), defined), path.Dir(defined), read.Files, declarations)
	if err != nil {
		return nil, exit.Usagef("magus job apply: %v", err)
	}
	return expanded, nil
}

// callersSymbol resolves name to the one symbol it names, refusing a name the index does
// not resolve or resolves to several.
func callersSymbol(g *knowledge.Graph, name string, pick func(*knowledge.Graph, string) (string, []string)) (string, error) {
	id, ambiguous := pick(g, name)
	if len(ambiguous) > 0 {
		return "", exit.Usagef("magus job apply: callers %q names %d symbols defined in this workspace, so name one by id: %s",
			name, len(ambiguous), strings.Join(ambiguous, ", "))
	}
	if _, ok := g.Refs(id); !ok {
		return "", exit.Usagef("magus job apply: callers %q names no symbol the index holds; check the name, or bring the index current with `%s`", name, hint.GraphBuild)
	}
	return id, nil
}

func callersRefusal(name string, refused []types.EditRefusal) error {
	reasons := make([]string, len(refused))
	for i, r := range refused {
		reasons[i] = r.Reason
	}
	return exit.Usagef("magus job apply: callers of %s cannot be listed whole: %s", name, strings.Join(reasons, "; "))
}

// goPackageName is the package clause of the Go file rel under root, "" when it is not Go
// or does not parse.
func goPackageName(root, rel string) string {
	if path.Ext(rel) != ".go" {
		return ""
	}
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(rel)), nil, parser.PackageClauseOnly)
	if err != nil {
		return ""
	}
	return f.Name.Name
}

// ReadSymbolDelta reads the delta of the diff from base to the tree at root over changed,
// against g, the graph of that same tree. false means it could not be read: no graph, no
// version control, no base, or regions the backend would not report.
//
// Added is each symbol a new-side line of a changed non-test Go file lands in whose name
// the base copy of that file does not hold. Left is each unreferenced symbol that is not a
// method, a field or test code, whose name some changed file held at base and none holds
// now. Both read names as identifiers in text, so a name that survives only in a comment
// still counts as held: they under-report rather than invent.
func ReadSymbolDelta(ctx context.Context, root string, g *knowledge.Graph, driver types.RevisionReader, base string, changed []string) (job.SymbolDelta, bool) {
	if g == nil || driver == nil || base == "" {
		return job.SymbolDelta{}, false
	}
	if len(changed) == 0 {
		return job.SymbolDelta{}, true
	}
	before, after := map[string]string{}, map[string]string{}
	for _, p := range changed {
		// A path missing on either side reads as empty: an added file held nothing at base,
		// and a deleted one holds nothing now.
		before[p], _ = driver.ReadFileAt(ctx, root, base, p) // an absent path reads as empty, as the comment above says
		if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			after[p] = string(b)
		}
	}

	var delta job.SymbolDelta
	var goFiles []jobtypes.FileChange
	for _, p := range changed {
		if _, present := after[p]; present && strings.HasSuffix(p, ".go") && !types.IsTestPath(p) {
			goFiles = append(goFiles, jobtypes.FileChange{Path: p})
		}
	}
	if len(goFiles) > 0 {
		// A graph that places no symbol in any changed Go file was not indexed over them,
		// and reading it would report every addition as none.
		if !slices.ContainsFunc(goFiles, func(f jobtypes.FileChange) bool { return g.SymbolAt(f.Path, math.MaxInt).ID != "" }) {
			return job.SymbolDelta{}, false
		}
		regions, err := driver.Regions(ctx, root, base, goFiles)
		if err != nil {
			return job.SymbolDelta{}, false
		}
		seen := map[string]bool{}
		for _, r := range regions {
			if r.Side != jobtypes.RegionNew {
				continue
			}
			for line := r.Lines[0]; line <= r.Lines[1]; line++ {
				span := g.SymbolAt(r.File.Path, line)
				if span.ID == "" || seen[span.ID] {
					continue
				}
				seen[span.ID] = true
				name, exported := goExport(span.ID)
				if exported && knowledge.IndexIdentifier(before[r.File.Path], span.Label) < 0 {
					delta.Added = append(delta.Added, name)
				}
			}
		}
	}

	held := func(texts map[string]string, label string) bool {
		for _, p := range changed {
			if knowledge.IndexIdentifier(texts[p], label) >= 0 {
				return true
			}
		}
		return false
	}
	for _, e := range g.Unreferenced() {
		if types.IsTestPath(e.Source) || len(descriptorNames(e.ID)) != 1 {
			continue
		}
		if held(before, e.Label) && !held(after, e.Label) {
			delta.Left = append(delta.Left, fmt.Sprintf("%s (%s)", e.Label, e.Source))
		}
	}
	slices.Sort(delta.Added)
	delta.Added = slices.Compact(delta.Added)
	slices.Sort(delta.Left)
	return delta, true
}

// goExport is a Go symbol's name as a goal spells it, `Type#Member` for a member, and
// whether code outside its package can name it: every step of the path exported.
func goExport(id string) (string, bool) {
	names := descriptorNames(id)
	if len(names) == 0 {
		return "", false
	}
	for _, n := range names {
		if r, _ := utf8.DecodeRuneInString(n); !unicode.IsUpper(r) {
			return "", false
		}
	}
	return strings.Join(names, "#"), true
}

// descriptorNames are the names along a symbol node id's path inside its package:
// `Goal#Signature.` is [Goal Signature], `Resolve().` is [Resolve], and a parameter adds its
// own name. The id is `symbol:<manager> <package> <descriptors>`, and the descriptors' own
// namespace ends at their last `/`.
func descriptorNames(id string) []string {
	parts := strings.SplitN(strings.TrimPrefix(id, types.KindSymbol+":"), " ", 3)
	path := parts[len(parts)-1]
	if i := strings.LastIndex(path, "/"); i >= 0 {
		path = path[i+1:]
	}
	return strings.FieldsFunc(strings.ReplaceAll(path, "`", ""), func(r rune) bool {
		return strings.ContainsRune("#.():[]!", r) || unicode.IsSpace(r)
	})
}
