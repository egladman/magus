package job

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/job"
)

// The isolation levels a fork can run a job at, cheapest first.
const (
	// isolationShared runs the job in the forker's checkout.
	isolationShared = 1
	// isolationWave runs the job in one scratch checkout its wave shares, with one binary.
	isolationWave = 2
	// isolationOwnCheckout runs the job in its own checkout, a binary placed when no build
	// input differs (see [changedBuildInputs]).
	isolationOwnCheckout = 3
	// isolationOwnBuild is isolationOwnCheckout whose holder builds ./magus there.
	isolationOwnBuild = 4
)

// largeWriteSet is the p90 write set over the job store's fork rows: past it a job is
// likely to touch what a shared checkout's peers load.
const largeWriteSet = 27

// isolationJob is the job chooseIsolation places, with each fact the caller classified from
// the workspace: the write paths that are build inputs of ./cmd/magus, that cover a file the
// workspace loads, and that cover a generator's declared output; the overlaps with each live
// peer in the forker's checkout; and a depends_on whose head the checkpoint does not hold.
type isolationJob struct {
	Row                job.Job
	BinaryInputs       []string
	LoadFiles          []string
	GeneratorOutputs   []string
	Overlaps           []job.Overlap
	UnmergedDependency string
}

// chooseIsolation is the level job runs at, with the reason that decided it. Hard signals
// ignore the dials: a binary input is level 4; a generator output, or an overlap with a live
// peer without disjoint claims, is 3; a load file with a peer live in the checkout is at
// least 2. Otherwise a read-only job is 1 and a writer 3, lowered to 2 at riskTolerance 0.5,
// to 1 at 0.7, and to 2 at a weight in (0, 0.3]; over largeWriteSet paths or an unmerged
// dependency raise it back to 3. peers are the live jobs in the forker's checkout; weight 0
// is unweighted.
func chooseIsolation(candidate isolationJob, peers []job.Job, riskTolerance, weight float64) (level int, reason string) {
	row := candidate.Row
	switch {
	case len(candidate.BinaryInputs) > 0:
		return isolationOwnBuild, fmt.Sprintf("write path %s is a build input of ./cmd/magus, so a placed binary would judge the old tree", candidate.BinaryInputs[0])
	case len(candidate.GeneratorOutputs) > 0:
		return isolationOwnCheckout, fmt.Sprintf("write path %s is a generator's output, which a peer's run would overwrite", candidate.GeneratorOutputs[0])
	}
	if o, ok := claimlessOverlap(candidate.Overlaps); ok {
		return isolationOwnCheckout, fmt.Sprintf("its write paths overlap %s's (%s) with no disjoint claim", overlapPeer(o, row.ID), strings.Join(overlapPaths(o, row.ID), ", "))
	}

	level, reason = isolationOwnCheckout, "a writer commits on its own branch"
	switch {
	case row.ReadOnly:
		level, reason = isolationShared, "read-only"
	case riskTolerance >= 0.7:
		level, reason = isolationShared, fmt.Sprintf("a disjoint writer with no generator at risk_tolerance %.2g", riskTolerance)
	case riskTolerance >= 0.5:
		level, reason = isolationWave, fmt.Sprintf("a writer with no generator at risk_tolerance %.2g", riskTolerance)
	case weight > 0 && weight <= 0.3:
		level, reason = isolationWave, fmt.Sprintf("weight %.2g is too light for a checkout of its own", weight)
	}
	switch {
	case level >= isolationOwnCheckout:
	case candidate.UnmergedDependency != "":
		level, reason = isolationOwnCheckout, fmt.Sprintf("depends_on %s is not merged at its checkpoint, so it needs its own base", candidate.UnmergedDependency)
	case len(row.Paths.Write) > largeWriteSet:
		level, reason = isolationOwnCheckout, fmt.Sprintf("%d write paths, past the %d a shared checkout takes", len(row.Paths.Write), largeWriteSet)
	}
	if level < isolationWave && len(candidate.LoadFiles) > 0 && len(peers) > 0 {
		level, reason = isolationWave, fmt.Sprintf("write path %s covers a file the workspace loads, and %s is live in this checkout", candidate.LoadFiles[0], peers[0].ID)
	}
	return level, reason
}

// claimlessOverlap is the first overlap whose two sides did not claim disjoint declarations.
func claimlessOverlap(overlaps []job.Overlap) (job.Overlap, bool) {
	for _, o := range overlaps {
		if o.Claims != job.ClaimsDisjoint {
			return o, true
		}
	}
	return job.Overlap{}, false
}

func overlapPeer(o job.Overlap, id string) string {
	if o.JobA == id {
		return o.JobB
	}
	return o.JobA
}

func overlapPaths(o job.Overlap, id string) []string {
	if o.JobA == id {
		return o.PathsA
	}
	return o.PathsB
}
