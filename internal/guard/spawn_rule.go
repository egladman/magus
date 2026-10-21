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
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/types"
)

// workspaceSpawnRule is the GuardJudgment.Rule an unnamed magus\guard.spawn answer carries, in the
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
// response, "" when the response names none. The key is the host's and magus reads it by
// shape, never by host name: `agentId`, `agent_id`, or `task_name`, the path a host that
// addresses children by name hands back in place of an id.
//
// A response may be the object itself or a string holding its JSON; a string that is not
// JSON names nothing.
func spawnedAgentID(response any) string {
	if s, ok := response.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(s), &decoded) != nil {
			return ""
		}
		response = decoded
	}
	m, ok := response.(map[string]any)
	if !ok {
		return ""
	}
	return cmp.Or(envelopeString(m, "agentId"), envelopeString(m, "agent_id"), envelopeString(m, "task_name"))
}

// judgeAgentEvent answers a spawn or a continuation of a subagent.
//
// Nothing here judges the prompt: it is prose, and a prompt that merely mentions a denied
// command would otherwise block the spawn describing it. The built-in rules read session
// state; the workspace's magus\guard.spawn rule then reads the normalized request and may
// only add to what they said.
func judgeAgentEvent(ctx context.Context, deps Dependencies, req Request, env hookRequest, who hookAttribution) types.GuardJudgment {
	at := hookLocation(ctx, deps)
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	// A spawn call that has already run judges nothing, and judging it again would spend
	// the session's markers twice. What it carries is the id the host gave the child,
	// which is what lets that child's own later calls name their parent and their job.
	if env.AfterCall {
		if env.SpawnedAgent != "" {
			spawner, _ := resolveActingLease(who, at, req.Lease)
			recordSpawnedAgent(ctx, at, facts, who, env, spawner)
		}
		return types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionPass}
	}
	digest := recordPolicy(ctx, deps, at, true)
	ctx = withJobStoreRows(ctx, at)
	ctx = withOutputStore(ctx, at)
	titled := spawnTitle(sync.OnceValues(func() (types.Job, bool) {
		return spawnTitleJob(ctx, at, env.Spawn.Description)
	}))

	verdict := types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionPass}
	decided := ""
	var failures []trail.RuleFailure
	// The first built-in deny the workspace demoted, spoken once both built-ins have had
	// their turn.
	var held heldAdvice
	// why is the rationale of the built-in deny standing, kept behind the deny's ref.
	why := ""
	verdictRef := ""
	if env.IsSpawn {
		verdict, verdictRef = spawnBuiltIns(ctx, deps, req, env, who, at)
		if verdict.Decision == decisionDeny {
			if _, refused := held.hold(deps, ShellVerdict{Deny: verdict.Reason, Rule: denyRule{Name: denyRuleName(verdict.Rule)}}); !refused {
				verdict = types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionPass}
			}
		}
		if verdict = deps.gradeAdvice(verdict); verdict.Decision != decisionPass {
			decided = decidedByBuiltin
		}
	}
	// A continuation carries a brief as much as a spawn does, so both are graded.
	if verdict.Decision != decisionDeny {
		deps.scope = scopeAt(at)
		brief := briefInput{text: env.Value, title: env.Spawn.Description, isolated: env.IsSpawn && spawnIsolated(env, titled), bound: continuesBoundWorker(ctx, at, facts, env)}
		if v, refused := held.hold(deps, judgeBriefCommand(deps, brief)); refused {
			verdict = types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionDeny, Reason: v.Deny, Rule: v.RuleName()}
			why, decided = v.Why, decidedByBuiltin
		} else if v.Context != "" && verdict.Decision == decisionPass {
			verdict = types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionAdvise, Context: v.Context, Rule: v.RuleName()}
			decided = decidedByBuiltin
		}
	}
	if held.v.demoted {
		gate, whys := hint.NewGate(at.cacheDir, who.callerKey()), map[string]string{}
		held.speak(gate, &verdict, whys)
		if verdict.Decision == decisionAdvise {
			verdict.Context, verdictRef = shapeAdvice(ctx, gate, verdict.Rule, verdict.Context, whys, false)
		}
		if verdict.Decision != decisionPass {
			decided = decidedByBuiltin
		}
	}
	actingLease, _ := resolveActingLease(who, at, req.Lease)
	if verdict.Decision != decisionDeny {
		verdict = gradeMainSpawn(deps, verdict, env, who, at, actingLease, titled)
		verdict = gradeSpawnCeiling(deps, verdict, env, who, at, actingLease)
		if verdict.Decision != decisionPass && decided == "" {
			decided = decidedByBuiltin
		}
	}
	// Strengthen only, and so asked only when there is something left to strengthen: a
	// workspace allow can never lift a built-in deny.
	if verdict.Decision != decisionDeny {
		spawnReq := spawnRequest(ctx, env, who, at, facts, req.Lease, titled)
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
		asked := askWorkspaceRules(ctx, seamSpawn, deps, resolve, bind(deps.SpawnRule))
		failures = asked.failures
		// A continuation hands a running child more work; only a spawn creates one the rule
		// exists to bind.
		call := judgedCall{seam: seamSpawn, what: "a subagent spawn", lease: actingLease}
		if env.IsSpawn {
			call.verb, call.changes = call.what, true
		}
		denied := asked.denyShort(func() judgedCall { return call }, at)
		verdict, decided = applyWorkspaceAnswer(verdict, decided, asked, workspaceSpawnRule)
		if !denied {
			note := ruleFailureNote(ruleFailureReport{
				gate: hint.NewGate(at.cacheDir, who.callerKey()), seam: seamSpawn, failures: failures, answered: asked.answered, toldInFull: asked.lapse != nil,
				judging: func() string { return judgingBinaryNote(deps, at, asked.loadFailure, actingLease) },
				lead:    loadFailureLead(deps.LoadFailure, at.workspace),
			})
			verdict = applyRuleFailureNote(verdict, note, advisorySpawnRuleFailed)
		}
	}
	// Worded as the command and write seams word theirs: the verdict, at most one next and
	// the ref holding the rest. A rule the catalog does not list keeps its whole reason.
	if verdict.Decision == decisionDeny && verdict.Rule != "" {
		verdict.Reason, verdictRef, verdict.Next = shapeDeny(ctx, hint.NewGate(at.cacheDir, who.callerKey()), verdict.Rule, verdict.Reason, why, "", verdict.Next, false)
	}
	appendHookSpawn(ctx, deps, env, who, spawnVerdictRecord{
		verdictRef:   verdictRef,
		policyDigest: digest,
		decidedBy:    decided,
		target:       resolveAgentID(facts, env.Target),
		ruleFailures: failures,
	})
	if verdict.Decision != decisionDeny {
		if env.IsContinue {
			recordRework(ctx, at, facts, env.Target)
		}
		markAgentSeen(facts, env)
		bound := false
		if env.IsSpawn {
			_, bound = titled()
		}
		noteMainSpawn(at, who, actingLease, bound)
		if env.IsSpawn {
			recordPendingSpawn(facts, env, actingLease, time.Now())
		}
	}
	return verdict
}

