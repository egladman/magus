package guard

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// workspaceSpawnRule is the Verdict.Rule a magus\guard.spawn answer carries, in the
// namespace workspace shell rules already use so a reader can tell it from a built-in.
const workspaceSpawnRule = workspaceShellPrefix + "spawn"

// advisorySpawnRuleFailed names the notice that a workspace spawn rule judged nothing.
// Each distinct set of failures is told in full once per session, since the rule stays
// broken on every spawn until someone edits it.
const advisorySpawnRuleFailed hint.MarkerKind = "workspace-spawn-failed"

// spawnFields is what a spawn's tool_input says about the child it asks for.
type spawnFields struct {
	AgentType   string
	Description string
	Name        string
	Background  bool
	Isolated    bool
}

// spawnedAgentID reads the id a host assigned the child from a finished spawn call's
// response, "" when the response names none. Both spellings are accepted because the
// key is the host's and magus reads it by shape, never by host name.
func spawnedAgentID(response any) string {
	m, ok := response.(map[string]any)
	if !ok {
		return ""
	}
	return cmp.Or(envelopeString(m, "agentId"), envelopeString(m, "agent_id"))
}

// judgeAgentEvent answers a spawn or a continuation of a subagent.
//
// Nothing here judges the prompt: it is prose, and a prompt that merely mentions a denied
// command would otherwise block the spawn describing it. The built-in rules read session
// state; the workspace's magus\guard.spawn rule then reads the normalized request and may
// only add to what they said.
func judgeAgentEvent(ctx context.Context, deps Dependencies, req Request, env hookRequest, who hookAttribution) Verdict {
	at := hookLocation(ctx, deps)
	facts := hint.NewGate(at.cacheDir, who.sessionKey())
	// A spawn call that has already run judges nothing, and judging it again would spend
	// the session's markers twice. What it carries is the id the host gave the child,
	// which is what lets that child's own later calls name their parent and their job.
	if env.AfterCall {
		if env.SpawnedAgent != "" {
			recordSpawnedAgent(ctx, deps, at, facts, env)
		}
		return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
	}
	digest := recordPolicy(ctx, deps, at, true)
	ctx = withJobStoreRows(ctx, at)

	verdict := Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
	decided := ""
	var failures []trail.RuleFailure
	if env.IsSpawn {
		if verdict = spawnBuiltIns(ctx, req, who, at); verdict.Decision != "pass" {
			decided = decidedByBuiltin
		}
	}
	// Strengthen only, and so asked only when there is something left to strengthen: a
	// workspace allow can never lift a built-in deny.
	if verdict.Decision != "deny" {
		spawnReq := spawnRequest(ctx, env, who, at, facts, req.Lease)
		bind := func(rule workspace.SpawnRule) ruleCall {
			if rule == nil {
				return nil
			}
			return func(ctx context.Context) (types.GuardVerdict, error) { return rule(ctx, spawnReq, facts) }
		}
		var resolve func(context.Context) (ruleCall, error)
		if deps.ApprovedSpawnRule != nil {
			resolve = func(ctx context.Context) (ruleCall, error) {
				rule, err := deps.ApprovedSpawnRule(ctx)
				return bind(rule), err
			}
		}
		asked := askWorkspaceRules(ctx, seamSpawn, deps.LoadFailure, resolve, bind(deps.SpawnRule))
		failures = asked.failures
		verdict, decided = applyWorkspaceAnswer(verdict, decided, asked, workspaceSpawnRule)
		verdict = applyRuleFailureNote(verdict, ruleFailureNote(hint.NewGate(at.cacheDir, who.callerKey()), seamSpawn, failures, asked.answered), advisorySpawnRuleFailed)
	}
	appendHookSpawn(ctx, deps, env, who, spawnVerdictRecord{
		policyDigest: digest,
		decidedBy:    decided,
		target:       resolveAgentID(facts, env.Target),
		ruleFailures: failures,
	})
	if verdict.Decision != "deny" {
		markAgentSeen(facts, env)
	}
	return verdict
}

