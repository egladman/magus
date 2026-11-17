package sandbox

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

// Boundary is a job lease's write boundary over one checkout, derived from the lease's
// row by LeaseBoundary. The zero Boundary narrows nothing.
type Boundary struct {
	id       string
	from     job.LeaseSource
	root     string   // the checkout, resolved
	writable []string // granted write whatever the row says, resolved
	granted  []string // the row's write paths, resolved
}

// LeaseBoundary derives the write boundary row declares over the checkout at root. It
// reports false when the row states none narrower than the checkout: a row that is not
// live, a writable root lease (a row with no parent owns the whole checkout), or a
// writable row that declared no write paths. A read-only row of any place in the tree
// is a boundary that grants no write in the checkout.
//
// The grant is derived from the row rather than declared a second time, because a
// boundary written twice is a boundary that disagrees with itself. Reads are left as
// the policy grants them: a row declares a write boundary only, and a worker has to
// read the tree it is changing.
//
// writable are paths granted write on the narrowed policy whatever the row says, such
// as the caller's cache and temp dirs, which every child needs to produce output.
// from is the source that answered the lease, carried onto the narrowed policy.
func LeaseBoundary(row job.Job, from job.LeaseSource, root string, writable ...string) (Boundary, bool) {
	if !narrows(row) {
		return Boundary{}, false
	}
	b := Boundary{id: row.ID, from: from, root: filesystem.ResolveRulePath(root)}
	for _, w := range writable {
		if w != "" {
			b.writable = append(b.writable, filesystem.ResolveRulePath(w))
		}
	}
	if !row.ReadOnly {
		b.granted = grantedPaths(root, row.Paths.Write, row.Paths.Deny)
	}
	return b, true
}

// Lease names the lease b was derived from and the source that answered it.
func (b Boundary) Lease() (string, job.LeaseSource) { return b.id, b.from }

// Granted reports how many write grants the row's write paths resolved to.
func (b Boundary) Granted() int { return len(b.granted) }

// Narrow returns rules with every write grant on the checkout, or on a directory
// holding it, dropped, and the boundary's own write grants appended. Grants outside
// the checkout keep their writes.
//
// A deny path inside a write path costs the directory holding it, not the leased tree:
// the ruleset is an allowlist with no deny rule, so an enclosing grant is replaced by
// grants on its children (see splitAroundDenied).
func (b Boundary) Narrow(rules []filesystem.Rule) []filesystem.Rule {
	out := make([]filesystem.Rule, 0, len(rules)+len(b.granted)+len(b.writable))
	for _, r := range rules {
		if filesystem.Under(r.Path, b.root) || filesystem.Under(b.root, r.Path) {
			r.Write = false
		}
		out = append(out, r)
	}
	for _, p := range slices.Concat(b.granted, b.writable) {
		out = append(out, filesystem.Rule{Path: p, Read: true, Write: true})
	}
	return out
}

// Apply returns a copy of p narrowed to b (see Narrow) and naming b's lease. A nil p
// stays nil: no policy has no writes to narrow.
func (b Boundary) Apply(p *Policy) *Policy {
	if p == nil {
		return nil
	}
	q := *p
	q.rules = filesystem.Ruleset{Rules: b.Narrow(p.rules.Rules)}
	q.lease, q.leaseFrom = b.id, b.from
	return &q
}

// narrows reports whether row states a boundary narrower than the checkout. Liveness
// is job.State.Live, the same test the guard applies, so a row the guard ignores is
// one the sandbox ignores.
func narrows(row job.Job) bool {
	switch {
	case !row.State.Live():
		return false
	case row.ReadOnly:
		return true
	}
	return row.Parent != "" && len(row.Paths.Write) > 0
}

// grantedPaths resolves write (checkout-relative doublestar globs) against root and
// returns the absolute paths to grant writes on, ancestors first and subsumed
// descendants pruned so a whole-subtree glob costs one rule rather than one per file.
//
// A glob that matches nothing contributes nothing, and so does one that will not parse:
// a write path is a claim about files that exist, and inventing a rule for a path that
// does not would grant a subtree on the strength of a typo. A LITERAL path is the
// exception, because a lease routinely owns a file it is spawned to create: it grants
// its nearest existing ancestor, which is the directory the new file lands in.
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