// spawnBuiltIns is the compiled half of a spawn's verdict, which reads session state and
// never the prompt.
func spawnBuiltIns(ctx context.Context, deps Dependencies, req Request, env hookRequest, who hookAttribution, at location) (types.GuardJudgment, string) {
	refused := func(rule denyRuleName, d denial) types.GuardJudgment {
		return types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionDeny, Reason: d.Say, Rule: string(rule)}
	}
	// Whether the multi-agent brief was read before work was handed out: a marker file,
	// no prose, which is what lets it live on this path.
	if reason := denySpawnWithoutBrief(hint.NewGate(at.cacheDir, who.skillsKey()), req.ObservesSkillLoads, at.workspace); reason != "" {
		return refused(denySpawnUnbriefed, denial{Say: reason}), ""
	}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	if row, ok := spawnDuplicate(ctx, at, facts, env.Spawn.Description); ok {
		return refused(denyRuleSpawnDuplicate, denySpawnDuplicate(row)), ""
	}
	if row, ok := spawnTitleJob(ctx, at, env.Spawn.Description); ok && row.Validated == 0 {
		return refused(denyRuleSpawnUnvalidated, denySpawnUnvalidated(row)), ""
	}
	if spawnWaitsFires(env.Spawn.Description, env.Value) {
		return refused(denyRuleSpawnWaiter, denySpawnWaiter()), ""
	}
	// Whether this checkout is already somebody's, asked the same way and for the same reason.
	gate := hint.NewGate(at.cacheDir, who.callerKey())
	if note := adviseSharedCheckoutSpawn(ctx, gate, at); note.Say != "" {
		shown, ref := shapeAdvice(ctx, gate, string(advisorySharedCheckout), note.Say, map[string]string{note.Say: note.Why}, false)
		return types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionAdvise, Context: shown, Rule: string(advisorySharedCheckout)}, ref
	}
	// A subagent handing waiting to a helper of its own, said once a session.
	if who.Agent != "" && spawnNestedWordRe.MatchString(env.Spawn.Description+"\n"+env.Value[:min(len(env.Value), spawnWaiterHead)]) {
		note := adviseSpawnNestedWaiter()
		if said := deps.heldBy(gate, string(advisorySpawnNestedWaiter)).Once(advisorySpawnNestedWaiter, note.Say); said != "" {
			shown, ref := shapeAdvice(ctx, gate, string(advisorySpawnNestedWaiter), said, map[string]string{note.Say: note.Why}, false)
			return types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionAdvise, Context: shown, Rule: string(advisorySpawnNestedWaiter)}, ref
		}
	}
	// The orchestrator waits for the build budget so its workers never do; a subagent
	// spawning is a worker, which reports a full budget rather than waiting on it.
	if who.Agent == "" {
		if note := adviseBudgetFull(ctx, time.Now()); note.Say != "" {
			shown, ref := shapeAdvice(ctx, gate, string(denyRuleSleepPoll), note.Say, map[string]string{note.Say: note.Why}, false)
			return types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionAdvise, Context: shown, Rule: string(denyRuleSleepPoll)}, ref
		}
	}
	return types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionPass}, ""
}