// spawnBuiltIns is the compiled half of a spawn's verdict, which reads session state and
// never the prompt.
func spawnBuiltIns(ctx context.Context, req Request, who hookAttribution, at location) Verdict {
	// Whether the multi-agent brief was read before work was handed out: a marker file,
	// no prose, which is what lets it live on this path.
	if reason := denySpawnWithoutBrief(hint.NewGate(at.cacheDir, who.sessionKey()), req.ObservesSkillLoads, at.workspace); reason != "" {
		return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "deny", Reason: reason, Rule: string(denySpawnUnbriefed)}
	}
	// Whether this checkout is already somebody's, asked the same way and for the same reason.
	if note := adviseSharedCheckoutSpawn(ctx, hint.NewGate(at.cacheDir, who.callerKey()), at); note != "" {
		return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "advise", Context: note, Rule: string(advisorySharedCheckout)}
	}
	return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
}

// spawnRequest normalizes one spawn or continuation for the workspace rule. Every field is
// read from the envelope or from what magus recorded; none is inferred from the host.
func spawnRequest(ctx context.Context, env hookRequest, who hookAttribution, at location, facts hint.Gate, explicitLease string) types.SpawnRequest {
	req := types.SpawnRequest{
		Kind:        types.SpawnKindSpawn,
		Host:        who.Host,
		Session:     who.Session,
		Model:       env.DeclaredModel,
		AgentType:   env.Spawn.AgentType,
		Description: env.Spawn.Description,
		Name:        env.Spawn.Name,
		Prompt:      env.Value,
		Background:  env.Spawn.Background,
		Isolated:    env.Spawn.Isolated,
		Parent:      spawnedAs(facts, who.Agent),
	}
	if env.IsContinue {
		req.Kind = types.SpawnKindContinue
		req.Target = continueTarget(facts, env.Target, time.Now())
	}
	// The same resolution every lease-scoped rule uses, so the rule and the guard cannot
	// disagree about who is acting.
	req.Role, req.Lease = actingRole(ctx, at, actingLeaseFor(who, at, facts, explicitLease))
	return req
}

// agentSeenKind names the marker holding when magus last saw agent in a session. Hashed
// because an agent name is host-chosen text and a marker kind is a filename component.
func agentSeenKind(agentID string) hint.MarkerKind {
	return agentMarkerKind("agent-seen-", agentID)
}

// spawnedAgentKind names the marker recording what a subagent was spawned as.
func spawnedAgentKind(agentID string) hint.MarkerKind {
	return agentMarkerKind("agent-spawned-", agentID)
}

func agentMarkerKind(prefix, agentID string) hint.MarkerKind {
	sum := sha256.Sum256([]byte(agentID))
	return hint.MarkerKind(prefix + hex.EncodeToString(sum[:8]))
}

// agentIdle is how long ago, in milliseconds, magus last saw agent spawned, continued or
// finish its spawn call. Nil when it never did, which the rule reads as unknown rather
// than as idle forever or not at all.
func agentIdle(facts hint.Gate, agentID string, now time.Time) *int64 {
	if agentID == "" {
		return nil
	}
	seen, ok := facts.LastSeen(agentSeenKind(agentID))
	if !ok {
		return nil
	}
	idle := max(now.Sub(seen).Milliseconds(), 0)
	return &idle
}

// markAgentSeen restarts the idle clock of the agent a continue addresses. A spawn's clock
// starts when its call finishes, since only then does the child have an id to key it on.
func markAgentSeen(facts hint.Gate, env hookRequest) {
	if env.IsContinue {
		facts.Touch(agentSeenKind(resolveAgentID(facts, env.Target)))
	}
}

// agentAliasKind names the marker mapping a subagent's addressable name to its id, since a
// continue may address it by either and the record is filed under the id.
func agentAliasKind(name string) hint.MarkerKind {
	return agentMarkerKind("agent-alias-", name)
}

// resolveAgentID is the id an address names: the id a recorded name maps to, else the
// address itself, which is then either an id or a name whose spawn magus never saw finish.
// Every per-agent record is keyed on the result, so a name and an id stay one agent.
func resolveAgentID(facts hint.Gate, addressed string) string {
	if addressed == "" || facts.CacheDir() == "" {
		return addressed
	}
	id, err := os.ReadFile(hint.MarkerPath(facts.CacheDir(), facts.Session(), agentAliasKind(addressed)))
	if err != nil || len(id) == 0 {
		return addressed
	}
	return string(id)
}

