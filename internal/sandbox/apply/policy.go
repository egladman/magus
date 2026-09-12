// Package apply builds per-workspace sandbox policies from config and the acting lease's
// job row, and owns the process-wide landlock application state. It lives here (not in
// sandbox or config) to break the import cycle.
package apply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// applyOnce gates landlock_restrict_self; landlock is permanent so it must run at most once per process.
var applyOnce sync.Once

// applyErr holds the outcome of the one-shot Apply call (success, ErrUnsupported, or kernel error).
var applyErr error

// warnedUnsupported gates the MGS2005 warning to at most one log line per process.
var warnedUnsupported sync.Once

// globalsMu guards policyFingerprint and appliedExternally: both are read from Apply
// on arbitrary goroutines and written from MarkAppliedExternally on arbitrary
// goroutines, so (unlike applyErr, which only ever changes inside the applyOnce.Do
// callback) they need their own lock rather than riding on sync.Once's happens-before.
var globalsMu sync.Mutex

// policyFingerprint is the fingerprint of the applied landlock policy.
// Subsequent Apply calls with a different fingerprint are rejected (MGS2010) because the ruleset is immutable.
var policyFingerprint string

// appliedExternally is set when the daemon has already applied the union ruleset via MarkAppliedExternally.
// In this mode per-workspace Apply calls are attach-only (no syscall, no fingerprint check).
var appliedExternally bool

// MarkAppliedExternally records that the daemon has already applied the union landlock ruleset.
// Subsequent per-workspace Apply calls become attach-only; the MGS2010 fingerprint check is skipped.
func MarkAppliedExternally(fp string) {
	applyOnce.Do(func() {})
	globalsMu.Lock()
	policyFingerprint = fp
	appliedExternally = true
	globalsMu.Unlock()
}

// FromConfig assembles a sandbox Policy for root using the sandbox fields of cfg.
func FromConfig(ctx context.Context, root string, cfg config.Config) *sandbox.Policy {
	userExtras := make([]filesystem.Rule, 0, len(cfg.Sandbox.Allow))
	for _, pp := range cfg.Sandbox.Allow {
		read := true
		write := pp.Mode == "rw"
		rule, err := filesystem.ExpandUserRule(pp.Path, read, write)
		if err != nil {
			slog.WarnContext(ctx, types.FormatDiagnostic(types.AllowlistUnresolved,
				"sandbox.allow entry failed to resolve; skipped"),
				"path", pp.Path, "err", err)
			continue
		}
		userExtras = append(userExtras, rule)
	}
	var exact, globs []string
	for _, name := range cfg.Sandbox.Env.Passthrough {
		if strings.Contains(name, "*") {
			if err := env.ValidateGlobs([]string{name}); err != nil {
				slog.WarnContext(ctx, types.FormatDiagnostic(types.AllowlistUnresolved,
					"sandbox.env.passthrough pattern must end in '*'; ignoring"),
					"pattern", name, "err", err)
				continue
			}
			globs = append(globs, name)
		} else {
			exact = append(exact, name)
		}
	}
	return sandbox.BuildPolicy(root, userExtras, nil, exact, globs)
}

// Apply applies the kernel-level landlock sandbox (once per process) and attaches policy to ctx.
// ErrUnsupported logs MGS2005 and falls through to interpreter-level enforcement.
// A fingerprint mismatch rejects the run with MGS2010 (landlock is immutable once set).
func Apply(ctx context.Context, policy *sandbox.Policy, root string) (context.Context, error) {
	// Stamp the live provider as the binding-layer sandbox metrics recorder so the
	// fs/archive/crypto/exec checks (which run below observability in the import graph
	// and cannot reach it directly) can report allow/deny decisions and dropped env
	// counts down the same ctx chain that carries the Policy.
	if prov := observability.FromContext(ctx); prov != nil {
		ctx = sandbox.WithMetrics(ctx, prov)
	}

	globalsMu.Lock()
	externally := appliedExternally
	globalsMu.Unlock()
	if externally { // daemon applied union policy; attach-only
		return sandbox.WithPolicy(ctx, policy), nil
	}

	fp := policy.Fingerprint()

	applyOnce.Do(func() {
		globalsMu.Lock()
		policyFingerprint = fp
		globalsMu.Unlock()
		start := time.Now()
		applyErr = sandbox.Apply(policy)
		secs := time.Since(start).Seconds()
		switch {
		case applyErr == nil:
			RecordApply(ctx, secs, "applied", "workspace", policy)
		case errors.Is(applyErr, sandbox.ErrUnsupported):
			warnedUnsupported.Do(func() {
				slog.WarnContext(ctx, types.FormatDiagnostic(types.SandboxUnsupported,
					"kernel landlock unavailable; sandbox running with interpreter-level checks only"),
					"reason", applyErr.Error())
			})
			// ErrUnsupported is the documented fallback path; not fatal. Binding-level
			// checks still enforce the same rules, so record them under "unsupported".
			applyErr = nil
			RecordApply(ctx, secs, "unsupported", "workspace", policy)
		}
		// A hard kernel error falls through unrecorded; the run aborts below.
	})
	if applyErr != nil {
		// Fail closed: ruleset was partially built but restrict_self was never called.
		return ctx, fmt.Errorf("sandbox: kernel sandbox failed: %w", applyErr)
	}

	globalsMu.Lock()
	current := policyFingerprint
	globalsMu.Unlock()
	if fp != current { // mismatch: kernel-level and binding-level policies would disagree
		RecordApply(ctx, 0, "mismatch", "workspace", nil) // no ruleset installed; count the outcome, not rules
		return ctx, fmt.Errorf("%w: sandbox policy for workspace %q differs from the policy already applied to this daemon process (fingerprint %s vs %s); restart the daemon to pick up new sandbox configuration",
			types.DiagnosticErrorf(types.SandboxPolicyMismatch, "sandbox policy mismatch"),
			root, fp, current)
	}

	return sandbox.WithPolicy(ctx, policy), nil
}