// spawnIsolated reports whether the child runs outside this checkout: the spawn asked for
// the host's isolation, or the row its title names owns a checkout of its own, where the
// worker is taken.
func spawnIsolated(env hookRequest, titled spawnTitle) bool {
	if env.Spawn.Isolated {
		return true
	}
	row, ok := titled()
	return ok && row.Worktree.Standing()
}

// spawnRequest normalizes one spawn or continuation for the workspace rule. Every field is
// read from the envelope or from what magus recorded; none is inferred from the host.
func spawnRequest(ctx context.Context, env hookRequest, who hookAttribution, at location, facts hint.Gate, explicitLease string, titled spawnTitle) types.SpawnRequest {
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
		Isolated:    spawnIsolated(env, titled),
		Agent:       who.Agent,
		Parent:      spawnedAs(facts, who.Agent),
	}
	if env.IsContinue {
		req.Kind = types.SpawnKindContinue
		req.Target = continueTarget(ctx, at, facts, env.Target, time.Now())
	} else if row, ok := titled(); ok {
		req.ModelTier = row.ModelTier
	}
	// The same resolution every lease-scoped rule uses, so the rule and the guard cannot
	// disagree about who is acting.
	lease, _ := resolveActingLease(who, at, explicitLease)
	req.Role, req.Lease = actingRole(ctx, at, lease, req.Parent)
	req.Thread, req.ThreadFrom, req.Window = callerCost(at, who, lease, facts)
	req.Orchestration = callerOrchestration(at, who, req.Thread, req.ThreadFrom, lease)
	return req
}

// agentSeenKind names the marker holding when magus last saw agent in a session. Hashed
// because an agent name is host-chosen text and a marker kind is a filename component.
func agentSeenKind(agentID string) hint.MarkerKind {
	return agentMarkerKind("agent-seen-", agentID)
}

// spawnedAgentPrefix starts the kind of every spawn record.
const spawnedAgentPrefix hint.MarkerKind = "agent-spawned-"

// spawnedAgentKind names the marker recording what a subagent was spawned as.
func spawnedAgentKind(agentID string) hint.MarkerKind {
	return agentMarkerKind(string(spawnedAgentPrefix), agentID)
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
	// UntrustedJob is a live job the title named that the spawner could not hand out: the
	// spawner acted under a lease, and the job is neither that lease nor forked beneath
	// it. Kept so a reader can see the claim; it attributes nothing.
	UntrustedJob string `json:"untrusted_job,omitempty"`
	// ContextTokens is the agent's last observed context size, nil until its host
	// reports usage for it.
	ContextTokens *int64 `json:"context_tokens,omitempty"`
	// ContextWindow is the largest context the agent's host says it can hold, 0 until a
	// host reports one alongside its usage.
	ContextWindow int64 `json:"context_window,omitempty"`
}

