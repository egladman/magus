// Package confinement builds a workspace's sandbox policy from its config, the host
// and the acting lease's job row, and holds the process-wide landlock state that
// policy is applied under.
//
// It is not part of internal/sandbox because it reports to the observability
// provider, and observability imports cache, which imports proc/run, which imports
// sandbox.
package confinement

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
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

// globalsMu guards policyFingerprint, appliedExternally and kernelEnforced: they are read
// from Apply on arbitrary goroutines and written from MarkAppliedExternally on arbitrary
// goroutines, so (unlike applyErr, which only ever changes inside the applyOnce.Do
// callback) they need their own lock rather than riding on sync.Once's happens-before.
var globalsMu sync.Mutex

// policyFingerprint is the fingerprint of the applied landlock policy.
// Subsequent Apply calls with a different fingerprint are rejected (MGS2010) because the ruleset is immutable.
var policyFingerprint string

// appliedExternally is set when the server has already applied the union ruleset via MarkAppliedExternally.
// In this mode per-workspace Apply calls are attach-only (no syscall, no fingerprint check).
var appliedExternally bool

// kernelEnforced is set once a landlock ruleset is in force on this process, so a
// required sandbox can tell kernel enforcement from the binding-check fallback.
var kernelEnforced bool

// MarkAppliedExternally records that the server has already applied the union landlock
// ruleset, enforced saying whether the kernel took it. Subsequent per-workspace Apply calls
// become attach-only; the MGS2010 fingerprint check is skipped.
func MarkAppliedExternally(fp string, enforced bool) {
	applyOnce.Do(func() {})
	globalsMu.Lock()
	policyFingerprint = fp
	appliedExternally = true
	kernelEnforced = enforced
	globalsMu.Unlock()
}

// ErrRequired returns the MGS2012 refusal of a required sandbox at root that the kernel
// is not enforcing, why saying what stands in the way.
func ErrRequired(root, why string) error {
	return types.DiagnosticErrorf(types.SandboxRequired,
		"sandbox mode is required for %s, and %s; run it on Linux 5.13 or newer with landlock enabled (/sys/kernel/security/landlock)", root, why)
}

// FromConfig builds the sandbox policy for the workspace at root from cfg and the host:
// its environment, the running binary, the checkout's git directories and the go
// toolchain on PATH. It creates the private temp dir children get as TMPDIR (see
// privateTempDir).
//
// A sandbox.allow entry that does not resolve, and a passthrough pattern that does not
// parse, are errors (MGS2004): a sandbox that quietly grants less than was written
// breaks builds in ways nobody can trace, and one that grants more is not a sandbox.
func FromConfig(root, cacheDir string, cfg config.SandboxConfig) (*sandbox.Policy, error) {
	home, _ := os.UserHomeDir()
	var errs []error
	allow := make([]filesystem.Rule, 0, len(cfg.Allow))
	for _, a := range cfg.Allow {
		rule, err := filesystem.ExpandUserRule(a.Path, a.Mode, home, os.LookupEnv)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		allow = append(allow, rule)
	}
	passthrough, err := env.Parse(cfg.Env.Passthrough)
	errs = append(errs, err)
	if err := errors.Join(errs...); err != nil {
		return nil, types.WrapDiagnostic(types.AllowlistUnresolved, err, "sandbox config for %s", root)
	}

	tmp, err := privateTempDir(os.TempDir(), root)
	if err != nil {
		return nil, err
	}
	if filesystem.Under(filesystem.ResolveRulePath(tmp), filesystem.ResolveRulePath(root)) {
		return nil, fmt.Errorf("sandbox: the private temp dir %s is inside the workspace %s; point TMPDIR outside it", tmp, root)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("sandbox: locate the running binary: %w", err)
	}
	gitDir, commonDir := gitDirs(root)
	var installs []string
	if goroot := goRootOnPath(home); goroot != "" {
		installs = append(installs, goroot)
	}
	return sandbox.BuildPolicy(sandbox.PolicyOptions{
		Workspace:    root,
		CacheDir:     cacheDir,
		TempDir:      tmp,
		Executable:   exe,
		GitDir:       gitDir,
		GitCommonDir: commonDir,
		Home:         home,
		GOOS:         runtime.GOOS,
		Environ:      os.Environ(),
		InstallDirs:  installs,
		Allow:        allow,
		Env:          passthrough,
	}), nil
}

