package job

import (
	"cmp"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// hubFanIn is the fan-in at which a file is a hub, which the guard never claims on its
// own. Fan-in is how many other files reference a top-level name the file declares,
// counting only names one indexed symbol holds: the index cannot attribute a shared
// name's sites. Measured 2026-10-23 at eaec17348: of 1557 Go files with any fan-in, the
// median is 3, the 90th percentile 14 and the 95th 25, so the hubs are the 78 most-used
// files: types/, guard.go, internal/trail, libs/testkit and the Buzz VM's value.go.
const hubFanIn = 25

// fanInFunc counts the files other than file that reference one of names, the top-level
// names file declares, or says why it cannot.
type fanInFunc func(file string, names []string) (int, error)

// AutoClaim is what magus decides about a write outside a job's write paths that the
// guard would otherwise refuse. Build it with [DecideAutoClaim].
type AutoClaim struct {
	// Request and Proof are the first proof of the path, set when Proved.
	Request types.JobClaimRequest
	Proof   string
	Proved  bool
	// Stop names the condition that keeps magus from claiming the path itself, "" when
	// it claims it.
	Stop string
	// Appliable says the proof is one `magus job apply` would record now, so the refusal
	// may serve its line.
	Appliable bool
}

// DecideAutoClaim decides whether magus claims rel for row without asking. It claims a
// path one graph edge from what the job changed: a reference to a declaration its diff
// changed, a call to a function whose signature it changed, the declaration of a name a
// changed file uses, or a test beside a file the job holds. It also claims the write path
// covering rel of a live job whose holder the facts hold dead, whole. It never claims a
// generated output, magusfile, hub or path another live job holds, nor past the row's
// max_claims, which the fork held under jobs.max_claims. projects say which files targets
// generate; nil stops every claim. rows are every job.
func DecideAutoClaim(row types.Job, rel string, facts claimFacts, rows []types.Job, projects []*types.Project, fanIn fanInFunc) AutoClaim {
	rel = path.Clean(rel)
	req, proof, ok := ProposeClaim(row, rel, facts, nil)
	if !ok {
		if err := checkClaimPath(row, rel, nil); err != nil {
			return AutoClaim{Stop: err.Error()}
		}
		return AutoClaim{Stop: fmt.Sprintf("%s is more than one edge from what the job changed: no refs, calls, defines or test proof holds for it", rel)}
	}
	d := AutoClaim{Request: req, Proof: proof, Proved: true, Appliable: true}
	limit, err := claimCap(row, config.Jobs{})
	e, _ := types.ParseClaimEvidence(req.Because)
	held := checkClaimPath(row, rel, rows)
	if e.Kind == types.ClaimDeadHolder {
		held = checkClaimPathPast(row, rel, rows, e.Subject)
	}
	switch {
	case err != nil:
		d.Stop, d.Appliable = err.Error(), false
	case held != nil:
		d.Stop, d.Appliable = held.Error(), false
	case len(row.Claims) >= limit.Max:
		d.Stop, d.Appliable = fmt.Sprintf("%s holds %d proved claim(s), its max_claims (%s)", row.ID, len(row.Claims), limit.Source), false
	case e.Kind == types.ClaimOutput:
		d.Stop = rel + " is a generated output, which its target regenerates"
	case e.Kind == types.ClaimReleased:
		d.Stop = "a path the holder released is no graph edge from what the job changed"
	case projects == nil:
		d.Stop = fmt.Sprintf("no workspace answered for its targets' outputs, so %s may be generated", rel)
	case generatedFile(projects, rel):
		d.Stop = rel + " is a generated output, which its target regenerates"
	case configName(rel):
		d.Stop = rel + " is a magusfile or workspace config, which every job's plan reads"
	case e.Kind == types.ClaimDeadHolder:
		// A dead holder's path was already handed out as work, so its fan-in decides nothing.
	default:
		d.Stop = hubStop(rel, facts.files[rel], fanIn)
	}
	return d
}

// DeadHolder is a live job whose holder the guard judged dead, as it read the row then.
type DeadHolder struct {
	Row types.Job
	// Since is the unix second the holder died: its session's end, or its last judged call.
	Since int64
	// Why says how magus knows: the session ended, or how long it went silent.
	Why string
}

// WithDeadHolders is f with each of dead's write paths open to a dead proof.
func (f claimFacts) WithDeadHolders(dead []DeadHolder) claimFacts {
	f.dead = make(map[string]DeadHolder, len(dead))
	for _, d := range dead {
		f.dead[d.Row.ID] = d
	}
	return f
}

// SessionEnded reports when the session holding job id ended: every binding record naming
// id is a tombstone, and there is one. It answers with the newest tombstone's time. A
// session that bound id again, or a record never written, says no session ended.
func (s *Store) SessionEnded(id string) (int64, bool) {
	if s.err != nil || s.path == "" {
		return 0, false
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(s.path), agentRecordDir))
	if err != nil {
		return 0, false
	}
	var since int64
	for _, e := range entries {
		b := readBinding(filepath.Join(filepath.Dir(s.path), agentRecordDir, e.Name()))
		switch {
		case b.Job != id:
		case !b.Gone:
			return 0, false
		default:
			since = max(since, b.swept)
		}
	}
	return since, since != 0
}