// recordSpawnedAgent files what a finished spawn call handed its child under the child's
// id, maps its name to that id, and starts its idle clock.
//
// A title of the form `<parent>/<role> <job>` naming a live job attributes the child to
// that job through [job.Store.Bind], and every later call carrying its id is graded
// under that job's lease from whichever checkout it runs in. The job's base is recorded
// by the child's first call, in the checkout it landed in (see registerAgentBase).
//
// The title is the spawner's claim, and any process can pipe a spawn envelope into
// `magus shell`, so it attributes only what the spawner could hand out: an unleased
// spawner (the orchestrator or a person) any live job, a leased one only its own lease or
// a job forked beneath it. Anything else is recorded as UntrustedJob and attributes
// nothing, or a worker could name another job in a title and be graded under it.
//
// Best effort like every marker: a record that cannot be written leaves the child's calls
// with an empty parent and no job, the answer a host with no subagent identity gets.
func recordSpawnedAgent(ctx context.Context, at location, facts hint.Gate, who hookAttribution, env hookRequest, spawner string) {
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
		ContextWindow: prev.ContextWindow,
	}
	if row, ok := spawnTitleJob(ctx, at, env.Spawn.Description); ok {
		rows, err := leaseRows(ctx, at)
		if err != nil || !mayHandOut(rows, spawner, row.ID) {
			rec.UntrustedJob = row.ID
			writeSpawnedAgent(facts, env.SpawnedAgent, rec)
			return
		}
		child := job.Caller{Host: who.Host, Session: who.Session, Agent: env.SpawnedAgent}
		_ = job.NewStore(at.jobs()).Bind(child, row.ID)
	}
	writeSpawnedAgent(facts, env.SpawnedAgent, rec)
}

// mayHandOut reports whether a spawner acting under lease spawner may attribute a child to
// job: always when it acts under none, else when job is spawner's own row or one whose
// parent chain reaches it.
func mayHandOut(rows []types.Job, spawner, job string) bool {
	if spawner == "" {
		return true
	}
	parent := make(map[string]string, len(rows))
	for _, row := range rows {
		parent[row.ID] = row.Parent
	}
	for at, seen := job, map[string]bool{}; at != "" && !seen[at]; at = parent[at] {
		if at == spawner {
			return true
		}
		seen[at] = true
	}
	return false
}

// spawnTitleJob is the live job a spawn title names in the `<parent>/<role> <job>` form:
// the row `<job>`, else the row `<parent>/<job>`, since a job forked beneath its parent
// carries the parent in its id. A title in any other form, or naming a job the store does
// not hold live, names none.
func spawnTitleJob(ctx context.Context, at location, title string) (types.Job, bool) {
	if at.cacheDir == "" {
		return types.Job{}, false
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return types.Job{}, false
	}
	live := slices.DeleteFunc(slices.Clone(rows), func(row types.Job) bool { return !row.State.Live() })
	return spawnTitleRow(live, title)
}

// spawnTitle is spawnTitleJob for one spawn, asked once however many rules read it.
type spawnTitle func() (types.Job, bool)

// spawnTitleRow is the row of rows a spawn title in the `<parent>/<role> <job>` form names,
// tried as spawnTitleJob tries them, whatever the row's state.
func spawnTitleRow(rows []types.Job, title string) (types.Job, bool) {
	fields := strings.Fields(title)
	if len(fields) != 2 {
		return types.Job{}, false
	}
	slash := strings.LastIndex(fields[0], "/")
	if slash <= 0 || slash == len(fields[0])-1 || !types.ValidJobID(fields[1]) {
		return types.Job{}, false
	}
	for _, id := range []string{fields[1], fields[0][:slash] + "/" + fields[1]} {
		if i := slices.IndexFunc(rows, func(row types.Job) bool { return row.ID == id }); i >= 0 {
			return rows[i], true
		}
	}
	return types.Job{}, false
}