// spawnedAgent is what one subagent was spawned as, recorded under the id its host gave it.
type spawnedAgent struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name,omitempty"`
	// Model is the model the spawn named, "" when it named none.
	Model string `json:"model,omitempty"`
	// Job is the live job the spawn's title named, "" when it named none.
	Job string `json:"job,omitempty"`
	// ContextTokens is the agent's last observed context size, nil until its host
	// reports usage for it.
	ContextTokens *int64 `json:"context_tokens,omitempty"`
}

// recordSpawnedAgent files what a finished spawn call handed its child under the child's
// id, maps its name to that id, and starts its idle clock.
//
// A title of the form `<parent>/<role> <job>` naming a live job attributes the child to
// that job, and every later call carrying its id is graded under that job's lease. When
// the job never reported a base and the child shares this checkout, this checkout's base
// is recorded for it as `magus job exec` would, so its first write is not refused for a
// missing exec. An isolated child's checkout is one magus cannot see from here, so its
// base is left for the child to report.
//
// Best effort like every marker: a record that cannot be written leaves the child's calls
// with an empty parent and no job, the answer a host with no subagent identity gets.
func recordSpawnedAgent(ctx context.Context, deps Dependencies, at location, facts hint.Gate, env hookRequest) {
	facts.Touch(agentSeenKind(env.SpawnedAgent))
	if env.Spawn.Name != "" {
		writeAgentMarker(facts, agentAliasKind(env.Spawn.Name), []byte(env.SpawnedAgent))
	}
	// Usage is kept rather than replaced: a spawn that runs in the foreground returns after
	// its child stopped, so the child's usage can already be on file.
	prev, _ := readSpawnedAgent(facts, env.SpawnedAgent)
	rec := spawnedAgent{
		Description:   env.Spawn.Description,
		Name:          env.Spawn.Name,
		Model:         env.DeclaredModel,
		ContextTokens: prev.ContextTokens,
	}
	if row, ok := spawnTitleJob(ctx, at, env.Spawn.Description); ok {
		rec.Job = row.ID
		if row.Registered == 0 && !env.Spawn.Isolated {
			if base := deps.checkoutBase(ctx, at.workspace); base != "" {
				_, _ = job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}).Exec(ctx, row.ID, base)
			}
		}
	}
	writeSpawnedAgent(facts, env.SpawnedAgent, rec)
}

// spawnTitleJob is the live job a spawn title names in the `<parent>/<role> <job>` form.
// A title in any other form, or naming a job the store does not hold live, names none.
func spawnTitleJob(ctx context.Context, at location, title string) (types.Job, bool) {
	fields := strings.Fields(title)
	if len(fields) != 2 || at.cacheDir == "" {
		return types.Job{}, false
	}
	slash := strings.LastIndex(fields[0], "/")
	if slash <= 0 || slash == len(fields[0])-1 || !types.ValidJobID(fields[1]) {
		return types.Job{}, false
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return types.Job{}, false
	}
	for _, row := range rows {
		if row.ID == fields[1] && row.State.Live() {
			return row, true
		}
	}
	return types.Job{}, false
}

// actingLeaseFor is the lease a call acts under: an explicit --lease, then the job the
// calling subagent was spawned for, then the session's binding in this checkout.
//
// The subagent's job outranks the session's marker because a host that reports subagents
// hands them their parent's session id, so the marker cannot tell them apart and the
// agent id can. The marker is keyed on the session, so several sessions sharing one
// checkout each resolve their own; a host that reports none reads the checkout-wide one.
func actingLeaseFor(who hookAttribution, at location, facts hint.Gate, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if who.Agent != "" {
		if rec, ok := readSpawnedAgent(facts, who.Agent); ok && rec.Job != "" {
			return rec.Job
		}
	}
	return job.Checkout{CacheDir: at.cacheDir, Session: who.Session}.ActingLease()
}

