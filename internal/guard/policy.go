package guard

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// The lineage actions a guard_policy trail event records.
const (
	// PolicyLoaded is the first effective rule set this cache has seen.
	PolicyLoaded = "loaded"
	// PolicyTightened is an unapproved edit whose added rules are live. An edit to an
	// existing spawn or command rule records as this too: under stricter-of evaluation only
	// its tightening takes effect before approval.
	PolicyTightened = "tightened"
	// PolicyLoosenPending is an unapproved edit that removes the spawn or command rule,
	// which has no effect until it is approved. A removed shell rule has no approved twin to
	// wait on, so it records as committed.
	PolicyLoosenPending = "loosen_pending"
	// PolicyCommitted is a rule set whose sources match the approved ones again, so what
	// was pending now applies in full.
	PolicyCommitted = "committed"
	// PolicyRemoved is a workspace with no guard rule left on either side.
	PolicyRemoved = "removed"
)

// policyMarkerFile holds the last recorded rule set, so a hook call learns whether the
// policy moved from one small read rather than by scanning the trail. It is a JSON Lines
// file whose last line is the marker.
const policyMarkerFile = "guard-policy.jsonl"

// oldPolicyMarkerFile is the JSON document an older magus kept the marker in.
const oldPolicyMarkerFile = "guard-policy.json"

// PolicySource is one file a workspace guard rule can come from, named by the ContentID of
// its working-tree bytes and of its approved bytes. Approved is "" when the approved
// state has no such file.
type PolicySource struct {
	Path     string `json:"path"`
	Worktree string `json:"worktree"`
	Approved string `json:"approved"`
}

// PolicyState is the effective workspace rule set as one hook call sees it.
type PolicyState struct {
	// Digest names the rule set; "" when the working tree declares no guard rule.
	Digest      string
	ShellRules  int
	SpawnRule   bool
	CommandRule bool
	WriteRule   bool
	StopRule    bool
	// Files are the files the load read, the root magusfile and every source it imports, as
	// the load named them. Cheap, unlike Sources: they are what the load already logged.
	Files []string
	// Sources resolves each policy source's approved id. It can run a process per file, so
	// it is called only when the record can change; nil when there is no approval authority.
	Sources func(ctx context.Context) []PolicySource
}

type policyMarker struct {
	Digest      string `json:"digest"`
	ShellRules  int    `json:"shell_rules"`
	SpawnRule   bool   `json:"spawn_rule"`
	CommandRule bool   `json:"command_rule,omitzero"`
	WriteRule   bool   `json:"write_rule,omitzero"`
	StopRule    bool   `json:"stop_rule,omitzero"`
	Pending     bool   `json:"pending"`
	// Files is PolicyState.Files, kept so a call whose own load ran out of time still knows
	// which files the guard loads.
	Files []string `json:"files,omitempty"`
}

// policyMarkerLog is the marker's file: a line for each change of the rule set, the last one
// being the marker. The file is the guard's own state, private to its owner.
func policyMarkerLog(path string) file.JSONL[policyMarker] {
	return file.JSONL[policyMarker]{Path: path, Perm: 0o600, Key: func(policyMarker) string { return "" },
		Old: filepath.Join(filepath.Dir(path), oldPolicyMarkerFile)}
}

// policyEvent is the guard_policy blob: refs and ids, never a source body.
type policyEvent struct {
	SchemaVersion int            `json:"schema_version"`
	Action        string         `json:"action"`
	Digest        string         `json:"digest"`
	Previous      string         `json:"previous,omitempty"`
	Sources       []PolicySource `json:"sources,omitempty"`
}

// recordPolicy is RecordPolicy for the location a hook call resolved, "" when the caller
// could not describe the policy.
func recordPolicy(ctx context.Context, deps Dependencies, at location, recheck bool) string {
	if deps.Policy == nil {
		return ""
	}
	return RecordPolicy(ctx, at.cacheDir, at.workspace, deps.Policy(), recheck)
}

// RecordPolicy appends one guard_policy event when the effective workspace rules changed
// since the last call recorded, and returns the digest a verdict event carries.
//
// A steady-state call costs one small file read. recheck re-reads the approved side even
// when the digest is unchanged, but only while an edit is pending: approving an edit moves
// no working-tree byte, so a spawn, which pays for the approved side anyway, is where a
// pending edit is seen to settle.
func RecordPolicy(ctx context.Context, cacheDir, workspace string, now PolicyState, recheck bool) string {
	if cacheDir == "" {
		return now.Digest
	}
	path := filepath.Join(cacheDir, policyMarkerFile)
	prev, seen := readPolicyMarker(path)
	switch {
	case !seen && now.Digest == "":
		return now.Digest
	case seen && prev.Digest == now.Digest && (!recheck || !prev.Pending) && slices.Equal(prev.Files, now.Files):
		return now.Digest
	}
	var sources []PolicySource
	if now.Sources != nil {
		sources = now.Sources(ctx)
	}
	pending := slices.ContainsFunc(sources, func(s PolicySource) bool { return s.Worktree != s.Approved })
	action := classifyPolicy(prev, seen, now, pending)
	next := policyMarker{Digest: now.Digest, ShellRules: now.ShellRules, SpawnRule: now.SpawnRule, CommandRule: now.CommandRule, WriteRule: now.WriteRule, StopRule: now.StopRule, Pending: pending, Files: now.Files}
	if action != "" {
		body, _ := json.Marshal(policyEvent{SchemaVersion: 1, Action: action, Digest: now.Digest, Previous: prev.Digest, Sources: sources})
		ref, size := trail.WriteBlob(ctx, cacheDir, "policy", body)
		trail.Append(ctx, cacheDir, trail.Event{
			Ts:           time.Now().UnixMilli(),
			Kind:         trail.KindGuardPolicy,
			Workspace:    workspace,
			Action:       action,
			Outcome:      trail.OutcomeOK,
			RequestRef:   ref,
			RequestBytes: size,
			PolicyDigest: now.Digest,
		})
	}
	writePolicyMarker(path, next)
	return now.Digest
}