// spawnedJob reports whether one of the spawn records in facts attributed a worker to job:
// its title names that row of rows, and its spawner could hand the job out.
func spawnedJob(facts hint.Gate, rows []types.Job, job string) bool {
	for _, kind := range facts.Kinds(spawnedAgentPrefix) {
		rec, ok := readMarker[spawnedAgent](facts, kind)
		if !ok || rec.UntrustedJob != "" {
			continue
		}
		if row, ok := spawnTitleRow(rows, rec.Description); ok && row.ID == job {
			return true
		}
	}
	return false
}

// spawnedAsJob reports whether the spawn record filed under agentID attributed that agent to
// job, as spawnedJob does for any agent of the session.
func spawnedAsJob(facts hint.Gate, rows []types.Job, agentID, job string) bool {
	rec, ok := readMarker[spawnedAgent](facts, spawnedAgentKind(agentID))
	if !ok || rec.UntrustedJob != "" {
		return false
	}
	row, ok := spawnTitleRow(rows, rec.Description)
	return ok && row.ID == job
}

// caller is who this call comes from, as the job store keys a binding.
func (who hookAttribution) caller() job.Caller {
	return job.Caller{Host: who.Host, Session: who.Session, Agent: who.Agent}
}

// boundJob is the caller's own record, tombstone included: for an identified caller the
// one keyed on exactly its host, session and agent, and for a caller with neither id this
// checkout's. Zero when no record answers.
func boundJob(who hookAttribution, at location) job.Binding {
	if who.caller().Identified() && at.workspace == "" {
		return job.Binding{}
	}
	return job.NewStore(at.jobs()).Binding(who.caller())
}

// resolveActingLease is the lease a call acts under. See [resolveLease].
func resolveActingLease(who hookAttribution, at location, explicit string) (string, types.LeaseSource) {
	return resolveLease(who, explicit, boundJob(who, at).Job)
}

// resolveLease resolves by job.LeaseQuery with the answer only the guard holds: the
// caller's record, bound. A tombstone's job answers too, so the verdict names the job the
// binding ended on rather than reading as unbound.
//
// Exact, with no fallback between keys. A host that reports subagents hands them their
// parent's session id, so a subagent that read its session's record would be graded as
// its parent, and a parent that read the checkout's would be graded as whichever
// identity-less caller took a job here last.
func resolveLease(who hookAttribution, explicit, bound string) (string, types.LeaseSource) {
	q := job.LeaseQuery{Flag: explicit, Claim: trail.LeaseFromEnv()}
	if who.caller().Identified() {
		q.CallerJob = bound
	} else {
		q.CheckoutJob = bound
	}
	return q.Resolve()
}

// execTarget is the job a command line takes, through `magus job exec <job>` or a client
// script's magus\job.register, or "" when it takes none.
func execTarget(command string) string {
	cmds, ok := ParseCommands(command)
	if !ok {
		return ""
	}
	for _, c := range cmds {
		if c.Name == hint.ToolClient.String() {
			if params := mcpParams(c.Args); params["op"] == jobOpRegister {
				return strings.TrimSpace(params["id"])
			}
			continue
		}
		if path.Base(c.Name) != "magus" || magusFlag(c.Args, "h") || magusFlag(c.Args, "help") {
			continue
		}
		if hint.JobExec.MatchedBy(magusSubcommandWords(c.Args)) {
			return execOperand(c.Args)
		}
	}
	return ""
}

// execOperand is the job a `magus job exec` argv names: its first word after the verb,
// with the value of every flag that takes one skipped, the verb's own `--base` included.
// magusSubcommandWords knows only the global flags, so it reads `--base <rev>`'s value as
// the job.
func execOperand(args []string) string {
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if len(a) < 2 || a[0] != '-' {
			words = append(words, a)
			continue
		}
		name, _, joined := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !joined && execFlagTakesValue(name) {
			i++
		}
	}
	if len(words) < 3 {
		return ""
	}
	return words[2]
}

