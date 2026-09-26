package sandbox

import (
	"context"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sandbox/filesystem"
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
// is replaced by grants on its children (see splitAroundDenied).
//
// An unreadable job store fails OPEN with a warning, matching the guard: a lease id that
// stops resolving must not brick the checkout a person is working in.
//
// from is the source that answered leaseID; the narrowed policy carries it so a denial
// can say whether the boundary was bound or only claimed.
func NarrowToLease(ctx context.Context, policy *Policy, loc job.Location, leaseID string, from types.LeaseSource) *Policy {
	if policy == nil || loc.Root == "" || leaseID == "" {
		return policy
	}
	rows, err := job.NewStore(loc).List()
	if err != nil {
		slog.WarnContext(ctx, types.FormatDiagnostic(types.AllowlistUnresolved,
			"job store unreadable; sandbox running with the workspace write grant"),
			"lease", leaseID, "err", err.Error())
		return policy
	}
	row, ok := workerLease(rows, leaseID)
	if !ok {
		return policy
	}

	b := &leaseBoundary{id: row.ID, from: from, root: filesystem.ResolveRulePath(loc.Root), cacheDir: loc.CacheDir}
	if !row.ReadOnly {
		b.granted = grantedPaths(loc.Root, row.WritePaths, row.DenyPaths)
	}
	slog.InfoContext(ctx, "magus: narrowed the sandbox write grant to a lease boundary",
		"lease", row.ID, "parent", row.Parent, "write_paths", len(row.WritePaths), "write_rules", len(b.granted))
	return b.apply(policy)
}

// leaseBoundary is a lease's write boundary as NarrowToLease derived it from the job
// store, kept on the narrowed policy so a ForSpells policy is narrowed the same way
// without reading the store again.
type leaseBoundary struct {
	id       string
	from     types.LeaseSource
	root     string   // the checkout, resolved
	cacheDir string   // the workspace cache, granted back
	granted  []string // the lease's write grants, resolved
}

// apply returns policy with its write grants on the checkout replaced by b's.
func (b *leaseBoundary) apply(policy *Policy) *Policy {
	rules := make([]filesystem.Rule, 0, len(policy.FS.Rules)+len(b.granted)+2)
	for _, r := range policy.FS.Rules {
		if filesystem.Under(r.Path, b.root) || filesystem.Under(b.root, r.Path) {
			r.Write = false
		}
		rules = append(rules, r)
	}
	for _, p := range b.granted {
		rules = append(rules, filesystem.Rule{Path: p, Read: true, Write: true})
	}
	for _, p := range []string{b.cacheDir, policy.TempDir} {
		if p == "" {
			continue
		}
		rules = append(rules, filesystem.Rule{Path: filesystem.ResolveRulePath(p), Read: true, Write: true})
	}

	narrowed := *policy
	narrowed.FS = filesystem.Ruleset{Rules: rules}
	narrowed.Lease = b.id
	narrowed.LeaseFrom = b.from
	narrowed.lease = b
	// The unnarrowed policy's memo holds unnarrowed policies.
	narrowed.scoped = newScopedPolicies()
	return &narrowed
}

// workerLease returns the live row leaseID names when it states a boundary narrower than
// the workspace: a read-only row of any model, or a worker row with write paths. A writable
// root lease and a writable row with nothing declared report false. Liveness is
// types.JobState.Live, the same test the guard applies, so a row the guard ignores is
// one the sandbox ignores.
func workerLease(rows []types.Job, leaseID string) (types.Job, bool) {
	for _, l := range rows {
		if l.ID != leaseID {
			continue
		}
		if !l.State.Live() {
			return types.Job{}, false
		}
		// Read-only is a boundary whatever the row's place in the tree: a root row that
		// declares it gets no writes either, rather than the whole checkout.
		if l.ReadOnly {
			return l, true
		}
		if l.Parent == "" || len(l.WritePaths) == 0 {
			return types.Job{}, false
		}
		return l, true
	}
	return types.Job{}, false
}

// grantedPaths resolves write (workspace-relative doublestar globs) against root and
// returns the absolute paths to grant writes on, ancestors first and subsumed descendants
// pruned so a whole-subtree glob costs one landlock rule rather than one per file.
//
// A glob that matches nothing contributes nothing, and so does one that will not parse: a
// write path is a claim about files that exist, and inventing a rule for a path that does
// not would grant a subtree on the strength of a typo. A LITERAL path is the exception,
// because a lease routinely owns a file it is spawned to create: it grants its nearest
// existing ancestor, which is the directory the new file lands in. The guard already
// admits that write, and a kernel that refused it would be the two tiers disagreeing.
func grantedPaths(root string, write, deny []string) []string {
	denyAbs := make([]string, 0, len(deny))
	for _, f := range deny {
		denyAbs = append(denyAbs, filesystem.ResolveRulePath(filepath.Join(root, filepath.FromSlash(path.Clean(f)))))
	}

	// Containment is checked on the RESOLVED path, after symlinks: a declared `..` or a
	// symlink out of the checkout would otherwise turn a write path into a grant on
	// whatever it points at, and the root itself is never a grant, because a lease that
	// owns the whole checkout is not a worker.
	rootAbs := filesystem.ResolveRulePath(root)
	matched := make([]string, 0, len(write))
	grant := func(abs string) {
		abs = filesystem.ResolveRulePath(abs)
		if abs == rootAbs || !filesystem.Under(abs, rootAbs) {
			return
		}
		matched = append(matched, splitAroundDenied(abs, denyAbs)...)
	}
	rootFS := os.DirFS(root)
	for _, g := range write {
		pattern := strings.TrimPrefix(path.Clean(filepath.ToSlash(g)), "/")
		if !strings.ContainsAny(pattern, "*?[{") {
			if abs := nearestExisting(root, filepath.Join(root, filepath.FromSlash(pattern))); abs != "" {
				grant(abs)
			}
			continue
		}
		hits, err := doublestar.Glob(rootFS, pattern)
		if err != nil {
			continue
		}
		for _, h := range hits {
			grant(filepath.Join(root, filepath.FromSlash(h)))
		}
	}

	slices.Sort(matched)
	matched = slices.Compact(matched)
	out := make([]string, 0, len(matched))
	for _, m := range matched {
		if len(out) > 0 && filesystem.Under(m, out[len(out)-1]) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// nearestExisting walks up from abs to the first path that exists below root: the
// directory a not-yet-created file will land in. It reports "" for a path outside root
// and for one whose every ancestor below root is missing, because the only thing left to
// grant then is the checkout itself.
func nearestExisting(root, abs string) string {
	if !filesystem.Under(abs, root) {
		return ""
	}
	for abs != root {
		if _, err := os.Lstat(abs); err == nil {
			return abs
		}
		abs = filepath.Dir(abs)
	}
	return ""
}

// splitAroundDenied returns what may be granted for abs: abs itself when no denied
// path lies inside it, nothing when abs is inside one, and otherwise the same question
// asked of each of its children.
//
// The descent is what keeps one denied leaf from costing a worker its whole leased tree.
// What it cannot recover is write access to the directory HOLDING the denied path:
// granting that would grant the denied entry with it, so creating a new file beside a
// denied sibling is refused. An allowlist has no deny rule, and neither does landlock.
func splitAroundDenied(abs string, deny []string) []string {
	holds := false
	for _, f := range deny {
		if filesystem.Under(abs, f) {
			return nil
		}
		holds = holds || filesystem.Under(f, abs)
	}
	if !holds {
		return []string{abs}
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		// A denied path claims to be inside abs and abs cannot be enumerated, so
		// there is no subset that is safe to grant.
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, splitAroundDenied(filepath.Join(abs, e.Name()), deny)...)
	}
	return out
}
