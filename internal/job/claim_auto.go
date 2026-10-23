package job

import (
	"fmt"
	"maps"
	"path"
	"slices"

	"github.com/egladman/magus/internal/config"
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

// DecideAutoClaim decides whether magus claims rel for row without asking: a path one
// graph edge from what the job changed (a reference to a declaration its diff changed,
// or a test beside a file it changed), that is no generated output, magusfile or hub,
// that no other live job holds, while the job is under its claim cap. projects are what
// says which files targets generate; nil stops every claim. rows are every job.
//
// The cap is the row's own max_claims, which the fork held under jobs.max_claims.
func DecideAutoClaim(row types.Job, rel string, facts claimFacts, rows []types.Job, projects []*types.Project, fanIn fanInFunc) AutoClaim {
	rel = path.Clean(rel)
	req, proof, ok := ProposeClaim(row, rel, facts, nil)
	if !ok {
		if err := checkClaimPath(row, rel, nil); err != nil {
			return AutoClaim{Stop: err.Error()}
		}
		return AutoClaim{Stop: fmt.Sprintf("%s is more than one edge from what the job changed: it neither references a declaration the diff changed nor tests a file the job changed", rel)}
	}
	d := AutoClaim{Request: req, Proof: proof, Proved: true, Appliable: true}
	limit, err := claimCap(row, config.Jobs{})
	held := checkClaimPath(row, rel, rows)
	switch e, _ := types.ParseClaimEvidence(req.Because); {
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
		d.Stop = "no workspace answered for its targets' outputs, so " + rel + " may be generated"
	case generatedFile(projects, rel):
		d.Stop = rel + " is a generated output, which its target regenerates"
	case configName(rel):
		d.Stop = rel + " is a magusfile or workspace config, which every job's plan reads"
	default:
		d.Stop = hubStop(rel, facts.files[rel], fanIn)
	}
	return d
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