// RecordApply reports one sandbox-apply attempt to the observability provider on ctx
// (a no-op when none is stamped). secs is the apply wall-clock duration; outcome is
// applied|unsupported|mismatch; scope is workspace|union. When policy is non-nil its
// filesystem and env rule counts are also recorded under the same scope; pass nil (a
// mismatch installs no ruleset) to record only the outcome.
func RecordApply(ctx context.Context, secs float64, outcome, scope string, policy *sandbox.Policy) {
	prov := observability.FromContext(ctx)
	if prov == nil {
		return
	}
	prov.RecordSandboxApply(ctx, secs, outcome, scope)
	if policy == nil {
		return
	}
	var read, write, exec int64
	for _, r := range policy.FS.Rules {
		if r.Read {
			read++
		}
		if r.Write {
			write++
		}
		if r.Exec {
			exec++
		}
	}
	prov.RecordSandboxRules(ctx, observability.SandboxRules{
		Read:     read,
		Write:    write,
		Exec:     exec,
		EnvExact: int64(len(policy.Env.Allow)),
		EnvGlob:  int64(len(policy.Env.Globs)),
		Scope:    scope,
	})
}

// NarrowToLease reduces policy's filesystem WRITE grant to the boundary the lease leaseID
// names declared in the job store at loc. It returns policy untouched when there is no
// boundary to derive one from: no lease id, no row, a row that is not live, a ROOT lease
// (a row with no parent is the orchestrator, and it owns the whole checkout), or a row
// that declared no write paths and is not read-only. A read-only row narrows the grant
// to nothing but the cache dir and $TMPDIR, the same answer the guard gives its writes.
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
// Beyond the write paths it grants writes to the workspace cache directory and $TMPDIR,
// which every target run needs to produce output at all.
//
// A deny path INSIDE a write one costs the directory holding it, not the leased tree:
// this ruleset and landlock are both allowlists with no deny rule, so an enclosing grant
// is replaced by grants on its children (see splitAroundDenied).
//
// An unreadable job store fails OPEN with a warning, matching the guard: a lease id that
// stops resolving must not brick the checkout a person is working in.
func NarrowToLease(ctx context.Context, policy *sandbox.Policy, loc job.Location, leaseID string) *sandbox.Policy {
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

	var granted []string
	if !row.ReadOnly {
		granted = grantedPaths(loc.Root, row.WritePaths, row.DenyPaths)
	}
	rules := make([]filesystem.Rule, 0, len(policy.FS.Rules)+len(granted)+2)
	for _, r := range policy.FS.Rules {
		r.Write = false
		rules = append(rules, r)
	}
	for _, p := range granted {
		rules = append(rules, filesystem.Rule{Path: p, Read: true, Write: true})
	}
	for _, p := range []string{loc.CacheDir, os.TempDir()} {
		if p == "" {
			continue
		}
		rules = append(rules, filesystem.Rule{Path: filesystem.ResolveRulePath(p), Read: true, Write: true})
	}

	narrowed := *policy
	narrowed.FS = filesystem.Ruleset{Rules: rules}
	narrowed.Lease = row.ID
	slog.InfoContext(ctx, "magus: narrowed the sandbox write grant to a lease boundary",
		"lease", row.ID, "parent", row.Parent, "write_paths", len(row.WritePaths), "write_rules", len(granted))
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