func hubStop(rel string, src sourceFacts, fanIn fanInFunc) string {
	if fanIn == nil {
		return "nothing measured the fan-in of " + rel
	}
	n, err := fanIn(rel, slices.Sorted(maps.Keys(src.decls)))
	switch {
	case err != nil:
		return err.Error()
	case n >= hubFanIn:
		return fmt.Sprintf("%s is a hub: %d files use what it declares, at or past %d", rel, n, hubFanIn)
	}
	return ""
}

// callerClaims is one request per declaration holding a non-definition occurrence, in file
// then line order. pkg and dir are the defining file's package name and directory: a
// caller outside dir uses `<pkg>.<ident>`, so that is what its proof names. declarations
// places every line of a file, as RegionsBetween does against an empty file. An error
// names what the caller fixes: a stale symbol index, or an occurrence no declaration
// claim covers.
func callerClaims(ident, pkg, dir string, files []types.SymbolOccurrenceFile, declarations func(file string) ([]types.RegionChange, error)) ([]types.JobClaimRequest, error) {
	var out []types.JobClaimRequest
	for _, f := range slices.SortedFunc(slices.Values(files), func(a, b types.SymbolOccurrenceFile) int { return cmp.Compare(a.File, b.File) }) {
		if f.Stale {
			return nil, staleCallers(f.File)
		}
		var regions []types.RegionChange
		for _, occ := range f.Occurrences {
			if occ.Definition {
				continue
			}
			if occ.Status != types.SymbolOccurrenceVerified {
				return nil, staleCallers(fmt.Sprintf("%s:%d", f.File, occ.Line))
			}
			if regions == nil {
				placed, err := declarations(f.File)
				if err != nil {
					return nil, fmt.Errorf("placing the declarations of %s: %w", f.File, err)
				}
				regions = placed
			}
			word, err := enclosingDeclaration(f.File, occ.Line, regions)
			if err != nil {
				return nil, err
			}
			because := "refs:" + ident
			if path.Dir(f.File) != dir && pkg != "" {
				because = fmt.Sprintf("refs:%s.%s", pkg, ident)
			}
			req := types.JobClaimRequest{Path: fmt.Sprintf("%s#%s", strings.ReplaceAll(f.File, "#", `\#`), word), Because: because}
			if !slices.Contains(out, req) {
				out = append(out, req)
			}
		}
	}
	return out, nil
}

func staleCallers(where string) error {
	return fmt.Errorf("the symbol index is behind the tree at %s: rebuild it with `%s` to place its callers", where, hint.GraphBuild)
}

// enclosingDeclaration is the claim word for the declaration holding line of file: the
// shortest of the declared name, a method's `Recv) name` and the whole declaration line
// that names that declaration alone among the file's.
func enclosingDeclaration(file string, line int, regions []types.RegionChange) (string, error) {
	var decl string
	var all []string
	for _, r := range regions {
		loc := r.Location()
		if loc.Declaration != "" && loc.Declaration != types.Preamble && !slices.Contains(all, loc.Declaration) {
			all = append(all, loc.Declaration)
		}
		if r.Side == types.RegionNew && r.Lines[0] <= line && line <= r.Lines[1] {
			decl = loc.Declaration
		}
	}
	if decl == "" || decl == types.Preamble {
		return "", fmt.Errorf("no declaration holds the occurrence at %s:%d, so claim %s in paths.write instead", file, line, file)
	}
	for _, word := range declarationWords(decl) {
		if word == "" {
			continue
		}
		if n := len(slices.DeleteFunc(slices.Clone(all), func(d string) bool { return !types.NamesDeclaration(word, d) })); n == 1 {
			return word, nil
		}
	}
	return "", fmt.Errorf("the declaration holding %s:%d is not named alone by any claim of %s, so claim %s in paths.write instead", file, line, file, file)
}