// privateTempDir returns base/magus-sandbox-<uid>-<hash of root>, creating it 0700.
//
// It sits outside the workspace because a temp dir inside a checkout changes what
// tools see: a repository a test creates there is nested in the workspace's own. The
// name is fixed per workspace rather than per run, so every run of one workspace in a
// server process builds the same policy and passes the MGS2010 fingerprint check.
// base is usually world-writable, so a path that already exists must be a directory
// this user owns and nobody else can enter, or another account could have planted it.
func privateTempDir(base, root string) (string, error) {
	sum := sha256.Sum256([]byte(root))
	dir := filepath.Join(base, fmt.Sprintf("magus-sandbox-%d-%x", os.Getuid(), sum[:6]))
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("sandbox: create the private temp dir: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("sandbox: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ownedByUser(info) {
		return "", fmt.Errorf("sandbox: %s is not a private directory of this user; remove it and let magus recreate it", dir)
	}
	return dir, nil
}

// gitDirs returns the git directory of the checkout holding root and the repository's
// common one; both are empty outside a git checkout. A linked worktree's .git is a file
// naming its own directory, which names the common one in its commondir file.
func gitDirs(root string) (gitDir, commonDir string) {
	for dir := root; ; dir = filepath.Dir(dir) {
		dotgit := filepath.Join(dir, ".git")
		info, err := os.Stat(dotgit)
		if err == nil && info.IsDir() {
			return dotgit, dotgit
		}
		if err == nil {
			return linkedGitDirs(dotgit)
		}
		if filepath.Dir(dir) == dir {
			return "", ""
		}
	}
}

func linkedGitDirs(dotgit string) (gitDir, commonDir string) {
	b, err := os.ReadFile(dotgit)
	if err != nil {
		return "", ""
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return "", ""
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(dotgit), gitDir)
	}
	commonDir = gitDir
	if c, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = strings.TrimSpace(string(c))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
	}
	return filepath.Clean(gitDir), filepath.Clean(commonDir)
}

// goRootOnPath returns the GOROOT of the go binary on PATH when it resolves into a Go
// install (<root>/bin/go beside <root>/pkg/tool), the toolchain directory a build execs
// the compiler from. A shim resolves elsewhere and yields nothing, and so does a root
// that would contain home.
func goRootOnPath(home string) string {
	bin, err := exec.LookPath("go")
	if err != nil {
		return ""
	}
	if bin, err = filepath.EvalSymlinks(bin); err != nil {
		return ""
	}
	root := filepath.Dir(filepath.Dir(bin))
	if filepath.Base(filepath.Dir(bin)) != "bin" || (home != "" && filesystem.Under(home, root)) {
		return ""
	}
	if info, err := os.Stat(filepath.Join(root, "pkg", "tool")); err != nil || !info.IsDir() {
		return ""
	}
	return root
}

