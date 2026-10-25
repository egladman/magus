package job

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
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

// CallerClaims is one request per declaration holding a non-definition occurrence, in file
// then line order. pkg and dir are the defining file's package name and directory: a
// caller outside dir uses `<pkg>.<ident>`, so that is what its proof names. declarations
// places every line of a file, as RegionsBetween does against an empty file. An error
// names what the caller fixes: a stale symbol index, or an occurrence no declaration
// claim covers.
func CallerClaims(ident, pkg, dir string, files []types.SymbolOccurrenceFile, declarations func(file string) ([]types.RegionChange, error)) ([]types.JobClaimRequest, error) {
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
		return "", fmt.Errorf("no declaration holds the occurrence at %s:%d, so claim %s in write_paths instead", file, line, file)
	}
	for _, word := range declarationWords(decl) {
		if word == "" {
			continue
		}
		if n := len(slices.DeleteFunc(slices.Clone(all), func(d string) bool { return !types.NamesDeclaration(word, d) })); n == 1 {
			return word, nil
		}
	}
	return "", fmt.Errorf("the declaration holding %s:%d is not named alone by any claim of %s, so claim %s in write_paths instead", file, line, file, file)
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