// execFlagTakesValue reports whether name is a global flag or a `magus job exec` flag that
// takes a value, read from the registry the CLI parses with.
func execFlagTakesValue(name string) bool {
	if takes, known := MagusFlagTakesValue(name); known {
		return takes
	}
	for _, group := range commands.Root.Children {
		if group.Name != hint.JobExec.Head() {
			continue
		}
		for _, verb := range group.Children {
			if verb.Name != hint.JobExec.Leaf() {
				continue
			}
			i := slices.IndexFunc(verb.Flags, func(f cmdline.Flag) bool { return f.Name == name })
			return i >= 0 && verb.Flags[i].Kind != cmdline.Bool
		}
	}
	return false
}

// bindOnExec records that the caller acts under the job command takes, when it takes a
// live one its record does not already name. It runs only on a command the guard lets
// through, and is the one place a binding is made outside a spawn.
//
// The GUARD records it, not `magus job exec`: a binding is a fact about who is acting, and
// only the hook reads the host's session and agent ids. The CLI knows neither, so every
// binding it wrote was keyed on the checkout, and one subagent's exec there graded its
// parent as that subagent from the parent's next call.
//
// Whether the caller may take the job is decided before this runs: denyLeaseScopedRebind
// refuses another job while the one it holds is still declared or running.
func bindOnExec(ctx context.Context, at location, who hookAttribution, command string) {
	id := execTarget(command)
	if !types.ValidJobID(id) {
		return
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return
	}
	i := slices.IndexFunc(rows, func(row types.Job) bool { return row.ID == id })
	if i < 0 || !rows[i].State.Live() {
		return
	}
	if who.caller().Identified() && at.workspace == "" {
		return
	}
	store := job.NewStore(at.jobs())
	if store.Bound(who.caller()) != id {
		_ = store.Bind(who.caller(), id)
	}
}

// releaseOnEnd unbinds the caller from each job command ends, through `magus job exit`,
// `job wait` or `job rm`, that its record names. Like bindOnExec it runs only on a command
// the guard lets through.
//
// Released before the command runs, as bindOnExec binds: the hook sees no result. A
// command that then fails leaves the caller unbound, which the orchestrator it now reads
// as can undo with one `magus job exec`.
func releaseOnEnd(at location, who hookAttribution, command string) {
	if who.caller().Identified() && at.workspace == "" {
		return
	}
	cmds, ok := ParseCommands(command)
	if !ok {
		return
	}
	var store *job.Store
	for _, c := range cmds {
		if path.Base(c.Name) != "magus" || magusFlag(c.Args, "h") || magusFlag(c.Args, "help") {
			continue
		}
		words := magusSubcommandWords(c.Args)
		for _, verb := range []hint.Command{hint.JobExit, hint.JobWait, hint.JobRm} {
			if !verb.MatchedBy(words) {
				continue
			}
			operands, _ := verbArgv(c.Args, verb)
			if len(operands) == 0 || !types.ValidJobID(operands[0]) {
				continue
			}
			if store == nil {
				store = job.NewStore(at.jobs())
			}
			store.Release(who.caller(), operands[0])
		}
	}
}

// bindingUnlessEnded is b, or no binding when b names a job that ended in a verdict (pass,
// fail or no_return), for the caller to release. A wait ends a job from its orchestrator's
// session, so releaseOnEnd never sees the holder's call; the holder's own next call
// releases it instead.
//
// An exited row still holds its holder, since a rejected wait sends it back to the same
// paths, until the caller's BAGGAGE claims another job still declared or running. The
// holder filed its result, so nothing of the exited job is left to grade, and keeping the
// binding graded a resumed worker under the job it had finished. A binding to a job in
// flight still outranks the claim: a worker cannot rewrite its own grading from a shell.
func bindingUnlessEnded(ctx context.Context, at location, b job.Binding) job.Binding {
	if b.Job == "" || b.Gone {
		return b
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return b
	}
	state := func(id string) types.JobState {
		if i := slices.IndexFunc(rows, func(row types.Job) bool { return row.ID == id }); i >= 0 {
			return rows[i].State
		}
		return ""
	}
	switch held := state(b.Job); {
	case held.Terminal():
		return job.Binding{}
	case held == types.StateExited:
		if claim := trail.LeaseFromEnv(); claim != b.Job && state(claim).Editing() {
			return job.Binding{}
		}
	}
	return b
}