// continueTarget is what magus recorded about the agent a continue addresses, by its id
// or by its name.
func continueTarget(facts hint.Gate, addressed string, now time.Time) *types.SpawnTarget {
	id := resolveAgentID(facts, addressed)
	target := &types.SpawnTarget{Agent: addressed, IdleMs: agentIdle(facts, id, now)}
	if rec, ok := readSpawnedAgent(facts, id); ok {
		target.Description, target.Model, target.ContextTokens = rec.Description, rec.Model, rec.ContextTokens
	}
	return target
}

// spawnedAs is the label the calling subagent was itself spawned with: its description,
// else its name. "" for a root session, for a host that reports no subagent identity, and
// for a subagent whose spawn magus never saw finish.
func spawnedAs(facts hint.Gate, agentID string) string {
	rec, _ := readSpawnedAgent(facts, agentID)
	return cmp.Or(rec.Description, rec.Name)
}

// recordAgentUsage files a subagent's last observed context size beside its spawn record.
// A transcript with no usage record changes nothing, so a size is never guessed.
func recordAgentUsage(facts hint.Gate, agentID, transcript string) {
	if agentID == "" {
		return
	}
	tokens, ok := lastContextTokens(transcript)
	if !ok {
		return
	}
	rec, _ := readSpawnedAgent(facts, agentID)
	rec.ContextTokens = &tokens
	writeSpawnedAgent(facts, agentID, rec)
}

// agentUsageTail bounds how much of a subagent transcript is read. The file grows every
// turn and only its last usage record is wanted.
const agentUsageTail = 512 << 10

// transcriptUsage is the one shape read from a transcript line: a message's token usage.
type transcriptUsage struct {
	Message struct {
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// lastContextTokens reads the latest usage record in the tail of a JSONL transcript and
// returns its input plus cache-read plus cache-write tokens: what the model was handed
// on its last call, which is the context a resume would have to rebuild.
func lastContextTokens(path string) (int64, bool) {
	if !filepath.IsAbs(path) {
		return 0, false
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	start := max(info.Size()-agentUsageTail, 0)
	buf := make([]byte, info.Size()-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, false
	}
	// A short read must not feed the unwritten tail of buf to json.Unmarshal.
	lines := bytes.Split(buf[:n], []byte("\n"))
	if start > 0 {
		// The tail cut the first line, and half a record is not one.
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var rec transcriptUsage
		if json.Unmarshal(line, &rec) != nil || rec.Message.Usage == nil {
			continue
		}
		u := rec.Message.Usage
		return u.Input + u.CacheRead + u.CacheWrite, true
	}
	return 0, false
}

func readSpawnedAgent(facts hint.Gate, agentID string) (spawnedAgent, bool) {
	if agentID == "" || facts.CacheDir() == "" {
		return spawnedAgent{}, false
	}
	body, err := os.ReadFile(hint.MarkerPath(facts.CacheDir(), facts.Session(), spawnedAgentKind(agentID)))
	if err != nil {
		return spawnedAgent{}, false
	}
	var rec spawnedAgent
	if json.Unmarshal(body, &rec) != nil {
		return spawnedAgent{}, false
	}
	return rec, true
}

func writeSpawnedAgent(facts hint.Gate, agentID string, rec spawnedAgent) {
	body, err := json.Marshal(rec)
	if err != nil {
		return
	}
	writeAgentMarker(facts, spawnedAgentKind(agentID), body)
}

// writeAgentMarker writes one agent record, best effort. Through a temporary file, since
// a subagent's own calls read the record while its parent's hooks may be writing it.
func writeAgentMarker(facts hint.Gate, kind hint.MarkerKind, body []byte) {
	if facts.CacheDir() == "" {
		return
	}
	path := hint.MarkerPath(facts.CacheDir(), facts.Session(), kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(body)
	_ = tmp.Chmod(0o644)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// decidedBy names which side produced a command verdict. A workspace shell rule is the
// working tree's: it has no approved twin to be stricter than.
func decidedBy(v Verdict) string {
	switch {
	case v.Decision == "pass" || v.Decision == "":
		return ""
	case strings.HasPrefix(v.Rule, workspaceShellPrefix):
		return decidedByWorktree
	}
	return decidedByBuiltin
}
