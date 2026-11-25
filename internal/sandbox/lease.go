package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	scissors "github.com/egladman/magus/libs/safetyscissors/sandbox"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// NarrowToLease reduces policy's filesystem WRITE grant to the boundary the lease leaseID
// names declared in the job store at loc. It returns policy untouched when there is no
// boundary to derive one from: no lease id, no row, a row that is not live, a ROOT lease
// (a row with no parent is the orchestrator, and it owns the whole checkout), or a row
// that declared no write paths and is not read-only. A read-only row narrows the grant
// to nothing but the cache dir and the policy's private temp dir, the same answer the
// guard gives its writes.
//
// The grant is DERIVED from the row rather than declared a second time in magus.yaml,
// because a boundary written twice is a boundary that disagrees with itself. The agent
// guard already grades a write against the row's write paths, so a separate sandbox declaration
// would let the kernel refuse something other than what the guard explains, and one of
// the two would be teaching a rule nothing enforces.
//
// Reads are left exactly as the workspace policy granted them: the job store declares a
// write boundary only, and a worker has to read the tree it is changing.
//
// Only write grants on the checkout, or on a directory holding it, are dropped. Grants
// outside it (/dev/null, tool caches, the git directories of a linked worktree) stay,
// and the workspace cache directory and the private temp dir are granted back, which
// every target run needs to produce output at all.
//
// A deny path INSIDE a write one costs the directory holding it, not the leased tree:
// this ruleset and landlock are both allowlists with no deny rule, so an enclosing grant
// is replaced by grants on its children (see scissors.Boundary.Narrow).
//
// An unreadable job store is an error and no policy.
// A claimed lease whose boundary cannot be read never gets
// the workspace write grant.
//
// from is the source that answered leaseID; the narrowed policy carries it so a denial
// can say whether the boundary was bound or only claimed.
//
// products are workspace-relative product paths. A writable lease may write
// them beside its write paths, as run.go grants them to the step.
func NarrowToLease(ctx context.Context, policy *Policy, loc job.Location, leaseID string, from jobtypes.LeaseSource, products ...string) (*Policy, error) {
	if policy == nil || loc.Root == "" || leaseID == "" {
		return policy, nil
	}
	rows, err := job.NewStore(loc).List(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", types.FormatDiagnostic(types.AllowlistUnresolved,
			fmt.Sprintf("lease %s: job store unreadable, so its write boundary is unknown", leaseID)), err)
	}
	i := slices.IndexFunc(rows, func(r jobtypes.Job) bool { return r.ID == leaseID })
	if i < 0 {
		return policy, nil
	}
	row := rows[i]
	// Products join only write paths already declared: a row that declares none states
	// no boundary, and products must not turn it into one.
	if len(row.Paths.Write) > 0 {
		row.Paths.Write = slices.Concat(row.Paths.Write, products)
	}
	b, ok := scissors.LeaseBoundary(row, from, loc.Root, loc.CacheDir, policy.TempDir)
	if !ok {
		return policy, nil
	}
	slog.With(attr.Component("magus")).InfoContext(ctx, "narrowed the sandbox write grant to a lease boundary",
		"lease", row.ID, "parent", row.Parent, "paths.write", len(rows[i].Paths.Write), "write_rules", b.Granted())
	return narrow(policy, b), nil
}

// narrow returns policy with its write grants on the checkout replaced by b's (see
// scissors.Boundary.Narrow), b kept so each Scoped policy is narrowed the same way
// without reading the store again.
func narrow(policy *Policy, b scissors.Boundary) *Policy {
	narrowed := *policy
	narrowed.FS = filesystem.Ruleset{Rules: b.Narrow(policy.FS.Rules)}
	narrowed.Lease, narrowed.LeaseFrom = b.Lease()
	narrowed.lease = &b
	// The unnarrowed policy's memo holds unnarrowed policies.
	narrowed.scoped = &scopedPolicies{policies: map[string]*Policy{}}
	return narrowed.withConfinement()
}