// registerAgentBase records the base of the checkout a subagent's call runs in for the
// job it was spawned for, as `magus job exec` would, when that job has reported none or
// its recorded base is superseded (see [baseSuperseded]). Reports whether it wrote, so the
// caller re-reads the rows it graded against.
//
// Done by the child's own call, not at spawn: the host picks the child's checkout, and
// only the child's hooks run in it. Without it an attributed worker's first write is
// refused for a missing exec the worker was never going to run.
//
// A registration the job store refuses (another checkout holds the job) is not tried again
// until the row changes: every call would otherwise read the checkout's base again only to
// be refused the same way.
func registerAgentBase(ctx context.Context, deps Dependencies, at location, agentJob string) bool {
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return false
	}
	for _, row := range rows {
		if row.ID != agentJob {
			continue
		}
		if !row.State.Live() || (row.Registered != 0 && !baseSuperseded(ctx, deps, at, row)) {
			return false
		}
		refusals := hint.NewGate(at.cacheDir, agentBaseRefusals)
		refused := agentBaseRefusedKind(row, at.workspace)
		if refusals.AlreadyFired(refused) {
			return false
		}
		if _, err := job.NewStore(at.jobs()).Exec(ctx, agentJob); err != nil {
			// Only a holder conflict lasts until the row changes. A cancelled deadline, lock
			// contention, I/O or a checkout with no revision yet may pass on the next call, so
			// it is never remembered.
			if errors.Is(err, job.ErrHeldElsewhere) {
				refusals.MarkFired(refused)
			}
			return false
		}
		return true
	}
	return false
}

// agentBaseRefusals keys the registrations the job store refused, for every caller alike.
const agentBaseRefusals = "guard/agent-base"

// agentBaseRefusedKind names one refused registration: row as it stood, every field of it,
// from the checkout at workspace.
func agentBaseRefusedKind(row types.Job, workspace string) hint.MarkerKind {
	body, _ := json.Marshal(row)
	sum := sha256.Sum256(append(body, "\x00"+workspace...))
	return hint.MarkerKind("agent-base-refused-" + hex.EncodeToString(sum[:8]))
}

// baseSuperseded reports a registered row whose recorded base diverged from its checkpoint
// and whose checkout has since moved to another revision: an isolated worker registers on
// its first call, at whatever its worktree was created on, and only then switches to the
// branch it was handed.
//
// Only a diverged row is refreshed. Revisions are compared exactly, so re-registering a
// worker that committed on its checkpoint would read its own commit as a divergence.
// Revisions only, through the cheap seam: the dirty digest moves on every edit.
func baseSuperseded(ctx context.Context, deps Dependencies, at location, row types.Job) bool {
	if row.BaseVerdict != types.BaseDiverged {
		return false
	}
	live := deps.revision(ctx, at.workspace, "")
	reported, _ := types.ParseCheckpointToken(row.ReportedBase)
	return live != "" && !job.SameRevision(live, reported)
}

// continuesBoundWorker reports a continue addressed to an agent whose spawn title names a
// live row.
func continuesBoundWorker(ctx context.Context, at location, facts hint.Gate, env hookRequest) bool {
	if !env.IsContinue {
		return false
	}
	rec, ok := readSpawnedAgent(facts, resolveAgentID(facts, env.Target))
	if !ok {
		return false
	}
	_, bound := spawnTitleJob(ctx, at, rec.Description)
	return bound
}