// Apply applies the kernel-level landlock sandbox (once per process) and attaches policy
// to ctx. Without landlock, best-effort logs MGS2005 and falls back to binding checks,
// and required refuses with MGS2012 and attaches nothing. A fingerprint mismatch rejects
// the run with MGS2010 (landlock is immutable once set).
func Apply(ctx context.Context, policy *sandbox.Policy, root string, mode types.SandboxMode) (context.Context, error) {
	required := mode.Resolved() == types.SandboxModeRequired
	// Stamp the live provider as the binding-layer sandbox metrics recorder so the
	// fs/archive/crypto/exec checks (which run below observability in the import graph
	// and cannot reach it directly) can report allow/deny decisions and dropped env
	// counts down the same ctx chain that carries the Policy.
	if prov := observability.FromContext(ctx); prov != nil {
		ctx = sandbox.WithMetrics(ctx, prov)
	}

	globalsMu.Lock()
	externally, enforced := appliedExternally, kernelEnforced
	globalsMu.Unlock()
	if externally { // server applied union policy; attach-only
		if required && !enforced {
			return ctx, ErrRequired(root, "the ruleset this process runs under is not kernel-enforced")
		}
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
			globalsMu.Lock()
			kernelEnforced = true
			globalsMu.Unlock()
			RecordApply(ctx, secs, "applied", "workspace", policy)
		case errors.Is(applyErr, sandbox.ErrUnsupported) && required:
			// Left as the error: the ruleset was never installed, so no later Apply in
			// this process may run as though it were.
			RecordApply(ctx, secs, "unsupported", "workspace", policy)
			applyErr = ErrRequired(root, "the kernel cannot enforce it: "+applyErr.Error())
		case errors.Is(applyErr, sandbox.ErrUnsupported):
			warnedUnsupported.Do(func() {
				slog.WarnContext(ctx, types.FormatDiagnostic(types.SandboxUnsupported,
					"kernel landlock unavailable; sandbox running with binding-level checks only"),
					"reason", applyErr.Error())
			})
			// Best-effort's documented fallback: binding-level checks still enforce the
			// same rules on what goes through a binding, recorded as "unsupported".
			applyErr = nil
			RecordApply(ctx, secs, "unsupported", "workspace", policy)
		}
		// A hard kernel error falls through unrecorded; the run aborts below.
	})
	if applyErr != nil {
		if errors.Is(applyErr, types.SandboxRequired) {
			return ctx, applyErr
		}
		// Fail closed: ruleset was partially built but restrict_self was never called.
		return ctx, fmt.Errorf("sandbox: kernel sandbox failed: %w", applyErr)
	}

	globalsMu.Lock()
	current, enforced := policyFingerprint, kernelEnforced
	globalsMu.Unlock()
	if required && !enforced {
		return ctx, ErrRequired(root, "an earlier apply in this process fell back to binding-level checks")
	}
	if fp != current { // mismatch: kernel-level and binding-level policies would disagree
		RecordApply(ctx, 0, "mismatch", "workspace", nil) // no ruleset installed; count the outcome, not rules
		return ctx, fmt.Errorf("%w: sandbox policy for workspace %q differs from the policy already applied to this server process (fingerprint %s vs %s); restart the server to pick up new sandbox configuration",
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
		EnvExact: int64(len(policy.Env.Names)),
		EnvGlob:  int64(len(policy.Env.Prefixes)),
		Scope:    scope,
	})
}

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
func NarrowToLease(ctx context.Context, policy *sandbox.Policy, loc job.Location, leaseID string, from types.LeaseSource) *sandbox.Policy {
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
	rootAbs := filesystem.ResolveRulePath(loc.Root)
	rules := make([]filesystem.Rule, 0, len(policy.FS.Rules)+len(granted)+2)
	for _, r := range policy.FS.Rules {
		if filesystem.Under(r.Path, rootAbs) || filesystem.Under(rootAbs, r.Path) {
			r.Write = false
		}
		rules = append(rules, r)
	}
	for _, p := range granted {
		rules = append(rules, filesystem.Rule{Path: p, Read: true, Write: true})
	}
	for _, p := range []string{loc.CacheDir, policy.TempDir} {
		if p == "" {
			continue
		}
		rules = append(rules, filesystem.Rule{Path: filesystem.ResolveRulePath(p), Read: true, Write: true})
	}

	narrowed := *policy
	narrowed.FS = filesystem.Ruleset{Rules: rules}
	narrowed.Lease = row.ID
	narrowed.LeaseFrom = from
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
