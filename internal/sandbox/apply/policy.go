// Package apply builds per-workspace sandbox policies from config and the acting lease's
// ledger row, and owns the process-wide landlock application state. It lives here (not in
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
	"github.com/egladman/magus/internal/ledger"
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
// names declared in the workspace ledger. It returns policy untouched when there is no
// boundary to derive one from: no lease id, no row, a terminal row, a ROOT lease (a row
// with no parent is the orchestrator, and it owns the whole checkout), or a row that
// declared no owned paths.
//
// The grant is DERIVED from the row rather than declared a second time in magus.yaml,
// because a boundary written twice is a boundary that disagrees with itself. The agent
// guard already grades a write against owned_paths, so a separate sandbox declaration
// would let the kernel refuse something other than what the guard explains, and one of
// the two would be teaching a rule nothing enforces.
//
// Reads are left exactly as the workspace policy granted them: the ledger declares a
// write boundary only, and a worker has to read the tree it is changing.
//
// Beyond the owned paths it grants writes to the workspace cache directory and $TMPDIR,
// which every target run needs to produce output at all.
//
// A forbidden path INSIDE an owned one costs the directory holding it, not the owned tree:
// this ruleset and landlock are both allowlists with no deny rule, so an enclosing grant
// is replaced by grants on its children (see splitAroundForbidden).
//
// An unreadable ledger fails OPEN with a warning, matching the guard: a lease id that
// stops resolving must not brick the checkout a person is working in.
func NarrowToLease(ctx context.Context, policy *sandbox.Policy, root, cacheDir, leaseID string) *sandbox.Policy {
	if policy == nil || root == "" || leaseID == "" {
		return policy
	}
	rows, err := ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root}).List()
	if err != nil {
		slog.WarnContext(ctx, types.FormatDiagnostic(types.AllowlistUnresolved,
			"lease ledger unreadable; sandbox running with the workspace write grant"),
			"lease", leaseID, "err", err.Error())
		return policy
	}
	row, ok := workerLease(rows, leaseID)
	if !ok {
		return policy
	}

	granted := grantedPaths(root, row.OwnedPaths, row.ForbiddenPaths)
	rules := make([]filesystem.Rule, 0, len(policy.FS.Rules)+len(granted)+2)
	for _, r := range policy.FS.Rules {
		r.Write = false
		rules = append(rules, r)
	}
	for _, p := range granted {
		rules = append(rules, filesystem.Rule{Path: p, Read: true, Write: true})
	}
	for _, p := range []string{cacheDir, os.TempDir()} {
		if p == "" {
			continue
		}
		rules = append(rules, filesystem.Rule{Path: filesystem.ResolveRulePath(p), Read: true, Write: true})
	}

	narrowed := *policy
	narrowed.FS = filesystem.Ruleset{Rules: rules}
	narrowed.Lease = row.ID
	slog.InfoContext(ctx, "magus: narrowed the sandbox write grant to a lease boundary",
		"lease", row.ID, "parent", row.Parent, "owned_paths", len(row.OwnedPaths), "write_rules", len(granted))
	return &narrowed
}

// workerLease returns the live worker row leaseID names. A root lease, a terminal row, and
// a row with no declared owned paths all report false: none of them states a boundary
// narrower than the workspace.
func workerLease(rows []types.Lease, leaseID string) (types.Lease, bool) {
	for _, l := range rows {
		if l.ID != leaseID {
			continue
		}
		if l.Parent == "" || len(l.OwnedPaths) == 0 {
			return types.Lease{}, false
		}
		switch l.State {
		case types.StatePass, types.StateFail, types.StateNoReturn:
			return types.Lease{}, false
		}
		return l, true
	}
	return types.Lease{}, false
}

// grantedPaths resolves owned (workspace-relative doublestar globs) against root and
// returns the absolute paths to grant writes on, ancestors first and subsumed descendants
// pruned so a whole-subtree glob costs one landlock rule rather than one per file.
//
// A glob that matches nothing contributes nothing, and so does one that will not parse: an
// owned path is a claim about files that exist, and inventing a rule for a path that does
// not would grant a subtree on the strength of a typo.
func grantedPaths(root string, owned, forbidden []string) []string {
	forbiddenAbs := make([]string, 0, len(forbidden))
	for _, f := range forbidden {
		forbiddenAbs = append(forbiddenAbs, filesystem.ResolveRulePath(filepath.Join(root, filepath.FromSlash(path.Clean(f)))))
	}

	rootFS := os.DirFS(root)
	matched := make([]string, 0, len(owned))
	for _, g := range owned {
		pattern := strings.TrimPrefix(path.Clean(filepath.ToSlash(g)), "/")
		hits, err := doublestar.Glob(rootFS, pattern)
		if err != nil {
			continue
		}
		for _, h := range hits {
			abs := filesystem.ResolveRulePath(filepath.Join(root, filepath.FromSlash(h)))
			matched = append(matched, splitAroundForbidden(abs, forbiddenAbs)...)
		}
	}

	slices.Sort(matched)
	matched = slices.Compact(matched)
	out := make([]string, 0, len(matched))
	for _, m := range matched {
		if len(out) > 0 && under(m, out[len(out)-1]) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// splitAroundForbidden returns what may be granted for abs: abs itself when no forbidden
// path lies inside it, nothing when abs is inside one, and otherwise the same question
// asked of each of its children.
//
// The descent is what keeps one forbidden leaf from costing a worker its whole owned tree.
// What it cannot recover is write access to the directory HOLDING the forbidden path:
// granting that would grant the forbidden entry with it, so creating a new file beside a
// forbidden sibling is refused. An allowlist has no deny rule, and neither does landlock.
func splitAroundForbidden(abs string, forbidden []string) []string {
	holds := false
	for _, f := range forbidden {
		if under(abs, f) {
			return nil
		}
		holds = holds || under(f, abs)
	}
	if !holds {
		return []string{abs}
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		// A forbidden path claims to be inside abs and abs cannot be enumerated, so
		// there is no subset that is safe to grant.
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, splitAroundForbidden(filepath.Join(abs, e.Name()), forbidden)...)
	}
	return out
}

// under reports whether child is at or beneath parent; both must be absolute and clean.
func under(child, parent string) bool {
	return child == parent || strings.HasPrefix(child, parent+string(filepath.Separator))
}
