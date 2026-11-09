package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// The trail phase a judgment's snapshot lookup records, named by its outcome.
const (
	phaseSnapshotHit         = "policy_snapshot_hit"
	phaseSnapshotMiss        = "policy_snapshot_miss"
	phaseSnapshotUnavailable = "policy_snapshot_unavailable"
)

// loadPolicyRules is LoadWorkspaceRules, a var so a test can count the loads a
// judgment runs.
var loadPolicyRules = LoadWorkspaceRules

// seamRules says which function rules one side of the policy registers.
type seamRules struct {
	Spawn   bool `json:"spawn,omitzero"`
	Command bool `json:"command,omitzero"`
	Write   bool `json:"write,omitzero"`
	Stop    bool `json:"stop,omitzero"`
}

func registered(reg *workspace.WorkspaceRegistry) seamRules {
	if reg == nil {
		return seamRules{}
	}
	return seamRules{Spawn: reg.SpawnRule() != nil, Command: reg.CommandRule() != nil, Write: reg.WriteRule() != nil, Stop: reg.StopRule() != nil}
}

type snapshotSource struct {
	Path      string `json:"path"`
	ContentID string `json:"content_id"`
}

type snapshotShellRule struct {
	Name     string   `json:"name"`
	Decision string   `json:"decision"`
	Program  string   `json:"program"`
	Args     []string `json:"args,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Dialect  string   `json:"dialect,omitempty"`
}

// snapshotBody is what one load of a workspace's policy decided, as data. The
// function rules are code and are not kept: Worktree and Approved say which
// exist, and a judgment that calls one loads it then.
type snapshotBody struct {
	Sources      []snapshotSource           `json:"sources"`
	Digest       string                     `json:"digest,omitempty"`
	ShellRules   []snapshotShellRule        `json:"shell_rules,omitempty"`
	ShellDialect string                     `json:"shell_dialect,omitempty"`
	Builtins     map[string]builtin.Setting `json:"builtins"`
	Worktree     seamRules                  `json:"worktree"`
	// Approved is empty when the approved load would register the working
	// tree's rules, or none. ApprovedPending is the pending answer that
	// load reads with, so it needs no status.
	Approved        seamRules `json:"approved"`
	ApprovedPending []string  `json:"approved_pending,omitempty"`
}

// policySnapshot is one line of the store. Seal covers key and the body
// encoded again, so a field read differently from how it was written fails.
type policySnapshot struct {
	key  string
	body snapshotBody
	seal string
}

type policySnapshotLine struct {
	Key      string       `json:"key"`
	Snapshot snapshotBody `json:"snapshot"`
	Seal     string       `json:"seal"`
}

func (s policySnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(policySnapshotLine{Key: s.key, Snapshot: s.body, Seal: s.seal})
}

func (s *policySnapshot) UnmarshalJSON(b []byte) error {
	var line policySnapshotLine
	if err := json.Unmarshal(b, &line); err != nil {
		return err
	}
	*s = policySnapshot{key: line.Key, body: line.Snapshot, seal: line.Seal}
	return nil
}

// sealed is the bytes the seal covers.
func (s policySnapshot) sealed() ([]byte, error) {
	body, err := json.Marshal(s.body)
	if err != nil {
		return nil, err
	}
	return append([]byte(s.key+"\n"), body...), nil
}

// snapshotSite is where one workspace's snapshot is kept and what keys it.
type snapshotSite struct {
	w         Wiring
	store     interp.PolicyStore
	authority headPolicy
}

func (s snapshotSite) log() file.JSONL[policySnapshot] {
	// One entry per store: a store is one workspace, and only its current
	// state is worth keeping.
	return file.JSONL[policySnapshot]{Path: s.store.Path, Perm: 0o600, Key: func(policySnapshot) string { return "" }}
}

// openSnapshot resolves the snapshot site for w.Root. The error says why the
// workspace has none.
func openSnapshot(ctx context.Context, w Wiring) (snapshotSite, error) {
	store, err := interp.GuardPolicyStore(w.Root)
	if err != nil {
		return snapshotSite{}, err
	}
	authority, ok, err := approvalAuthority(ctx, w.Root, w.vcsOptions(), w.CacheDir)
	switch {
	case err != nil:
		return snapshotSite{}, err
	case !ok:
		return snapshotSite{}, errors.New("no version control names the checked-out commit to key it by")
	}
	return snapshotSite{w: w, store: store, authority: authority}, nil
}

// key names the state a load of sources would see. It covers the build, the
// checked-out commit, the agents settings and every byte in the load's
// scope. ok is false when the checkout id cannot be read.
func (s snapshotSite) key(sources []string) (string, bool) {
	files := make([]interp.SourceFile, len(sources))
	for i, p := range sources {
		files[i] = interp.SourceFile{Path: p}
	}
	id, scope, ok := s.authority.pendingFingerprint(loadScope(s.w.Root, files))
	if !ok {
		return "", false
	}
	agents, err := config.LoadAgents(s.w.Root)
	settings, merr := json.Marshal(agents)
	if err := errors.Join(err, merr); err != nil {
		settings = []byte("error: " + err.Error())
	}
	commit, _ := buildinfo.Commit()
	h := sha256.New()
	for _, part := range []string{s.w.Root, s.store.Stamp, commit, fmt.Sprintf("%+v", s.w.vcsOptions()), id, scope} {
		frame(h, framePath, []byte(part))
	}
	frame(h, frameFile, settings)
	return hex.EncodeToString(h.Sum(nil)), true
}

// lookup is the stored snapshot when it verifies and its key names the state
// on disk now. A read error is the store's; a miss is false with none.
func (s snapshotSite) lookup() (policySnapshot, bool, error) {
	entries, err := s.log().Load()
	if err != nil || len(entries) == 0 {
		return policySnapshot{}, false, err
	}
	snap := entries[len(entries)-1]
	sealed, err := snap.sealed()
	if err != nil {
		return policySnapshot{}, false, err
	}
	if !s.store.Verify(sealed, snap.seal) {
		return policySnapshot{}, false, fmt.Errorf("%s: the snapshot does not verify", s.store.Path)
	}
	paths := make([]string, len(snap.body.Sources))
	for i, src := range snap.body.Sources {
		paths[i] = src.Path
	}
	key, ok := s.key(paths)
	if !ok || key != snap.key {
		return policySnapshot{}, false, nil
	}
	return snap, true, nil
}

// write records what rules decided, resolving the approved side and the
// compiled rules' settings if the hook has not. It writes nothing when
// either fails or a source moved under the load.
func (s snapshotSite) write(ctx context.Context, rules *WorkspaceRules) error {
	body, err := snapshotOf(ctx, rules)
	if err != nil {
		return err
	}
	paths := make([]string, len(body.Sources))
	for i, src := range body.Sources {
		paths[i] = src.Path
	}
	key, err := s.stableKey(paths, body.Sources)
	if err != nil {
		return err
	}
	snap := policySnapshot{key: key, body: body}
	sealed, err := snap.sealed()
	if err != nil {
		return err
	}
	snap.seal = s.store.Seal(sealed)
	if err := s.store.Prepare(); err != nil {
		return err
	}
	return s.log().Update(ctx, func([]policySnapshot) []policySnapshot { return []policySnapshot{snap} })
}

// snapshotOf is what one load of rules decided, as the body a snapshot keeps.
func snapshotOf(ctx context.Context, rules *WorkspaceRules) (snapshotBody, error) {
	approved, err := rules.approvedIfChanged(ctx)
	if err != nil {
		return snapshotBody{}, err
	}
	builtins, err := rules.effectiveBuiltins(ctx)
	if err != nil {
		return snapshotBody{}, err
	}
	policy := rules.Policy()
	body := snapshotBody{
		Digest:   policy.Digest,
		Builtins: builtins,
		Worktree: registered(rules.registry),
		Approved: registered(approved),
	}
	if approved != nil {
		body.ApprovedPending = rules.approvedPending
	}
	for _, src := range policy.Sources {
		body.Sources = append(body.Sources, snapshotSource{Path: src.Path, ContentID: src.ContentID})
	}
	shell, dialect := rules.ShellRules()
	body.ShellDialect = string(dialect)
	for _, r := range shell {
		body.ShellRules = append(body.ShellRules, snapshotShellRule(r))
	}
	return body, nil
}

// stableKey is the key of paths once no source has moved under the load that
// read sources and the key has held still across a second read of its scope.
func (s snapshotSite) stableKey(paths []string, sources []snapshotSource) (string, error) {
	key, ok := s.key(paths)
	if !ok {
		return "", errors.New("the checkout id cannot be read without the version control tool")
	}
	for _, src := range sources {
		data, err := os.ReadFile(src.Path)
		if err != nil || interp.ContentID(data) != src.ContentID {
			return "", fmt.Errorf("%s changed after the load read it", src.Path)
		}
	}
	if again, ok := s.key(paths); !ok || again != key {
		return "", errors.New("the load's scope changed while the snapshot was keyed")
	}
	return key, nil
}

// dependencies fills deps from snap. Each function rule loads on its first
// call, and the approved side reads snap's pending answer, so neither runs a
// VCS status.
func (s snapshotSite) dependencies(deps Dependencies, snap policySnapshot) Dependencies {
	w, body := s.w, snap.body
	for _, r := range body.ShellRules {
		deps.ShellRules = append(deps.ShellRules, WorkspaceShellRule(r))
	}
	deps.ShellDialect = Dialect(body.ShellDialect)
	deps.Builtins = body.Builtins
	s.lazyWorktreeRules(&deps, body.Worktree)
	s.lazyApprovedRules(&deps, body)
	lineage := &WorkspaceRules{root: w.Root, opts: w.vcsOptions(), cacheDir: w.CacheDir}
	policy := WorkspacePolicy{
		Digest: body.Digest, ShellRules: len(body.ShellRules),
		SpawnRule: body.Worktree.Spawn, CommandRule: body.Worktree.Command, WriteRule: body.Worktree.Write, StopRule: body.Worktree.Stop,
	}
	for _, src := range body.Sources {
		policy.Sources = append(policy.Sources, interp.SourceFile{Path: src.Path, ContentID: src.ContentID})
	}
	deps.Policy = func() PolicyState { return lineage.policyStateOf(policy) }
	return deps
}

// lazyWorktreeRules sets each function rule the snapshot says the working tree
// registers, loading the tree's rules on the first call of any.
func (s snapshotSite) lazyWorktreeRules(deps *Dependencies, has seamRules) {
	w := s.w
	var working onceAnswer[*WorkspaceRules]
	worktree := func(ctx context.Context) (*WorkspaceRules, error) {
		return working.get(ctx, func(ctx context.Context) (*WorkspaceRules, error) {
			defer w.trace(ctx, "guard.load_rules")()
			rules, err := loadPolicyRules(ctx, w.Root, w.vcsOptions())
			if rules != nil {
				rules.cacheDir = w.CacheDir
			}
			return rules, err
		})
	}
	if has.Spawn {
		deps.SpawnRule = lazyRule(worktree, func(r *WorkspaceRules) func(context.Context, types.SpawnRequest, hint.Gate) (types.GuardVerdict, error) {
			return r.SpawnRule()
		})
	}
	if has.Command {
		deps.CommandRule = lazyRule(worktree, func(r *WorkspaceRules) func(context.Context, types.CommandRequest, hint.Gate) (types.GuardVerdict, error) {
			return r.CommandRule()
		})
	}
	if has.Write {
		deps.WriteRule = lazyRule(worktree, func(r *WorkspaceRules) func(context.Context, types.WriteRequest, hint.Gate) (types.GuardVerdict, error) {
			return r.WriteRule()
		})
	}
	if has.Stop {
		deps.StopRule = lazyRule(worktree, func(r *WorkspaceRules) func(context.Context, types.StopRequest, hint.Gate) (types.GuardVerdict, error) {
			return r.StopRule()
		})
	}
}

// lazyApprovedRules sets the approved side's rules, resolved on the first call
// of any from the snapshot's pending answer.
func (s snapshotSite) lazyApprovedRules(deps *Dependencies, body snapshotBody) {
	w := s.w
	var approved onceAnswer[*workspace.WorkspaceRegistry]
	registry := func(ctx context.Context) (*workspace.WorkspaceRegistry, error) {
		return approved.get(ctx, func(ctx context.Context) (*workspace.WorkspaceRegistry, error) {
			defer w.trace(ctx, "guard.approved_resolve")()
			return approvedRegistry(ctx, w.Root, s.authority, body.ApprovedPending)
		})
	}
	deps.ApprovedSpawnRule = func(ctx context.Context) (workspace.SpawnRule, error) {
		return approvedSeam(ctx, body.Approved.Spawn, registry, (*workspace.WorkspaceRegistry).SpawnRule)
	}
	deps.ApprovedCommandRule = func(ctx context.Context) (workspace.CommandRule, error) {
		return approvedSeam(ctx, body.Approved.Command, registry, (*workspace.WorkspaceRegistry).CommandRule)
	}
	deps.ApprovedWriteRule = func(ctx context.Context) (workspace.WriteRule, error) {
		return approvedSeam(ctx, body.Approved.Write, registry, (*workspace.WorkspaceRegistry).WriteRule)
	}
	deps.ApprovedStopRule = func(ctx context.Context) (workspace.StopRule, error) {
		return approvedSeam(ctx, body.Approved.Stop, registry, (*workspace.WorkspaceRegistry).StopRule)
	}
}

// lazyRule is the working tree's rule that pick reads, loaded on its first
// call. A tree whose bytes match the snapshot registers it again, so a load
// that registers none answers nothing.
func lazyRule[Req any](load func(context.Context) (*WorkspaceRules, error), pick func(*WorkspaceRules) func(context.Context, Req, hint.Gate) (types.GuardVerdict, error)) func(context.Context, Req, hint.Gate) (types.GuardVerdict, error) {
	return func(ctx context.Context, req Req, facts hint.Gate) (types.GuardVerdict, error) {
		rules, err := load(ctx)
		if err != nil {
			return types.GuardVerdict{}, err
		}
		rule := pick(rules)
		if rule == nil {
			return types.GuardVerdict{}, nil
		}
		return rule(ctx, req, facts)
	}
}

func approvedSeam[R any](ctx context.Context, has bool, registry func(context.Context) (*workspace.WorkspaceRegistry, error), pick func(*workspace.WorkspaceRegistry) R) (R, error) {
	var none R
	if !has {
		return none, nil
	}
	reg, err := registry(ctx)
	if err != nil || reg == nil {
		return none, err
	}
	return pick(reg), nil
}

// onceAnswer keeps the first answer resolved under a live ctx. An answer read
// under a dead ctx is not kept. "No rule" from a read cut short may be a
// failed read.
type onceAnswer[T any] struct {
	mu   sync.Mutex
	done bool
	v    T
	err  error
}

func (o *onceAnswer[T]) get(ctx context.Context, resolve func(context.Context) (T, error)) (T, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return o.v, o.err
	}
	v, err := resolve(ctx)
	if ctx.Err() == nil {
		o.done, o.v, o.err = true, v, err
	}
	return v, err
}

func (o *onceAnswer[T]) kept() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.done
}

// recordSnapshot notes the lookup's outcome on the judgment's trail row.
func recordSnapshot(ctx context.Context, phase string, start time.Time) {
	judgmentPhases(ctx).record(timedPhase(phase, time.Since(start), 0, false))
}

// snapshotDependencies answers deps from w.Root's snapshot when one names
// the state on disk. Otherwise ok is false and site is where a load's
// snapshot is written, the zero value when there is no store.
func snapshotDependencies(ctx context.Context, w Wiring, deps Dependencies) (out Dependencies, site snapshotSite, ok bool) {
	defer w.trace(ctx, "guard.policy_snapshot")()
	start := time.Now()
	site, err := openSnapshot(ctx, w)
	if err != nil {
		recordSnapshot(ctx, phaseSnapshotUnavailable, start)
		slog.WarnContext(ctx, "the guard keeps no policy snapshot here, so every judgment loads the policy",
			attr.Error(err), attr.Component("magus guard"))
		return deps, snapshotSite{}, false
	}
	snap, hit, err := site.lookup()
	if err != nil {
		slog.WarnContext(ctx, "the policy snapshot was not read, so this judgment loads the policy",
			attr.Error(err), attr.Component("magus guard"))
	}
	if !hit {
		recordSnapshot(ctx, phaseSnapshotMiss, start)
		return deps, site, false
	}
	recordSnapshot(ctx, phaseSnapshotHit, start)
	return site.dependencies(deps, snap), site, true
}

// keepSnapshot writes the snapshot of a load that missed it. When the
// approved side is resolved the write costs no load, so it runs now.
// Otherwise it waits for the verdict, in the slot WithPolicySnapshotWrite set.
func keepSnapshot(ctx context.Context, site snapshotSite, rules *WorkspaceRules) {
	if site.store.Path == "" {
		return
	}
	if !rules.approved.kept() {
		pendingSnapshotWrite(ctx).set(func(ctx context.Context) error { return site.write(ctx, rules) })
		return
	}
	if err := site.write(ctx, rules); err != nil {
		slog.WarnContext(ctx, "the policy snapshot was not written, so the next judgment loads the policy too",
			attr.Error(err), attr.Component("magus guard"))
	}
}

// snapshotWrite is the slot WithPolicySnapshotWrite puts on ctx: the write a
// judgment could not finish before its deadline.
type snapshotWrite struct {
	mu    sync.Mutex
	write func(context.Context) error
}

type snapshotWriteKey struct{}

func (s *snapshotWrite) set(write func(context.Context) error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.write = write
}

// WithPolicySnapshotWrite lets the judgments under ctx leave a policy
// snapshot unwritten when their deadline cut the load short. finish runs
// that write once the verdict is out, for at most budget, and runs nothing
// for a non-positive budget. It starts no process that outlives its return.
func WithPolicySnapshotWrite(ctx context.Context) (context.Context, func(ctx context.Context, budget time.Duration) error) {
	slot := &snapshotWrite{}
	finish := func(ctx context.Context, budget time.Duration) error {
		slot.mu.Lock()
		write := slot.write
		slot.write = nil
		slot.mu.Unlock()
		if write == nil || budget <= 0 {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- write(ctx) }()
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return fmt.Errorf("the policy snapshot was not written within %s", budget)
		}
	}
	return context.WithValue(ctx, snapshotWriteKey{}, slot), finish
}

func pendingSnapshotWrite(ctx context.Context) *snapshotWrite {
	slot, _ := ctx.Value(snapshotWriteKey{}).(*snapshotWrite)
	return slot
}

// WritePolicySnapshot loads the policy of the workspace at w.Root to the end,
// bounded by ctx alone, and records it for the hooks that follow. Both sides'
// compiled Buzz chunks stay in the bytecode stores. An error is a policy that
// does not load or a store that cannot be written.
func WritePolicySnapshot(ctx context.Context, w Wiring) error {
	site, err := openSnapshot(ctx, w)
	if err != nil {
		return err
	}
	rules, err := loadPolicyRules(ctx, w.Root, w.vcsOptions())
	if err != nil {
		return err
	}
	rules.cacheDir = w.CacheDir
	return site.write(ctx, rules)
}