var methodLine = regexp.MustCompile(`^func\s*\([^)]*?([A-Za-z_][A-Za-z0-9_]*)(?:\[[^\]]*\])?\)\s*([A-Za-z_][A-Za-z0-9_]*)`)

func declarationWords(decl string) []string {
	decl = strings.TrimSpace(decl)
	var words []string
	if m := declaredNamePattern.FindStringSubmatch(decl); m != nil {
		words = append(words, m[1])
	}
	if m := methodLine.FindStringSubmatch(decl); m != nil {
		words = append(words, fmt.Sprintf("%s) %s", m[1], m[2]))
	}
	return append(words, decl)
}

// ClaimEnv is what applying a claim record asks of the command line: the workspace
// reads, the symbol lookups and the config the symbol graph is built under.
type ClaimEnv struct {
	Config config.Config
	// OpenWorkspace opens the workspace at root for a read.
	OpenWorkspace func(ctx context.Context, root string) (types.WorkspaceRepository, error)
	// Workspace is the workspace at root, for the projects a claim is proved on.
	Workspace func(ctx context.Context, root string) Workspace
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

// ApplyClaim applies the one claim record records holds: each path proved against the
// job's diff before the store's lock and again under it. It returns the stored row and how
// many claims it added. open is called once the record is valid, so a refused record opens
// no store.
func ApplyClaim(ctx context.Context, root string, records []types.Declaration, open func() (*Store, error), dryRun bool, env ClaimEnv) (types.Job, int, error) {
	if len(records) != 1 {
		return types.Job{}, 0, exit.Usagef("magus job apply: a claim record applies alone, and this input holds %d records", len(records))
	}
	rec := records[0]
	if err := rec.Validate(); err != nil {
		return types.Job{}, 0, exit.Usagef("magus job apply: %s", err)
	}
	store, err := open()
	if err != nil {
		return types.Job{}, 0, err
	}
	rows, err := store.List()
	if err != nil {
		return types.Job{}, 0, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == rec.ID })
	if i < 0 {
		return types.Job{}, 0, exit.Usagef("magus job apply: nothing declares job %s, so nothing claims", rec.ID)
	}
	reqs, err := expandCallerClaims(ctx, root, rows[i], rec.Claim, env)
	if err != nil {
		return types.Job{}, 0, err
	}
	if len(reqs) == 0 {
		return types.Job{}, 0, exit.Usagef("magus job apply: %s already holds every declaration the callers name, so nothing claims", rec.ID)
	}
	asked := make([]string, len(reqs))
	for k, w := range reqs {
		asked[k], _ = types.SplitClaim(w.Path)
	}
	facts := ObserveClaim(ctx, root, rows[i], asked, env.Workspace(ctx, root).Projects)
	stored, err := store.Claim(ctx, rec.ID, reqs, facts, env.Config.Jobs, dryRun)
	return stored, len(reqs), err
}

// expandCallerClaims replaces each callers request with one request per declaration
// holding an occurrence of the symbol, leaving out one row already holds, which the store
// would refuse. It lives here so the store never reads the symbol index; the store
// still proves every request it returns.
func expandCallerClaims(ctx context.Context, root string, row types.Job, reqs []types.JobClaimRequest, env ClaimEnv) ([]types.JobClaimRequest, error) {
	if !slices.ContainsFunc(reqs, func(r types.JobClaimRequest) bool { return r.Callers != "" }) {
		return reqs, nil
	}
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
	declarations := func(file string) ([]types.RegionChange, error) {
		body, err := os.ReadFile(filepath.Join(ws.Root(), filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		return res.VCS.RegionsBetween(ctx, ws.Root(), file, nil, body)
	}
	var out []types.JobClaimRequest
	for _, req := range reqs {
		if req.Callers == "" {
			out = append(out, req)
			continue
		}
		name := strings.TrimSpace(req.Callers)
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
		expanded, err := callerClaims(ident, goPackageName(ws.Root(), defined), path.Dir(defined), read.Files, declarations)
		if err != nil {
			return nil, exit.Usagef("magus job apply: %v", err)
		}
		for _, e := range expanded {
			if !heldClaim(row, e.Path) {
				out = append(out, e)
			}
		}
	}
	return out, nil
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

// heldClaim says row already holds claim: by the claim itself, or by a write path covering
// its whole file.
func heldClaim(row types.Job, claim string) bool {
	file, _ := types.SplitClaim(claim)
	return slices.ContainsFunc(row.Paths.Write, func(w string) bool {
		_, word := types.SplitClaim(w)
		return w == claim || word == "" && Covers(w, file)
	})
}