// classifyPolicy names what moved. With no approval authority the working tree is the
// whole policy and every change is in effect at once, which reads as committed.
func classifyPolicy(prev policyMarker, seen bool, now PolicyState, pending bool) string {
	switch {
	case !seen:
		return PolicyLoaded
	case now.Digest == prev.Digest:
		if prev.Pending && !pending {
			return PolicyCommitted
		}
		return ""
	case now.Digest == "" && !pending:
		return PolicyRemoved
	case !pending:
		return PolicyCommitted
	case prev.SpawnRule && !now.SpawnRule, prev.CommandRule && !now.CommandRule, prev.WriteRule && !now.WriteRule, prev.StopRule && !now.StopRule:
		return PolicyLoosenPending
	case now.ShellRules < prev.ShellRules:
		// Shell rules have no approved twin, so a dropped one is in effect at once.
		return PolicyCommitted
	}
	return PolicyTightened
}

// recordedRule reports whether the last rule set this cache recorded registered seam's
// rule. The marker is written only from a policy that loaded, so it outlives a load
// failure; false when there is no marker.
func recordedRule(cacheDir string, seam functionSeam) bool {
	if cacheDir == "" {
		return false
	}
	m, ok := readPolicyMarker(filepath.Join(cacheDir, policyMarkerFile))
	if !ok {
		return false
	}
	switch seam {
	case seamCommand:
		return m.CommandRule
	case seamWrite:
		return m.WriteRule
	case seamStop:
		return m.StopRule
	}
	return m.SpawnRule
}

// readPolicyMarker is the last marker at path, false when there is none. A marker that cannot
// be read is logged and read as none.
func readPolicyMarker(path string) (policyMarker, bool) {
	held, err := policyMarkerLog(path).Load()
	if err != nil {
		reportGuardMemo(err)
	}
	if len(held) == 0 {
		return policyMarker{}, false
	}
	return held[0], true
}

// reportGuardMemo logs a memo that could not be read or written. The guard goes on without
// it, so the log is how a person learns it was not used.
func reportGuardMemo(err error) {
	message := "guard memo"
	if errors.Is(err, file.ErrLegacyStore) {
		message = "legacy guard memo: state belongs to an older magus"
	}
	slog.With(attr.Component("magus")).ErrorContext(context.Background(), message, attr.Error(err))
}

// writePolicyMarker appends the marker when it differs from the last one, so a racing hook
// reads the old record or the new one and never half of each. Losing the race costs a
// duplicate event, not a missed one.
func writePolicyMarker(path string, m policyMarker) {
	_ = policyMarkerLog(path).Update(context.Background(), func(held []policyMarker) []policyMarker {
		// By encoding: a marker read back has nil where the one written had an empty list.
		if old, err := json.Marshal(held); len(held) == 1 && err == nil {
			if next, err := json.Marshal([]policyMarker{m}); err == nil && bytes.Equal(old, next) {
				return nil
			}
		}
		return []policyMarker{m}
	})
}

// isGuardSource reports whether path is a file the guard's own policy load reads: the
// workspace's root magusfile, or a source the policy loaded on this call or, when this
// call's load did not finish, the last one recorded. A relative path is the calling
// directory's, since the write it names lands there; a relative source is the workspace's.
// Paths compare with symlinks resolved, so /var and /private/var name one file.
func isGuardSource(deps Dependencies, at location, path string) bool {
	if at.workspace == "" || path == "" {
		return false
	}
	target, placed := placePath(path, cmp.Or(at.dir, at.workspace), userHome())
	if !placed {
		return false
	}
	if samePath(target, filepath.Join(at.workspace, "magusfile.buzz")) {
		return true
	}
	var files []string
	if deps.Policy != nil {
		files = deps.Policy().Files
	}
	if files == nil && at.cacheDir != "" {
		if m, ok := readPolicyMarker(filepath.Join(at.cacheDir, policyMarkerFile)); ok {
			files = m.Files
		}
	}
	return slices.ContainsFunc(files, func(f string) bool { return samePath(resolvePath(at.workspace, f), target) })
}

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
	body, err := newSnapshotBody(ctx, rules)
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

// newSnapshotBody is what one load of rules decided, as a snapshot keeps it.
func newSnapshotBody(ctx context.Context, rules *WorkspaceRules) (snapshotBody, error) {
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
	deps.Policy = func() PolicyState { return lineage.keptPolicyState(policy) }
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