// continueTarget is what magus recorded about the agent a continue addresses, by its id
// or by its name, with the entries on the job its title names.
func continueTarget(ctx context.Context, at location, facts hint.Gate, addressed string, now time.Time) *types.SpawnTarget {
	id := resolveAgentID(facts, addressed)
	target := &types.SpawnTarget{Agent: addressed, IdleMs: agentIdle(facts, id, now)}
	if rec, ok := readSpawnedAgent(facts, id); ok {
		target.Description, target.Model, target.ContextTokens = rec.Description, rec.Model, rec.ContextTokens
		if row, ok := spawnTitleJob(ctx, at, rec.Description); ok {
			target.Entries = row.Entries
		}
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
	usage, ok := lastContextUsage(transcript)
	if !ok {
		return
	}
	rec, _ := readSpawnedAgent(facts, agentID)
	rec.ContextTokens = &usage.Tokens
	if usage.Window > 0 {
		rec.ContextWindow = usage.Window
	}
	writeSpawnedAgent(facts, agentID, rec)
}

// agentUsageTail bounds how much of a subagent transcript is read. The file grows every
// turn and only its last usage record is wanted.
const agentUsageTail = 512 << 10

// transcriptUsage is the shapes read from a transcript line: a message's token usage, model
// and content, or the token_count event and function call items another host's rollout
// records.
type transcriptUsage struct {
	Message struct {
		Model string `json:"model"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	// IsSidechain marks a subagent's line written into its parent's transcript.
	IsSidechain bool   `json:"isSidechain"`
	Type        string `json:"type"`
	Payload     struct {
		Type string `json:"type"`
		// Info is null on a token_count event that reports only rate limits.
		Info *struct {
			Last *struct {
				Input int64 `json:"input_tokens"`
			} `json:"last_token_usage"`
			Window int64 `json:"model_context_window"`
		} `json:"info"`
		Name      string          `json:"name"`
		Arguments string          `json:"arguments"`
		CallID    string          `json:"call_id"`
		Output    json.RawMessage `json:"output"`
	} `json:"payload"`
}

// contextSize is what the model was handed on the line's call and the size of the window
// it was handed it in (0 when the host does not report one), or false for a line that
// reports no usage.
func (u transcriptUsage) contextSize() (tokens, window int64, ok bool) {
	if usage := u.Message.Usage; usage != nil {
		return usage.Input + usage.CacheRead + usage.CacheWrite, 0, true
	}
	if info := u.Payload.Info; u.Type == "event_msg" && u.Payload.Type == "token_count" && info != nil && info.Last != nil {
		// The count already includes the cached tokens, so nothing is added to it.
		return info.Last.Input, info.Window, true
	}
	return 0, 0, false
}

// lastContextUsage reads the latest usage record in the tail of a JSONL transcript. Its
// Tokens are input plus cache-read plus cache-write tokens: what the model was handed on
// its last call, which is the context a resume would have to rebuild. Its Window is the
// host's own figure for how large that context may grow, 0 when it names none. False when
// the tail holds no usage record.
func lastContextUsage(path string) (types.ContextWindow, bool) {
	if !filepath.IsAbs(path) {
		return types.ContextWindow{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return types.ContextWindow{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return types.ContextWindow{}, false
	}
	start := max(info.Size()-agentUsageTail, 0)
	buf := make([]byte, info.Size()-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return types.ContextWindow{}, false
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
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if tokens, window, ok := rec.contextSize(); ok {
			return types.ContextWindow{Tokens: tokens, Window: window, Source: types.ContextFromTranscript}, true
		}
	}
	return types.ContextWindow{}, false
}

func readSpawnedAgent(facts hint.Gate, agentID string) (spawnedAgent, bool) {
	if agentID == "" {
		return spawnedAgent{}, false
	}
	return readMarker[spawnedAgent](facts, spawnedAgentKind(agentID))
}

// readMarker decodes one marker of the session. A missing cache dir, an unreadable file
// and a corrupt body all read as absent, so a half-written marker never counts as a record.
func readMarker[T any](facts hint.Gate, kind hint.MarkerKind) (T, bool) {
	var rec T
	if facts.CacheDir() == "" {
		return rec, false
	}
	body, err := os.ReadFile(hint.MarkerPath(facts.CacheDir(), facts.Session(), kind))
	if err != nil || json.Unmarshal(body, &rec) != nil {
		var zero T
		return zero, false
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

// writeAgentMarker writes one agent record, best effort. Replaced whole, since a
// subagent's own calls read the record while its parent's hooks may be writing it.
func writeAgentMarker(facts hint.Gate, kind hint.MarkerKind, body []byte) {
	if facts.CacheDir() == "" {
		return
	}
	_ = file.ReplaceFile(hint.MarkerPath(facts.CacheDir(), facts.Session(), kind), body, 0o644)
}

// decidedBy names which side produced a command verdict. A workspace shell rule is the
// working tree's: it has no approved twin to be stricter than.
func decidedBy(v types.GuardJudgment) string {
	switch {
	case v.Decision == decisionPass || v.Decision == "":
		return ""
	case strings.HasPrefix(v.Rule, workspaceShellPrefix):
		return decidedByWorktree
	}
	return decidedByBuiltin
}
