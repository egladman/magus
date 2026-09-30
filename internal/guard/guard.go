// Package guard holds the agent guard: the rules that judge one shell command, or one
// file path an edit is about to write, and answer with a deny, an advisory, or a pass.
//
// It lives outside cmd/magus so the rules are reachable from the surfaces that have to
// agree with them (the CLI hook, the MCP door, the dogfood tests), rather than restated
// in each. Everything host-specific stays in the caller: this package never reads a flag,
// a host's tool vocabulary, or the display options a verdict is rendered with.
package guard

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Dependencies are the workspace facts the rules cannot resolve for themselves: they depend on the
// caller's loaded config and on plumbing that belongs to the CLI. Passed rather than
// discovered, so a rule can be graded against a fixture workspace and so the guard never
// grows a second config read of its own.
//
// A nil member is a caller that cannot answer, and every rule that needs it falls silent
// rather than guessing.
type Dependencies struct {
	// Inspect opens the workspace at root. An empty root means the one the hook process
	// runs in, which the caller may answer from a memoized load.
	Inspect func(ctx context.Context, root string) (types.WorkspaceRepository, error)
	// CacheDir resolves a workspace's cache directory. An empty root means the process
	// cwd's workspace, whose config the caller has already loaded; a named root reads its
	// own magus.yaml, because a cache dir configured in the orchestrator's tree says
	// nothing about where a worker's cache lives.
	CacheDir func(root string) (string, error)
	// NotesShared is the workspace's declared shared notes store, empty when it declares none.
	NotesShared string
	// ShellRules are workspace-declared additive shell rules from
	// magus\guard.shell. Empty means only the compiled built-ins apply. They
	// strengthen only: a built-in deny always wins; a workspace deny may
	// escalate a built-in advise or a pass; a workspace advise fills silence
	// only.
	ShellRules []WorkspaceShellRule
	// ShellDialect is the outer-parse dialect for Evaluate when a workspace
	// declared one on its shell rules. Empty means bash.
	ShellDialect Dialect
	// SpawnRule is the working tree's magus\guard.spawn rule, nil when none is
	// registered. It is called on every spawn and continuation and can only add to
	// the built-in verdict.
	SpawnRule workspace.SpawnRule
	// ApprovedSpawnRule resolves the same rule as the approved sources register it: nil
	// with no error when those sources are the working tree's or register none, and an
	// error when they could not be evaluated. Resolved lazily because it can load the
	// magusfile a second time, which only a spawn is worth. Nil when the workspace has no
	// approval authority wired.
	ApprovedSpawnRule func(ctx context.Context) (workspace.SpawnRule, error)
	// CommandRule is the working tree's magus\guard.command rule, nil when none is
	// registered. It is called on every shell command the built-in rules let through and
	// can only add to their verdict.
	CommandRule workspace.CommandRule
	// ApprovedCommandRule is ApprovedSpawnRule for the command rule, resolved per command.
	ApprovedCommandRule func(ctx context.Context) (workspace.CommandRule, error)
	// WriteRule is the working tree's magus\guard.write rule, nil when none is registered.
	// It is called on every file write the built-in rules let through.
	WriteRule workspace.WriteRule
	// ApprovedWriteRule is ApprovedSpawnRule for the write rule, resolved per write.
	ApprovedWriteRule func(ctx context.Context) (workspace.WriteRule, error)
	// CheckoutState reads the checkout holding dir for a command rule judging a push or a
	// commit, nil when its version control cannot report it.
	CheckoutState func(ctx context.Context, dir string) *types.CheckoutState
	// LoadFailure is why the working tree's workspace did not load, nil when it loaded or
	// there is none. SpawnRule, CommandRule and WriteRule are then nil because nothing could
	// be read, not because no rule is registered, and the failure is reported beside the
	// verdict.
	LoadFailure error
	// Policy describes the effective workspace rules for the lineage the trail keeps,
	// nil when the caller cannot say. See RecordPolicy.
	Policy func() PolicyState
	// GraphStaleAdvice is what to say to a graph read about to answer from an index older
	// than the tree, or "" when every built index is current.
	GraphStaleAdvice func(ctx context.Context) string
	// Spells is the spell catalog a rendered raw tool is matched against.
	//
	// Passed rather than read from the process registry, which a caller that never loads
	// a workspace leaves empty: a rule graded against an empty catalog refuses nothing
	// and says so to nobody. What is injected is the CATALOG, not a list of tools, so a
	// newly registered spell op still needs no guard edit.
	Spells func() []*spells.Spell
	// SymbolDefined reports whether ident is a symbol the workspace has indexed, and
	// whether that answer is DEFINITIVE. A stale index answers "unknown, not absent",
	// which is not proof of anything: the guard may only deny a search when it can
	// show the replacement returns the same sites.
	SymbolDefined func(ident string) (defined, definitive bool)
	// SymbolSites lists each file defining or referencing ident, with its count and first
	// lines, from SymbolDefined's index and definitive on the same terms.
	SymbolSites func(ident string) (sites []types.KnowledgeRefSite, definitive bool)
	// Revision is the revision rev names in the checkout holding dir, abbreviated, or ""
	// when there is no VCS to ask or rev names nothing. Empty rev is the checkout's current
	// revision; empty dir is the process's working directory. The push gate matches it
	// against the commit each recorded gate run was built from; with no answer that rule
	// stands down rather than refusing on an absence.
	Revision func(ctx context.Context, dir, rev string) string
	// GraphIDs lists the ids of every knowledge-graph node of kind, from the graph
	// `magus query` would answer from. definitive is false when the graph could not be
	// loaded, which proves nothing. It can build the graph, so a rule calls it only for a
	// command it has already found a translation candidate.
	GraphIDs func(ctx context.Context, kind string) (ids []string, definitive bool)
	// CheckoutBase is the checkout at root as `magus vcs checkpoint -o name` prints it:
	// `<rev>`, or `<rev>+<digest>` when dirty. "" when there is no VCS to ask. It is the
	// base an attributed spawn records for its job, the value `magus job exec` records.
	CheckoutBase func(ctx context.Context, root string) string
	// VCS is the workspace's version-control configuration. The worktree rule reads its
	// base branch, and refuses when it disables version control.
	VCS types.VCSOptions

	// scope is where the judged call runs. Judge fills it from the location it resolved,
	// so Evaluate can tell a path outside the workspace without reading anything itself.
	scope workspaceScope
	// callDir is the directory the judged call runs in, where its relative paths resolve.
	// Judge fills it from the envelope's cwd; empty means the hook process's own.
	callDir string
}

// workingDir is where a relative path on the judged line resolves. The hook process's cwd
// is only the fallback: a host runs its hooks from wherever it likes.
func (d Dependencies) workingDir() (string, bool) {
	if d.callDir != "" {
		return d.callDir, true
	}
	wd, err := os.Getwd()
	return wd, err == nil
}

// errNoDependency is what an unset Dependencies member answers with, so a rule takes the same silent
// path it takes for a workspace it could not open.
var errNoDependency = errors.New("guard: the caller supplied no resolver for this fact")

func (d Dependencies) inspect(ctx context.Context, root string) (types.WorkspaceRepository, error) {
	if d.Inspect == nil {
		return nil, errNoDependency
	}
	return d.Inspect(ctx, root)
}

func (d Dependencies) cacheDir(root string) (string, error) {
	if d.CacheDir == nil {
		return "", errNoDependency
	}
	return d.CacheDir(root)
}

func (d Dependencies) graphStaleAdvice(ctx context.Context) string {
	if d.GraphStaleAdvice == nil {
		return ""
	}
	return d.GraphStaleAdvice(ctx)
}

func (d Dependencies) revision(ctx context.Context, dir, rev string) string {
	if d.Revision == nil {
		return ""
	}
	return d.Revision(ctx, dir, rev)
}

func (d Dependencies) checkoutBase(ctx context.Context, root string) string {
	if d.CheckoutBase == nil || root == "" {
		return ""
	}
	return d.CheckoutBase(ctx, root)
}

func (d Dependencies) spells() []*spells.Spell {
	if d.Spells == nil {
		return nil
	}
	return d.Spells()
}

// symbolDefined answers false for an unset resolver, so a caller that supplies none
// keeps the advisory it had rather than gaining a deny nothing can substantiate.
func (d Dependencies) symbolDefined(ident string) (defined, definitive bool) {
	if d.SymbolDefined == nil {
		return false, false
	}
	return d.SymbolDefined(ident)
}

// symbolSites answers not-definitive for an unset resolver, like symbolDefined.
func (d Dependencies) symbolSites(ident string) ([]types.KnowledgeRefSite, bool) {
	if d.SymbolSites == nil {
		return nil, false
	}
	return d.SymbolSites(ident)
}

// graphIDs answers not-definitive for an unset resolver, so a caller that supplies none
// never gains a deny.
func (d Dependencies) graphIDs(ctx context.Context, kind string) ([]string, bool) {
	if d.GraphIDs == nil {
		return nil, false
	}
	return d.GraphIDs(ctx, kind)
}

// Request is one call the guard was asked to judge: the payload, plus what the caller's
// own flags said about it. A host envelope arriving as Input still answers the same
// questions, and an explicit flag wins over what the envelope implies.
type Request struct {
	Input  string
	IsPath bool
	// Observe records the input as a path the agent REACHED and judges nothing.
	Observe bool
	// DryRun reaches the verdict the call would and writes nothing: the session state the
	// rules spend is read from a discarded copy, and no trail line, policy record, binding
	// or registration is made. One difference in wording: a repeated deny is shown in full,
	// since the short form cites a stored verdict. A spawn or continuation is refused, as
	// judging one records it.
	DryRun bool
	// Lease is an explicit --lease; empty resolves through job.LeaseQuery.Resolve.
	Lease string
	// The attribution the caller knows about itself. No verdict reads what it SAYS; Judge
	// reads only whether installed glue (a non-empty Form) named a Host at all.
	Host string
	// Form is the form of the installed hook that called, such as sh or buzz, as that form
	// declares it (`magus shell --transport`). Two forms wired into one session are two
	// callers.
	Form    string
	Session string
	// Agent is the host's subagent id, for a wiring that forwards one field of the event
	// rather than the whole envelope; an envelope's own agent_id fills it otherwise.
	Agent      string
	Transcript string
	Event      string
	// Window names the terminal the call runs in, empty off a terminal. It keys what the
	// guard remembers for a caller no host delivered a session for, and is never recorded
	// as a session: a terminal window is not a host's conversation.
	Window string
	// ObservesSkillLoads is the one CAPABILITY on this struct rather than attribution: the
	// host's wiring reports skill loads to magus, so a rule may require one. It is set by
	// the wiring that provides the observation, never inferred from Host, because guard
	// code may not branch on a host's name and a name would not prove the wiring anyway.
	//
	// False is the safe answer: rules that need it stand down, which is what keeps them
	// from denying forever on a host that can never satisfy them.
	ObservesSkillLoads bool
	// RewritesInput is the third capability: the input is a shell command the host runs,
	// and the wiring's reply hands the host Verdict.UpdatedCommand to run in its place.
	// Without it the guard closes no stdin, since a host that never receives the
	// rewrite would run the command with stdin open while the trail says otherwise.
	RewritesInput bool
	// RendersAsk is the second capability: the wiring puts an ask verdict in front of the
	// person through the host's own prompt. Without it an ask is returned as a deny,
	// because a glue that predates the decision renders it as nothing and its host reads
	// nothing as allow.
	RendersAsk bool
}

// Verdict is the neutral result of evaluating one shell command: exactly
// one decision, carrying the field that decision needs. This envelope is the
// stable contract an agent host's hook config shapes with -o template (or
// parses from -o json); the host-specific response dialects live in the
// documentation, never in code.
type Verdict struct {
	SchemaVersion int    `json:"schema_version"`
	Decision      string `json:"decision"`          // one of agent.GuardDecisions
	Reason        string `json:"reason,omitempty"`  // deny: the block reason, written for the model
	Context       string `json:"context,omitempty"` // advise: context to inject alongside the allowed call
	// Rule names the catalogued rule or advisory that produced this verdict, and is set on
	// every deny, advise and ask (a workspace rule's carries its workspace prefix). Host
	// adapters still render Reason and Context; this is what a PERSON looks up, reports
	// as a false positive, or greps a trail for, and the text arm prints it beside the
	// decision for exactly that reason. Empty on a pass.
	Rule string `json:"rule,omitempty"`
	// Lease is the row this verdict was graded under, empty when the call named none.
	//
	// A session bound to a typo'd id, a session bound to a finished row, and a session
	// nobody leased are otherwise indistinguishable, and two of the three run unguarded.
	// An added optional field is not a schema bump: a glue that does not read it is
	// unaffected, which is the rule agent.GuardSchemaVersion states.
	Lease string `json:"lease,omitempty"`
	// LeaseFrom is which source answered Lease; see types.LeaseSource.
	LeaseFrom types.LeaseSource `json:"lease_from,omitempty"`
	// Next is a deny's remedy, served only when it passes the guard for the acting
	// lease, and pre-authorized for the calls after it. Reason renders it too.
	Next []hint.Next `json:"next,omitempty"`
	// UpdatedCommand is the command the host should run instead of the one it asked
	// about: that command with stdin closed. Set only on a pass or advise for a request
	// with RewritesInput, and never on a line that already starts by closing it.
	UpdatedCommand string `json:"updated_command,omitempty"`
}

// stdinClosedPrefix makes every command in the line read end-of-file from stdin. An agent's
// shell inherits a stdin nobody writes to, so a stray reader (grep with no file operand,
// read, a prompt, ssh, a pager) waits forever, and a host that times the call out
// backgrounds it instead of killing it. A heredoc, a pipe or a `<` still feed their own
// command, since each sets stdin for that command alone.
//
// A prefix rather than a `{ <line>\n} </dev/null` group. One host's isolation check for
// worktree agents judges the rewritten line; measured 2026-09-29, it refused the group as
// too complex even around `stat` or `git status`, and refuses the prefix only on a line it
// already found borderline (runtime-computed values beside a redirect).
const stdinClosedPrefix = "exec </dev/null; "

// advisoryStdinClosed tells a session once that its commands run with stdin closed.
const advisoryStdinClosed hint.MarkerKind = "stdin-closed"

const stdinClosedNotice = "magus runs your shell commands with stdin at end-of-file; pipe or redirect input explicitly."

// closeStdin is line with stdin closed, and false when line already starts by closing it.
func closeStdin(line string) (string, bool) {
	if strings.HasPrefix(line, stdinClosedPrefix) {
		return line, false
	}
	return stdinClosedPrefix + line, true
}

// hostUnnamed refuses a call from installed hook glue that did not say which agent host
// it answers. The glue picks the reply its host can parse from that name, so a call
// without it cannot be answered correctly for any host, and defaulting to one would
// hand another host a reply it drops, which on some hosts runs the call unguarded.
//
// The reason names no form, so the sh and Buzz forms of one template reply alike.
func hostUnnamed() Verdict {
	return Verdict{
		SchemaVersion: agent.GuardSchemaVersion,
		Decision:      "deny",
		Reason: types.FormatDiagnostic(types.HookHostUnnamed,
			"this hook did not pass --agent-name, so magus cannot tell which agent host it is "+
				"answering, and nothing was judged. Merge what `magus describe harness` prints into "+
				"the host's hook configuration; the commands it prints name the host."),
	}
}

// Judge evaluates one request against this workspace's rules and reports the verdict.
//
// A request from installed glue (Form set) that names no Host is refused with
// MGS3024 before anything is judged.
//
// An EMPTY input passes: a wrapper that hands the hook nothing must not have every tool
// call blocked. The caller owns the opposite case, a payload that failed to READ, because
// nothing here saw it.
func Judge(ctx context.Context, deps Dependencies, req Request) Verdict {
	if req.Form != "" && strings.TrimSpace(req.Host) == "" {
		return hostUnnamed()
	}
	input := req.Input
	hasInput := input != ""
	who := hookAttribution{Host: req.Host, Form: req.Form, Session: req.Session, Agent: req.Agent, Transcript: req.Transcript, Event: req.Event, Window: req.Window}
	isPath := req.IsPath
	// Where the call runs, which the bootstrap rule reads even when no workspace resolves
	// there to pin a location.
	callDir := ""
	description := ""
	var write writeFields
	// A host that writes its hook payload as JSON needs no jq and no --path: the envelope
	// says what is about to run and whether it is a write. Explicit flags still win, since
	// a wrapper that passed them meant them.
	if env, isEnvelope := decodeHookEnvelope(input); isEnvelope {
		// Attribution is resolved BEFORE the nothing-to-judge arm below returns: a skill
		// load is a nothing-to-judge envelope that still has to be recorded against the
		// session that made it, and a session read after the return is read too late.
		ctx = hookContextAt(ctx, deps, env.Cwd)
		if filepath.IsAbs(env.Cwd) {
			callDir = env.Cwd
		}
		if who.Session == "" {
			who.Session = env.Who.Session
		}
		if who.Transcript == "" {
			who.Transcript = env.Who.Transcript
		}
		if who.Event == "" {
			who.Event = env.Who.Event
		}
		if who.Agent == "" {
			who.Agent = env.Who.Agent
		}
		if req.DryRun && (env.LoadedSkill != "" || env.NothingToJudge) {
			return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
		}
		if env.LoadedSkill != "" {
			// Recorded, never judged. The gate is built here rather than reusing the one
			// below because this arm returns before it: same cacheDir, same session.
			recordSkillLoad(hint.NewGate(hookLocation(ctx, deps).cacheDir, who.factsKey()), env.LoadedSkill)
			return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
		}
		if env.NothingToJudge {
			// A host envelope whose tool_input carries no command, path or prompt (a todo
			// list, a search) has nothing any rule can read. Falling through judged the raw
			// JSON as a shell line, so a denied command merely NAMED inside a todo blocked
			// the tool call that wrote the todo.
			if env.AgentTranscript != "" {
				recordAgentUsage(hint.NewGate(hookLocation(ctx, deps).cacheDir, who.factsKey()), who.Agent, env.AgentTranscript)
			}
			return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
		}
		input = env.Value
		description = env.Description
		write = env.Write
		hasInput = input != ""
		if env.IsPath {
			isPath = true
		}
		if env.IsSpawn || env.IsContinue {
			if req.DryRun {
				return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "deny",
					Reason: "magus workspace: a spawn or continuation cannot be checked, since judging one records it."}
			}
			return judgeAgentEvent(ctx, deps, req, env, who)
		}
	}
	// One gate for the whole invocation, holding each enrolled advisory to one firing per
	// session. Built AFTER the envelope is decoded: a host that reports its session id only
	// inside the payload would otherwise be graded as having reported none, and every
	// session on that host would share the anonymous bucket. The acting lease is resolved
	// here for the same reason: the envelope's cwd is what locates the worker's marker.
	location := hookLocation(ctx, deps)
	deps.scope = scopeAt(location)
	policyDigest := ""
	if !req.DryRun {
		policyDigest = recordPolicy(ctx, deps, location, false)
	}
	ctx = withJobStoreRows(ctx, location)
	// Where the gates and the workspace rules keep session state. The rest of location is
	// what the rules judge against, so a check swaps only the cache dir.
	stateAt := location
	if req.DryRun {
		stateAt.cacheDir = copySessionState(location.cacheDir, who.callerKey(), who.factsKey())
		defer os.RemoveAll(stateAt.cacheDir)
	}
	markers := hint.NewGate(stateAt.cacheDir, who.callerKey())
	facts := hint.NewGate(stateAt.cacheDir, who.factsKey())
	bound := boundJob(who, location)
	actingLease, leaseFrom := resolveLease(who, req.Lease, bound)
	switch {
	case bound == "" || actingLease != bound:
	case req.DryRun:
		ctx = withRegisteredBase(ctx, deps, location, bound)
	case registerAgentBase(ctx, deps, location, bound):
		ctx = withJobStoreRows(ctx, location)
	}
	tool := hookToolCommand
	switch {
	case req.Observe:
		tool = hookToolRead
	case isPath:
		tool = hookToolWrite
	}
	verdict := Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass", Lease: actingLease, LeaseFrom: leaseFrom}
	// Where the acting lease STANDS, read once and before any rule. An id the job store does
	// not carry is refused, because every lease-scoped rule below reads that row and
	// finding nothing is how they all fall silent at once: the call would be graded by
	// nobody while looking exactly like a guarded one.
	//
	// Applied INSIDE each arm rather than returned from here, so the documented order
	// holds: the cache-dir rule and the workspace-wide denies are true whoever runs the
	// call. A pre-authorization does not stand it down either, since an id nobody declared means
	// nothing graded the call at all.
	//
	// --observe is exempt, as it is from every other verdict: it carries none.
	standing := actingLeaseStanding(ctx, deps, actingLease)
	denyUndeclared := func(command string) {
		if req.Observe || verdict.Decision == "deny" {
			return
		}
		if reason := denyUndeclaredLease(standing, actingLease, command); reason != "" {
			verdict.Decision, verdict.Reason, verdict.Context = "deny", reason, ""
			verdict.Rule = string(denyRuleLeaseUndeclared)
		}
	}
	// A served next is magus's own suggestion, and the guard does not argue with it: no
	// advisory fires on it, and the role-scoped rules stand down. The workspace-wide
	// denies do not, and they are the ones whose reasons say why (see internal/guard/preauth.go).
	var ruleRecord workspaceRuleRecord
	// remedy is the shell deny that carried a computed next, graded for the acting lease
	// once every rule has spoken.
	var remedy ShellVerdict
	preauth := ""
	if hasInput && !req.Observe && !isPath {
		preauth = servedNextPreauthorizes(markers, input)
	}
	switch {
	case !hasInput:
		// Nothing arrived on stdin. The verdict stays pass and the append below no-ops.
	case req.Observe:
		// No rule judges a read or a search, so none is run: the observation IS the whole
		// contribution. Running the write rules here would only ever manufacture a false
		// advisory about editing a file the agent opened read-only.
	case isPath:
		advice := ""
		// adviceKind is which rung spoke, for the verdict to name.
		//
		// NAMING IS NOT HOLDING. A kind is both an identity and a marker key, and the
		// two are separable: a rung sets this to be nameable, and separately chooses
		// whether to route its text through markers.Once.
		//
		// Enrolling these rungs in the gate as a side effect of naming them broke the
		// harness probe, which fires the AGENTS.md advisory once per wired harness and
		// reads the verdict: the first firing spent the marker and the other three
		// harnesses read `pass`, so `magus doctor` reported the guard uncovered. The
		// rungs below name themselves and speak every time, which is what they did
		// before they had names.
		adviceKind := hint.MarkerKind("")
		// Graded ahead of the rules, though it speaks near the end of them: the project
		// this write lands in is recorded whatever verdict they reach, so it cannot be
		// resolved inside a rung that a louder rule skips.
		drift := gradeScopeDrift(ctx, deps, facts, actingLease, input)
		// spoken reports that a rule MATCHED, which is not the same as a rule that
		// produced text. A once-per-session advisory that already fired this session
		// matched and stayed quiet, and the rules below it must not step into the silence
		// it left: without this the second write to a skill source would draw the
		// new-directory advisory instead of nothing.
		spoken := false
		// The checkout's own cache dir speaks before the job store, and it is the only rule
		// that does. Every lease-scoped verdict below is computed from files in there, so
		// a worker whose write paths happen to cover the dir must not be told it owns the
		// dir: what it is editing is whether the boundary was checked.
		if reason := denyCacheDirPath(location, input); reason != "" {
			verdict.Decision, verdict.Reason, verdict.Rule = "deny", reason, string(denyRuleCacheDirWrite)
		}
		if reason := denyTokenStatePath(location, input); reason != "" {
			verdict.Decision, verdict.Reason, verdict.Rule = "deny", reason, string(denyRuleTokenState)
		}
		denyUndeclared("")
		if verdict.Decision != "deny" {
			switch g := gradeLeasedEdit(ctx, deps, actingLease, input, write); g.Decision {
			case "deny":
				verdict.Decision = "deny"
				verdict.Reason = g.Reason
				verdict.Rule = cmp.Or(g.Rule, string(denyRuleLeaseWrite))
			case "advise":
				// Held by its own key when it has one; lease-state only names the rest.
				advice, adviceKind, spoken = markers.Once(cmp.Or(g.Key, g.Kind), g.Context), cmp.Or(g.Kind, hint.MarkerKind(advisoryLeaseState)), true
			}
		}
		if verdict.Decision != "deny" {
			if g := denyVCSOffSwitch(actingLease, input, write); g.Decision == "deny" {
				verdict.Decision, verdict.Reason, verdict.Rule = "deny", g.Reason, g.Rule
			}
		}
		if verdict.Decision != "deny" {
			switch g := gradeHookWiringWrite(actingLease, who.Agent != "", input); g.Decision {
			case "deny":
				verdict.Decision, verdict.Reason, verdict.Rule = "deny", g.Reason, string(denyRuleHookWiringWrite)
			case "advise":
				if !spoken {
					advice, adviceKind, spoken = markers.Once(g.Kind, g.Context), g.Kind, true
				}
			}
		}
		// Last of the denies and first thing a Buzz write meets, in that order for a
		// reason: the two above answer whether this agent may touch the file at all, and
		// there is nothing to learn before a write that is refused anyway.
		if verdict.Decision != "deny" {
			if reason := denyBuzzWriteWithoutSkill(facts, req.ObservesSkillLoads, location.workspace, input); reason != "" {
				verdict.Decision, verdict.Reason = "deny", reason
				verdict.Rule = string(denyBuzzUnbriefed)
			}
		}
		// A script is judged by what running it would be judged by, and the write is the
		// last moment that costs nothing to change.
		if verdict.Decision != "deny" {
			if v := denyScriptWrite(deps, input, write); v.Deny != "" {
				verdict.Decision, verdict.Reason, verdict.Rule = "deny", v.Deny, v.RuleName()
			}
		}
		// The generated-output rule is definitive (it reads declared globs), so it
		// outranks the heuristics below; the instruction-file nudge is a heuristic on
		// the filename and only fills the silence it leaves.
		if verdict.Decision == "pass" && !spoken {
			if text := adviseGeneratedWrite(ctx, deps, input); text != "" {
				advice, adviceKind, spoken = text, advisoryGeneratedWrite, true
			}
		}
		// The notes rule DENIES, so it is checked before the advisories: a verdict that
		// blocks is not something to fall through to.
		if verdict.Decision == "pass" && !spoken {
			if reason := denyNotesWrite(deps, input); reason != "" {
				verdict.Decision = "deny"
				verdict.Reason = reason
				verdict.Rule = string(denyRuleNotesAuthor)
			}
		}
		if verdict.Decision == "pass" && !spoken {
			if text := adviseInstalledSkillWrite(input); text != "" {
				advice, adviceKind, spoken = text, advisoryInstalledSkill, true
			}
		}
		// Every rung below advises about THIS workspace, so a write outside it (a scratch
		// file, a user-level config) is none of their business. The two rungs above still
		// speak: host wiring and an installed skill live outside a workspace by design.
		if deps.scope.outside(input) {
			spoken = true
		}
		if verdict.Decision == "pass" && !spoken {
			if text := adviseInstructionWrite(input); text != "" {
				advice, adviceKind, spoken = text, advisoryInstruction, true
			}
		}
		// Both of these are inert outside magus's own checkout; see magusOwnSourceTree.
		if verdict.Decision == "pass" && !spoken {
			if text := adviseAgentSurfaceWrite(input); text != "" {
				advice, adviceKind, spoken = markers.Once(advisorySkillSource, text), advisorySkillSource, true
			}
		}
		if verdict.Decision == "pass" && !spoken {
			if text := adviseDescriptorWrite(input); text != "" {
				advice, adviceKind, spoken = markers.Once(advisoryRegenSource, text), advisoryRegenSource, true
			}
		}
		// Above the new-directory rule because it is the wider question: whether this
		// write belongs in this session at all outranks how its unit is laid out.
		if verdict.Decision == "pass" && !spoken && drift.advice != "" {
			// Not held here: gradeScopeDrift already gates its own firing, on the PROJECT
			// as well as the kind, because a second drift into a different project is a
			// second fact. Re-holding it on the kind alone would report only the first.
			advice, adviceKind, spoken = drift.advice, advisoryScopeDrift, true
		}
		// Mutually exclusive with the rung below: that one answers an empty directory,
		// this one a populated one. Held to one firing per session, where the new-directory
		// rule is not, because creating a file is ordinary work and creating a boundary
		// is not (internal/guard/file.go).
		if verdict.Decision == "pass" && !spoken {
			if text := adviseNewFileName(input); text != "" {
				advice, adviceKind, spoken = markers.Once(advisoryNewFile, text), advisoryNewFile, true
			}
		}
		// Last rung, so it sets no flag: there is nothing below it to hold back.
		//
		// Not held, unlike its siblings: creating a boundary is not ordinary work, and the
		// rung above (new-file) is the one held for exactly that contrast. It carries a
		// kind anyway, because the kind is also the NAME a verdict reports.
		if verdict.Decision == "pass" && !spoken {
			if text := adviseNewSourceDir(input); text != "" {
				advice, adviceKind = text, advisoryNewSourceDir
			}
		}
		if verdict.Decision == "pass" && advice != "" {
			verdict.Decision = "advise"
			verdict.Context = advice
			verdict.Rule = string(adviceKind)
		}
		// The workspace's magus\guard.write rule, last because it may only add.
		verdict, ruleRecord = gradeWorkspaceWrite(ctx, deps, verdict, input, write, actingLease, who, stateAt)
		// A denied write never happens, so it never touched anything.
		if verdict.Decision != "deny" {
			drift.record()
		}
	default:
		// The sibling-checkout and cache-dir rules read the FILESYSTEM, so neither can
		// live inside Evaluate's pure rule set; ranking them is pure, and is
		// where the ordering is tested. The cache dir is outermost: what it refuses
		// outranks every other deny on the line (internal/guard/cache.go).
		shellD := effectiveDialect(deps.ShellDialect)
		if callDir == "" {
			callDir = location.dir
		}
		deps.callDir = callDir
		v := judgeShellLine(ctx, deps, location, callDir, input, shellD)
		// Outside judgeShellLine: it reads session state (which skills have loaded) rather
		// than the line alone, and a remedy graded by that function never writes Buzz.
		switch v = rankBuzzAuthor(v, denyBuzzAuthorWithoutSkill(facts, req.ObservesSkillLoads, location.workspace, input, shellD)); {
		case v.Deny != "":
			// These are the denies that hold for everyone, so a pre-authorization does not
			// reach them: whole-tree VCS, a pipe or redirect of magus's own output, a raw
			// language tool, a relocated checkout. A next magus served would not carry one
			// anyway, and the structural test is what says so before it ships.
			verdict.Decision = "deny"
			verdict.Reason = v.Deny
			verdict.Rule = v.RuleName()
			if v.Rule.Name == denyRuleSiblingCheckout {
				lead, next := siblingCheckoutRemedy(input, shellD)
				v = v.withRemedy(lead, next...)
			}
			remedy = v
		case v.Context != "" && preauth == "":
			if held := markers.OnceOrBrief(v.Kind, v.Context, v.Brief); held != "" {
				verdict.Decision = "advise"
				verdict.Context = held
				verdict.Rule = v.advisoryName()
			}
		}
		denyUndeclared(input)
		// The job store's half of the command surface, ranked BELOW the rules above
		// (a sibling checkout's gate is the wrong tree before it is the wrong scope).
		// Every one is ROLE-scoped, which is what a pre-authorization stands down: the
		// command came from magus, computed for this role, so refusing it here would be
		// the tool disagreeing with itself.
		for _, rule := range roleScopedCommandRules() {
			if verdict.Decision == "deny" || preauth != "" {
				break
			}
			if reason := rule.judge(ctx, deps, actingLease, input); reason != "" {
				verdict.Decision, verdict.Reason, verdict.Context = "deny", reason, ""
				verdict.Rule = string(rule.name)
			}
		}
		// The focus rule. Its DENY outranks any advisory above it, because that one is
		// about a boundary an orchestrator declared; its advisory only fills a silence.
		// Nothing runs once a deny stands: a wrong tree is a bigger mistake than a wrong
		// project, and the rule that caught it is also the cheaper one to have run.
		if verdict.Decision != "deny" && preauth == "" {
			focus := gradeFocusRead(ctx, deps, actingLease, input)
			switch {
			case focus.Decision == "deny":
				verdict.Decision, verdict.Reason, verdict.Context = "deny", focus.Reason, ""
				verdict.Rule = string(denyRuleFocusRead)
			case verdict.Decision == "pass" && focus.Decision == "advise" && !markers.MarkFired(advisoryFocusPath(focus.Rel)):
				if held := markers.OnceOrBrief(advisoryFocus, focus.Context, focus.Brief); held != "" {
					verdict.Decision, verdict.Context, verdict.Rule = "advise", held, string(advisoryFocus)
				}
			}
		}
		// The push gate, upgraded from the advisory gitGuard returned when the run log proves
		// no green gate covers this commit: the person is asked through the host's prompt,
		// or a leased worker is refused outright (see gradePushWithoutGate).
		//
		// Here rather than in gitGuard because that function is pure over the parsed
		// command and this reads the run log and the revision. The rule is split the same
		// way the skill gates are: the parser decides WHAT the command is, and the arm
		// with a location decides what the workspace knows about it.
		if verdict.Rule == string(advisoryPushGate) && preauth == "" {
			cover, commit := pushCoverage(ctx, deps, location, input, shellD, callDir)
			switch decision, reason := gradePushWithoutGate(cover, commit, actingLease); decision {
			case "ask":
				verdict.Decision, verdict.Context, verdict.Reason = "ask", "", reason
				if !req.RendersAsk {
					verdict.Decision, verdict.Reason = "deny", reason+"\n"+askUnrendered
				}
				verdict.Rule = string(denyRulePushUngated)
			case "deny":
				verdict.Decision, verdict.Context, verdict.Reason = "deny", "", reason
				verdict.Rule = string(denyRulePushUngated)
			}
		}
		// Gated on the command being the GATE, not on it merely spawning work: the
		// advisory's answer is to run a narrower target, and firing on one argues with
		// the caller for doing what it asked.
		if verdict.Decision == "pass" && preauth == "" && commandRunsGate(input) {
			full, brief := adviseRepeatGate(workspaceRunsDir(location.cacheDir), time.Now())
			if notice := markers.OnceOrBrief(advisoryGateRepeat, full, brief); notice != "" {
				verdict.Decision = "advise"
				verdict.Context = notice
				verdict.Rule = string(advisoryGateRepeat)
			}
		}
		// The guard's half of the index-staleness fact; the load-bearing half rides the
		// command's own output (stale_index.go). alreadyFired is asked BEFORE the rule, not
		// after: producing this text costs a directory walk, and once the session has been
		// told, paying for it again only to discard the answer is the cost nobody sees.
		if verdict.Decision == "pass" && preauth == "" && !markers.AlreadyFired(advisoryGraphStale) && commandReadsGraph(input) {
			if notice := markers.Once(advisoryGraphStale, deps.graphStaleAdvice(ctx)); notice != "" {
				verdict.Decision = "advise"
				verdict.Context = notice
				verdict.Rule = string(advisoryGraphStale)
			}
		}
		// The SPLIT-RUN rule's cross-call shape: the same target run again on a different
		// project set, as a separate command rather than chained on one line (that shape is
		// splitRunLineAdvice's, inside Evaluate). gradeSplitRun records this call's
		// invocation on the SESSION's facts either way, so it must run whenever nothing
		// louder already spoke, not only when it turns out to have something to say.
		if verdict.Decision == "pass" && preauth == "" {
			if text, matched := gradeSplitRun(facts, input); matched {
				if held := markers.Once(advisorySplitRun, text); held != "" {
					verdict.Decision = "advise"
					verdict.Context = held
					verdict.Rule = string(advisorySplitRun)
				}
			}
		}
		// The workspace's magus\guard.command rule, last because it may only add to what
		// every rule above said.
		verdict, ruleRecord = gradeWorkspaceCommand(ctx, deps, verdict, commandRuleInput{
			command:     input,
			description: description,
			dialect:     shellD,
			preauth:     preauth,
			lease:       actingLease,
		}, who, stateAt)
		// Last, so a line any rule refused or put to a person binds nobody.
		if !req.DryRun && (verdict.Decision == "pass" || verdict.Decision == "advise") {
			bindOnExec(ctx, location, who, input)
		}
	}
	// The two notices about the acting lease ITSELF: a row that has finished and an id
	// magus cannot parse both leave every lease-scoped rule inert while the verdicts look
	// identical to a session nobody leased. Once per session each, and never on a deny,
	// which explains itself and was reached by a rule that did not need the row.
	for kind, notice := range map[hint.MarkerKind]string{
		advisoryLeaseTerminal: adviseTerminalLease(standing, actingLease),
		advisoryLeaseInvalid:  adviseInvalidLease(actingLease),
	} {
		if notice == "" || req.Observe || verdict.Decision == "deny" || verdict.Decision == "ask" {
			continue
		}
		held := markers.Once(kind, notice)
		if held == "" {
			continue
		}
		if verdict.Decision == "advise" {
			verdict.Context += "\n\n" + held
			continue
		}
		verdict.Decision, verdict.Context, verdict.Rule = "advise", held, string(kind)
	}
	// The one rewrite the guard makes, after every rule has spoken and never on a deny or
	// an ask: a refused call does not run, and a person approves the command as typed.
	if req.RewritesInput && tool == hookToolCommand && hasInput && (verdict.Decision == "pass" || verdict.Decision == "advise") {
		if line, closed := closeStdin(input); closed {
			verdict.UpdatedCommand = line
			if notice := markers.Once(advisoryStdinClosed, stdinClosedNotice); notice != "" {
				if verdict.Decision == "advise" {
					verdict.Context += "\n\n" + notice
				} else {
					verdict.Decision, verdict.Context, verdict.Rule = "advise", notice, string(advisoryStdinClosed)
				}
			}
		}
	}
	// Worded after every arm has spoken, so whichever rule refused is the one held to a
	// full explanation per session. An ask is never shortened: it waits on a person.
	verdictRef := ""
	if verdict.Decision == "deny" && verdict.Rule != "" && !req.Observe {
		note := ""
		if tool == hookToolCommand {
			note = nothingRanNote(input, effectiveDialect(deps.ShellDialect))
		}
		shapeGate := markers
		if req.DryRun {
			shapeGate = hint.Gate{} // spends and stores nothing, so the deny is worded in full
		}
		// Only while the deny that computed the remedy is still the one standing: a later
		// rule's refusal is about something else.
		var next []hint.Next
		if len(remedy.Next) > 0 && verdict.Reason == remedy.Deny {
			next = servableRemedy(ctx, deps, location, callDir, standing, actingLease, remedy.Next)
			if len(next) > 0 {
				verdict.Reason = remedy.Lead
			}
		}
		verdict.Reason, verdictRef, verdict.Next = shapeDeny(ctx, shapeGate, verdict.Rule, verdict.Reason, note, next)
	}
	// An observation is not a judgment, and the trail already knows the difference: an
	// AgentCommand with no Decision previews as "observed" rather than "guard: <decision>".
	// Recording the pass verdict here would have every read claim the guard ran and cleared
	// it, which is exactly the conflation --observe exists to remove. The WIRE verdict is
	// unchanged: a host still needs a decision it can parse, and "pass" is the true one.
	record := verdict
	if req.Observe {
		record.Decision, record.Reason, record.Context = "", "", ""
	}
	if !req.DryRun {
		appendHookActivity(ctx, location, input, who, tool, actingLease, preauth, verdictRef, policyDigest, record, ruleRecord)
	}
	return verdict
}

// judgeShellLine ranks the rules every caller meets on a shell line, whatever lease it
// holds: Evaluate's, then the ones that read the filesystem.
func judgeShellLine(ctx context.Context, deps Dependencies, at location, callDir, line string, d Dialect) ShellVerdict {
	v := rankOwnBuild(Evaluate(deps, line), ownBuildVerdict(deps, callDir, line, d))
	// A remedy computed from a script's line would run outside the directory and the
	// lines around it that the script sets up.
	script := denyScriptContent(deps, callDir, line, d)
	script.Next, script.Lead = nil, ""
	v = rankScriptContent(v, script)
	v = rankSiblingCheckout(v, denySiblingCheckout(line, d))
	v = rankWorktreeRemove(v, denyWorktreeRemove(ctx, deps, at, callDir, line, d))
	v = rankInterpreterRewrite(v, denyInterpreterRewrite(at, line, d))
	v = rankCacheDirWrite(v, denyCacheDirCommand(at, line, d))
	return rankTokenState(v, denyTokenStateCommand(at, line, d))
}

// roleScopedRule is a command rule that reads the acting lease's row, with the name its
// refusal is recorded under.
type roleScopedRule struct {
	name  denyRuleName
	judge func(context.Context, Dependencies, string, string) string
}

// roleScopedCommandRules are the command rules a served next stands down, so each is
// asked of a remedy before it is served.
func roleScopedCommandRules() []roleScopedRule {
	return []roleScopedRule{
		{denyRuleLeaseGate, denyLeaseScopedGate},
		{denyRuleLeaseVCS, denyLeaseScopedVCS},
		{denyRuleLeaseRebind, denyLeaseScopedRebind},
		{denyRuleLeaseHarness, denyLeaseScopedHarness},
		{denyRuleLeaseWrite, denyWriteOutsideLease},
	}
}

// servableRemedy keeps the remedies the acting lease may run. A served next is
// pre-authorized and the role-scoped rules stand down for it, so one they would refuse
// is dropped here: serving it would clear a command the role may not run.
//
// Graded by the rules the next call meets, rather than filtered by a list of its own,
// so a rule added later grades remedies without anyone remembering to.
func servableRemedy(ctx context.Context, deps Dependencies, at location, callDir string, standing leaseStanding, actingLease string, next []hint.Next) []hint.Next {
	role, writePaths := hint.LeaseRole(standing.rows, actingLease)
	d := effectiveDialect(deps.ShellDialect)
	var kept []hint.Next
	for _, n := range hint.ServableTo(role, writePaths, next) {
		if judgeShellLine(ctx, deps, at, callDir, n.Run, d).Deny != "" || denyUndeclaredLease(standing, actingLease, n.Run) != "" {
			continue
		}
		refused := slices.ContainsFunc(roleScopedCommandRules(), func(rule roleScopedRule) bool {
			return rule.judge(ctx, deps, actingLease, n.Run) != ""
		})
		if refused || gradeFocusRead(ctx, deps, actingLease, n.Run).Decision == "deny" {
			continue
		}
		kept = append(kept, n)
	}
	return kept
}

// hookEnvelope is the JSON an agent host writes to a hook's stdin: which tool is about to
// run and with what. Only the fields the guard needs are modeled; everything else in the
// payload is ignored rather than rejected, since a host is free to add to it.
type hookEnvelope struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	// ConversationID is an alternate session pointer some hosts put on the envelope
	// instead of session_id. HostAttribution prefers session_id when both are set.
	ConversationID string `json:"conversation_id"`
	// ParentConversationID is the orchestrator session on a spawn event. When set,
	// a spawn is attributed to the parent rather than the child conversation.
	ParentConversationID string `json:"parent_conversation_id"`
	// Command is a shell line at the envelope root. The tool_input.command spelling
	// is preferred when both are present.
	Command string `json:"command"`
	// Task is a spawn prompt at the envelope root. tool_input.prompt is preferred
	// when both are present.
	Task         string `json:"task"`
	SubagentType string `json:"subagent_type"`
	// Cwd is the directory the host reports the tool call runs in. It is what locates the
	// WORKER's checkout when the host runs its hooks somewhere else, such as the
	// orchestrator's directory, and with it the lease marker bound there.
	Cwd string `json:"cwd"`
	// TranscriptPath is the host's own log of this session. Recorded as a pointer so a
	// session id in the activity view leads somewhere; magus never reads the file.
	TranscriptPath string `json:"transcript_path"`
	// AgentID is the subagent making this tool call, on a host that tells a subagent's
	// calls from its root session's; "" for the root session and for hosts that do not.
	AgentID string `json:"agent_id"`
	// AgentTranscriptPath is that subagent's own log, on an event that reports on the
	// subagent rather than a tool call. Read only for its last usage record.
	AgentTranscriptPath string `json:"agent_transcript_path"`
	ToolName            string `json:"tool_name"`
	// ToolResponse is what a finished call returned, present only on an after-the-call
	// event. Read as any so a host that reports it as a string costs this field rather than
	// the decode of the whole envelope.
	ToolResponse any `json:"tool_response"`
	// ToolInput is read as a plain map, so a field arriving with a type this guard did
	// not expect costs that field rather than the whole decode. Typed, a numeric `op`
	// failed the unmarshal outright and the raw JSON was then judged as a shell line.
	ToolInput map[string]any `json:"tool_input"`
}

// envelopeString reads one tool_input field, treating anything that is not a string as
// absent. A host is free to add to the payload, and a field magus cannot read is one it
// has no business guessing at.
func envelopeString(input map[string]any, key string) string {
	s, _ := input[key].(string)
	return s
}

// envelopeEdits reads an `edits` list of replacements, each shaped like a single edit's
// fields. A list with any entry magus cannot read is no list at all: applying the entries
// it could read would compute a file the host is not about to write.
func envelopeEdits(input map[string]any) []textEdit {
	list, ok := input["edits"].([]any)
	if !ok {
		return nil
	}
	edits := make([]textEdit, 0, len(list))
	for _, item := range list {
		fields, ok := item.(map[string]any)
		if !ok {
			return nil
		}
		oldText, okOld := fields["old_string"].(string)
		newText, okNew := fields["new_string"].(string)
		if !okOld || !okNew {
			return nil
		}
		edits = append(edits, textEdit{OldText: oldText, NewText: newText, ReplaceAll: fields["replace_all"] == true})
	}
	return edits
}

// envelopeWritePath is the file an edit tool is about to write, or "".
//
// `file_path` is the documented spelling and the others are what the same hosts use for
// their other editors, so the fallback is any `*_path` key rather than a list magus would
// have to grow per host: `transcript_path` and `cwd` live on the envelope itself, not in
// tool_input, so nothing here can pick them up.
func envelopeWritePath(input map[string]any) string {
	if p := envelopeString(input, "file_path"); p != "" {
		return p
	}
	keys := slices.Sorted(maps.Keys(input))
	for _, key := range keys {
		if strings.HasSuffix(key, "_path") {
			if p := envelopeString(input, key); p != "" {
				return p
			}
		}
	}
	return ""
}

// The tool labels recorded on an activity event. They are magus's OWN vocabulary, chosen by
// which flags the wrapper passed, never a host's tool name.
//
// That division is the whole design: only the wrapper knows that its host calls a read
// "Read" or "read_file", and mapping those names here would be a per-host branch, so the
// next change to any host would mean a magus release. The matcher in a host's own config is
// where the host's vocabulary lives.
//
// The hostagnostic linter matches host NAMES, so a switch over "Read"/"Bash" (a
// per-host branch in everything but spelling) passes it untouched.
// The hostvocab linter is the layer that catches that one: a host's
// word for a tool may not appear as a string literal in guard code at all, so a lookup
// table is no cheaper than a switch. These three constants are what it leaves room for.
const (
	hookToolCommand = "shell.command"
	hookToolWrite   = "file.write"
	hookToolRead    = "file.read"
)

// decodeHookEnvelope pulls the thing to judge out of a host's hook payload, reporting
// whether the input was an envelope at all.
//
// Reading it here keeps `jq` off the critical path of every tool call, and lets
// attribution come from the payload rather than from flags a wrapper has to
// remember. A payload carrying file_path rather than command is a write, so the
// envelope also answers the --path question.
//
// A payload with a file_path rather than a command is a WRITE, which is the --path
// question, so the envelope decides that too: a caller that pipes real JSON should not
// also have to know which flag its shape implies.
//
// The envelope cannot tell a read from a write on its own (both arrive carrying a
// file_path), so it does not try. --observe is what separates them, and only the wrapper
// can set it, because only the wrapper knows which of its host's tools merely look.
//
// A payload carrying a PROMPT rather than either is a spawn: it is RECORDED, and never judged
// as a shell line. A prompt that merely MENTIONS a denied command would otherwise block the
// spawn describing it; the commands it presents as ones to run are graded on their own
// (internal/guard/brief.go).
//
// Anything that is not an object with a usable tool_input is left alone and judged as the
// literal text it is: the bare-command form keeps working exactly as before.
func decodeHookEnvelope(raw string) (hookRequest, bool) {
	if !strings.HasPrefix(raw, "{") {
		return hookRequest{}, false
	}
	var env hookEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return hookRequest{}, false
	}
	req := hookRequest{Cwd: env.Cwd, Who: hookAttribution{
		Session:    envelopeSession(env),
		Transcript: env.TranscriptPath,
		Event:      env.HookEventName,
		Agent:      env.AgentID,
	}}
	tool := magusToolCall(env.ToolName)
	switch {
	case tool != "":
		// An MCP call to one of magus's own tools, normalized to the one shape every
		// rule here reads: a command line. It is the SAME work the CLI verbs do, through
		// a different transport, so a rule that held on one channel would move the
		// traffic rather than stop it. Judged on the TOOL NAME rather than on a param
		// being present: requiring `op` left the tools that do not carry one,
		// client and status among them, reaching no rule at all.
		req.Value = buildCall(tool, env.ToolInput, env.Cwd)
	case envelopeString(env.ToolInput, "command") != "":
		req.Value = envelopeString(env.ToolInput, "command")
		req.Description = envelopeString(env.ToolInput, "description")
	case env.Command != "":
		req.Value = env.Command
	case envelopeWritePath(env.ToolInput) != "":
		req.Value, req.IsPath = envelopeWritePath(env.ToolInput), true
		// Read by shape, like the path: a whole-file write carries its content, an edit the
		// text it replaces and the replacement. Another shape leaves all three empty.
		req.Write = writeFields{
			Content:    envelopeString(env.ToolInput, "content"),
			OldText:    envelopeString(env.ToolInput, "old_string"),
			NewText:    envelopeString(env.ToolInput, "new_string"),
			ReplaceAll: env.ToolInput["replace_all"] == true,
			Edits:      envelopeEdits(env.ToolInput),
		}
	case envelopeString(env.ToolInput, "skill") != "":
		// A skill load carries nothing to judge; it is recorded so a later spawn can ask
		// whether the session read the brief. The FIELD name is magus's contract with the
		// host config, the way `command` and `file_path` are; the host's tool name stays
		// in its own matcher.
		req.LoadedSkill = envelopeString(env.ToolInput, "skill")
		req.NothingToJudge = true
	case envelopeString(env.ToolInput, "prompt") != "" || env.Task != "":
		if p := envelopeString(env.ToolInput, "prompt"); p != "" {
			req.Value = p
		} else {
			req.Value = env.Task
		}
		req.IsSpawn = true
		req.Tool = env.ToolName
		// The caller's own model choice, when it named one. Absent when the spawn
		// inherits the parent's model, which is a legitimate choice this guard
		// takes no position on: it is recorded so the question can be asked at
		// all, not so an answer can be graded.
		req.DeclaredModel = envelopeString(env.ToolInput, "model")
		req.Spawn = spawnFields{
			AgentType:   cmp.Or(envelopeString(env.ToolInput, "subagent_type"), env.SubagentType),
			Description: envelopeString(env.ToolInput, "description"),
			Name:        envelopeString(env.ToolInput, "name"),
			// Only an explicit true: a host whose default is to background agents still
			// reports false here, because a default is not something the caller asked for.
			Background: env.ToolInput["run_in_background"] == true,
			// Any declared isolation mode means the child is not sharing this checkout.
			Isolated: envelopeString(env.ToolInput, "isolation") != "",
		}
		req.AfterCall = env.ToolResponse != nil
		req.SpawnedAgent = spawnedAgentID(env.ToolResponse)
		if env.ParentConversationID != "" {
			req.Who.Session = env.ParentConversationID
		}
		// Most specific label first. A sub-agent TYPE names what was delegated to and repeats
		// across spawns, so it groups a spawn feed; a description is per-spawn prose; the
		// tool name is the last resort that at least says a spawn happened.
		for _, label := range []string{
			envelopeString(env.ToolInput, "subagent_type"),
			env.SubagentType,
			envelopeString(env.ToolInput, "description"),
			env.ToolName,
		} {
			if label != "" {
				req.Child = label
				break
			}
		}
	case envelopeString(env.ToolInput, "to") != "" && env.ToolInput["message"] != nil:
		// A message addressed to an agent that already exists: the continuation of a
		// subagent. Read by shape like every arm above, so a host's name for the tool
		// stays in its own matcher. The message goes unjudged for the reason a spawn
		// prompt does, and a structured message has no text to hand the rule.
		req.IsContinue = true
		req.Tool = env.ToolName
		req.Target = envelopeString(env.ToolInput, "to")
		req.Value = envelopeString(env.ToolInput, "message")
	default:
		// A payload that identifies itself as a host hook is an envelope even when its
		// tool_input holds nothing this guard reads. Reporting "not an envelope" here sent
		// the raw JSON to the shell rules, which read a denied command quoted inside a todo
		// or a search string as the command about to run and blocked it.
		//
		// Keyed on the envelope's OWN fields, so a bare `{"tool_input":{}}` (which names no
		// host event and could be anything) still falls through to the literal form.
		if env.HookEventName == "" && env.ToolName == "" && env.SessionID == "" && env.ConversationID == "" {
			return hookRequest{}, false
		}
		req.NothingToJudge = true
		if env.AgentID != "" {
			req.AgentTranscript = env.AgentTranscriptPath
		}
	}
	return req, true
}

// envelopeSession picks the session pointer from the fields a host may send.
// session_id wins when both it and conversation_id are present.
func envelopeSession(env hookEnvelope) string {
	if env.SessionID != "" {
		return env.SessionID
	}
	return env.ConversationID
}

// HostAttribution reads the two pointers only a host knows out of its hook payload: its
// session id and its transcript path. Both are empty for anything that is not such an
// envelope, so a caller may hand it whatever arrived on stdin.
//
// Exported because the checkpoint command reads the same payload for the same two fields.
// A second decoder there would be a second copy of a wire contract, which is the drift
// internal/agent's guard constants were moved out of package main to avoid.
func HostAttribution(raw string) (session, transcript string) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") {
		return "", ""
	}
	var env hookEnvelope
	if json.Unmarshal([]byte(raw), &env) != nil {
		return "", ""
	}
	if env.SessionID != "" {
		return env.SessionID, env.TranscriptPath
	}
	return env.ConversationID, env.TranscriptPath
}

// hookRequest is what a host's payload asked the guard to judge: the text, whether it is a
// path rather than a command, and who reported it. A spawn asks for nothing to be judged: it
// carries the handed context and the callee's label, and is recorded rather than evaluated.
type hookRequest struct {
	Value  string
	IsPath bool
	// Description is the label the caller wrote for a shell command, "" when it wrote none.
	Description string
	// Write is the text a file write carries, each field empty when the host sent none.
	Write writeFields
	// Cwd is where the host says the call runs; "" when the envelope carried none.
	Cwd string
	// NothingToJudge is a recognized host envelope carrying no command, path or prompt.
	// Distinct from "not an envelope", which is judged as the literal text it is.
	NothingToJudge bool
	IsSpawn        bool
	// IsContinue is a message to an existing subagent; Target is the agent it addresses.
	IsContinue bool
	Target     string
	// Spawn is what a spawn's tool_input said about the child, each field empty when the
	// host sent none.
	Spawn spawnFields
	// AfterCall is a spawn call that has already run: its envelope carries the response.
	// It is recorded and never judged, since there is no longer a call to stop.
	AfterCall bool
	// SpawnedAgent is the id the host gave the child of a spawn call that has already
	// run, read from its response. "" before the call runs, or when the response names none.
	SpawnedAgent string
	Tool         string
	Child        string
	// DeclaredModel is the model the spawning tool_input named, or "" when it named
	// none. Not called Model: that word already means the reply CHANNEL in this
	// guard's coverage vocabulary (deny=model, advise=model), so a bare Model field
	// here would read as a verdict channel rather than a spawn's own claim.
	DeclaredModel string
	// LoadedSkill is the skill this envelope reports as loaded, or "" when it reports
	// none. Recorded rather than judged: it is what lets a later spawn ask whether the
	// session read its brief. See denySpawnWithoutBrief.
	LoadedSkill string
	// AgentTranscript is the log of the subagent a nothing-to-judge envelope reports on,
	// "" when it names none. Its last usage record is the subagent's context size.
	AgentTranscript string
	Who             hookAttribution
}

// hookAttribution is what the host wrapper knows about itself and cannot be
// derived here: a hook runs as a short-lived client process with no way to
// discover which agent host started it. It travels beside the input rather than
// inside the judged text because the guard's verdict must never depend on it.
type hookAttribution struct {
	Host string
	// Form is the form of the installed hook that called, such as sh or buzz.
	Form       string
	Session    string
	Transcript string
	Event      string
	// Agent is the subagent making the call, "" for a root session or a host that does
	// not say.
	Agent string
	// Window is the terminal the call runs in; see [Request.Window].
	Window string
}

// callerID is the id this caller goes by: the host's session, else the terminal window.
func (who hookAttribution) callerID() string {
	if s := strings.TrimSpace(who.Session); s != "" {
		return s
	}
	return strings.TrimSpace(who.Window)
}

// callerKeyEscaper keeps the key's delimiter out of every part. '%' is escaped too, so a
// part that already holds "%2F" cannot collide with one that held "/".
var callerKeyEscaper = strings.NewReplacer("%", "%25", "/", "%2F")

// factsKey keys FACTS about a caller, what a rule reads as "did this happen": a skill
// load, the projects written. `<host>/<caller>`, each part escaped, because two hosts may
// present the same id. The hook form is left out so a session wiring some surfaces as sh
// and others as Buzz sees one set of facts. A caller with no session is keyed on its
// terminal window; with neither it is empty, so hint.Gate falls back to its anonymous
// window rather than keying every unattributed caller together.
func (who hookAttribution) factsKey() string { return FactsKey(who.Host, who.callerID()) }

// FactsKey is the marker key the guard files a caller's facts under, for a reader outside
// the package: `<host>/<callerID>` with each part escaped, or "" when callerID is empty.
// callerID is a host session id or a terminal window key.
func FactsKey(host, callerID string) string {
	callerID = strings.TrimSpace(callerID)
	if callerID == "" {
		return ""
	}
	return callerKeyEscaper.Replace(host) + "/" + callerKeyEscaper.Replace(callerID)
}

// callerKey keys TEXT a caller has already rendered, a fire-once notice or a deny's full
// reason: `<host>/<form>/<caller>`. The sh and Buzz forms of one hook are two readers of
// their own replies, so each is told a rule in full once. Keyed and empty like factsKey.
func (who hookAttribution) callerKey() string {
	id := who.callerID()
	if id == "" {
		return ""
	}
	return callerKeyEscaper.Replace(who.Host) + "/" + callerKeyEscaper.Replace(who.Form) + "/" + callerKeyEscaper.Replace(id)
}

type location struct {
	cacheDir  string
	workspace string
	// dir is where the tool call runs, which the focus rule needs and the trail does
	// not: a session opened in a subdirectory stands in a different project than the
	// workspace root does, and that difference is the whole of what focus judges.
	dir string
}

type locationKey struct{}

// withJobStoreRows reads the job store once and pins it for the rules below, error
// included: an unreadable store and a store with no rows are different facts, and only the
// first means a rule could not be evaluated at all.
//
// Four of them grade against it on one command arm, and each used to open and parse the
// same file for itself. Nothing inside a hook call writes the store, so one snapshot is
// what those four reads already agreed on. A workspace spawn rule reads the same pin
// through magus\job.list.
func withJobStoreRows(ctx context.Context, at location) context.Context {
	if at.cacheDir == "" {
		return ctx
	}
	rows, err := listJobRows(job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}))
	return job.WithSnapshot(ctx, job.Snapshot{Rows: rows, Err: err})
}

// jobRowsMemoTTL bounds how long a memoized sweep is trusted. The sweep also ends rows for
// reasons no write to the store announces: a declared row aging past jobs.stale_after, a
// checkout directory being removed.
const jobRowsMemoTTL = 10 * time.Second

// jobRowsRacy is how recently a store file may have changed and still not be memoized: a
// second write inside one modification-time tick leaves the stamp the memo was keyed on.
const jobRowsRacy = time.Second

// jobRowsMemoFile sits beside jobs.json, so the memo follows the repository's store
// rather than one checkout's cache.
const jobRowsMemoFile = "guard-rows.json"

// jobRowsMemo is the rows one List returned, stamped with the store file it read.
type jobRowsMemo struct {
	Schema int         `json:"schema"`
	Size   int64       `json:"size"`
	ModNS  int64       `json:"mod_ns"`
	AtNS   int64       `json:"at_ns"`
	Rows   []types.Job `json:"rows"`
}

// fresh reports whether m holds the rows of the store file info describes, as of now.
func (m jobRowsMemo) fresh(info os.FileInfo, now time.Time) bool {
	age := now.Sub(time.Unix(0, m.AtNS))
	return m.Schema == types.JobSchemaVersion && m.Size == info.Size() && m.ModNS == info.ModTime().UnixNano() &&
		age >= 0 && age < jobRowsMemoTTL
}

// listJobRows is store.List, except that a call whose store file is unchanged since a
// sweep that ended nothing, within jobRowsMemoTTL, returns that sweep's rows.
//
// optimization: skip the sweep and its O(rows^2) ancestor walk when jobs.json is unchanged.
//
//	measured: BenchmarkWithJobStoreRows 300 rows -87% ns/op, -89% allocs/op (benchstat, n=10).
//	trade-off: a row that becomes dead by the clock or by a removed checkout is ended up to
//	jobRowsMemoTTL later than an unmemoized read would end it.
//	assumes:  a store rewrite changes the file's size or modification time; racy stamps are
//	never memoized.
//
// A memo is written only when the store file is the same before and after the List, which
// is how it is known nothing was ended and no writer interleaved. Any memo problem falls
// back to List: the memo can cost a read, never a verdict.
func listJobRows(store *job.Store) ([]types.Job, error) {
	path, err := store.Path()
	if err != nil {
		return store.List()
	}
	before, err := os.Stat(path)
	if err != nil {
		return store.List()
	}
	memoPath := filepath.Join(filepath.Dir(path), jobRowsMemoFile)
	now := time.Now()
	if raw, err := os.ReadFile(memoPath); err == nil {
		var memo jobRowsMemo
		if json.Unmarshal(raw, &memo) == nil && memo.fresh(before, now) {
			return memo.Rows, nil
		}
	}
	rows, err := store.List()
	if err != nil {
		return rows, err
	}
	after, err := os.Stat(path)
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) ||
		now.Sub(before.ModTime()) < jobRowsRacy {
		return rows, nil
	}
	memo := jobRowsMemo{Schema: types.JobSchemaVersion, Size: before.Size(), ModNS: before.ModTime().UnixNano(), AtNS: now.UnixNano(), Rows: rows}
	if raw, err := json.Marshal(memo); err == nil {
		_ = file.WriteFileAtomic(memoPath, raw, 0o644)
	}
	return rows, nil
}

// leaseRows reports the pinned job store, reading it when nothing pinned one. A test that
// calls a single rule gets its own read, which is what every rule used to do.
func leaseRows(ctx context.Context, at location) ([]types.Job, error) {
	if pinned, ok := job.SnapshotFromContext(ctx); ok {
		return pinned.Rows, pinned.Err
	}
	return job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}).List()
}

// copySessionState copies into a new temporary directory what the gates keyed on keys hold
// under cacheDir, and the served-next journal, keeping modification times, since an
// anonymous marker expires on its age. "" when there is nothing to copy or the copy cannot
// be made: a dry run then reads a session nothing was told yet, rather than write the real one.
func copySessionState(cacheDir string, keys ...string) string {
	if cacheDir == "" {
		return ""
	}
	dir, err := os.MkdirTemp("", "magus-guard-dry-run-")
	if err != nil {
		return ""
	}
	copyFile := func(src, dst string) error {
		info, err := os.Stat(src)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		body, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			return err
		}
		return os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	copyAll := func() error {
		if err := copyFile(hint.ServedNextPath(cacheDir), hint.ServedNextPath(dir)); err != nil {
			return err
		}
		for _, key := range keys {
			from, to := hint.MarkerPath(cacheDir, key, ""), hint.MarkerPath(dir, key, "")
			entries, _ := os.ReadDir(filepath.Dir(from))
			for _, e := range entries {
				if !strings.HasPrefix(e.Name(), filepath.Base(from)) {
					continue
				}
				if err := copyFile(filepath.Join(filepath.Dir(from), e.Name()), filepath.Join(filepath.Dir(to), e.Name())); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if copyAll() != nil {
		_ = os.RemoveAll(dir)
		return ""
	}
	return dir
}

// withRegisteredBase is registerAgentBase for a check: the row is registered in the pinned
// snapshot only. Its base verdict stays unset, so a diverged base is not reported.
func withRegisteredBase(ctx context.Context, deps Dependencies, at location, agentJob string) context.Context {
	snap, ok := job.SnapshotFromContext(ctx)
	if !ok || snap.Err != nil {
		return ctx
	}
	i := slices.IndexFunc(snap.Rows, func(row types.Job) bool { return row.ID == agentJob })
	if i < 0 || !snap.Rows[i].State.Live() || snap.Rows[i].Registered != 0 {
		return ctx
	}
	base := deps.checkoutBase(ctx, at.workspace)
	if base == "" {
		return ctx
	}
	snap = snap.Clone()
	row := &snap.Rows[i]
	row.Registered, row.ReportedBase, row.CheckoutRoot = time.Now().Unix(), base, ""
	if abs, err := filepath.Abs(at.workspace); err == nil && at.workspace != "" {
		row.CheckoutRoot = abs
	}
	return job.WithSnapshot(ctx, snap)
}

// WithLocation pins the cache directory, workspace root and calling directory this hook
// call is graded against.
//
// The test seam, and the one the rules have always had: a guard unit test must never write
// its checkout's real activity trail. It is exported because the command that renders a
// verdict now lives in another package and its tests need the same pin.
func WithLocation(ctx context.Context, cacheDir, workspace, dir string) context.Context {
	return context.WithValue(ctx, locationKey{},
		location{cacheDir: cacheDir, workspace: workspace, dir: dir})
}

// appendHookActivity contributes a best-effort, normalized observation to the same durable
// trail used by MCP and server actions. It deliberately runs before rendering the guard response:
// the host may choose not to execute a denied command, and a pre-hook never learns the eventual
// exit status. An audit failure must therefore be invisible to both the verdict and the command.
//
// lease is the acting lease the verdict was graded under, marker included: the trail's own
// fallback reads only the environment, which a host's hook never inherits, so without it a
// marker-bound worker's observations would carry no lease and join nothing.
//
// preauth is the `next` template that had already served this command, and it is recorded
// because a clearance nobody counts is a clearance nobody can audit: uptake per template is
// the number that decides whether a breadcrumb is reworded or deleted.
//
// rule is how the workspace command or write rule judged, which alone knows whether its answer came
// from the approved side or the working tree.
func appendHookActivity(ctx context.Context, location location, input string, who hookAttribution, tool, lease, preauth, verdictRef, policyDigest string, verdict Verdict, rule workspaceRuleRecord) {
	if input == "" || location.cacheDir == "" {
		return
	}
	command := trail.AgentCommand{
		PolicyDigest: policyDigest,
		DecidedBy:    cmp.Or(rule.decidedBy, decidedBy(verdict)),
		RuleFailures: rule.failures,
		// A call typed at a terminal entered through the CLI, not a hook.
		EntryPoint:      trail.EntryPointFromContext(ctx),
		Workspace:       location.workspace,
		Host:            who.Host,
		Session:         who.Session,
		Agent:           who.Agent,
		Transcript:      who.Transcript,
		Event:           who.Event,
		Tool:            tool,
		Lease:           lease,
		LeaseFrom:       verdict.LeaseFrom,
		PreauthorizedBy: preauth,
		Decision:        verdict.Decision,
		Reason:          verdict.Reason,
		Context:         verdict.Context,
		Rule:            verdict.Rule,
		StdinClosed:     verdict.UpdatedCommand != "",
		VerdictRef:      verdictRef,
	}
	if tool == hookToolCommand {
		command.Command = input
	} else {
		command.Path = input
	}
	trail.AppendAgentCommand(ctx, location.cacheDir, command)
}

// spawnVerdictRecord is what the trail keeps about how a spawn or continuation was judged.
type spawnVerdictRecord struct {
	policyDigest string
	decidedBy    string
	// target is the agent a continuation addresses, resolved to its id when magus knows it.
	target string
	// ruleFailures are the workspace spawn rules that judged nothing, and why.
	ruleFailures []trail.RuleFailure
}

// appendHookSpawn records a spawn or a continuation into the same trail, so a person
// auditing the activity log later can see WHAT CONTEXT an orchestrator handed a sub-agent,
// not merely that it spawned one. Like appendHookActivity it is best-effort and cannot fail
// the tool call.
func appendHookSpawn(ctx context.Context, deps Dependencies, req hookRequest, who hookAttribution, rec spawnVerdictRecord) {
	if req.Value == "" {
		return
	}
	location := hookLocation(ctx, deps)
	if location.cacheDir == "" {
		return
	}
	trail.AppendAgentSpawn(ctx, location.cacheDir, trail.AgentSpawn{
		PolicyDigest:  rec.policyDigest,
		DecidedBy:     rec.decidedBy,
		Continue:      req.IsContinue,
		Target:        rec.target,
		RuleFailures:  rec.ruleFailures,
		Workspace:     location.workspace,
		EntryPoint:    trail.EntryPointFromContext(ctx),
		Host:          who.Host,
		Session:       who.Session,
		Agent:         who.Agent,
		Event:         who.Event,
		Tool:          req.Tool,
		Child:         req.Child,
		Context:       req.Value,
		DeclaredModel: req.DeclaredModel,
	})
}

// hookLocation resolves the local workspace cache because a hook runs as a short-lived
// client process, outside the server's memory. Tests can pin a temporary base through context so
// a guard unit test never writes its checkout's real activity trail; hookContextAt pins the
// checkout a host's envelope named the same way.
func hookLocation(ctx context.Context, deps Dependencies) location {
	if location, ok := ctx.Value(locationKey{}).(location); ok {
		return location
	}
	return hookLocationAt(deps, "")
}

// hookLocationAt resolves the workspace holding dir, or the process cwd for "".
// The process cwd's workspace is the one the caller's config was loaded for, so its
// resolved config applies; another checkout reads its own magus.yaml, because a cache dir
// configured in the orchestrator's tree says nothing about where a worker's cache lives.
func hookLocationAt(deps Dependencies, dir string) location {
	root, err := magus.FindRoot(dir)
	if err != nil {
		return location{}
	}
	ask := root
	if dir == "" {
		ask = ""
	}
	cacheDir, err := deps.cacheDir(ask)
	if err != nil {
		return location{}
	}
	if dir == "" {
		// The process cwd, which for "" is what root was found from. Read rather than
		// assumed to be the root: a session opened in a subdirectory is exactly the
		// case focus exists for, and collapsing it to the root would hide it.
		if wd, wderr := os.Getwd(); wderr == nil {
			dir = wd
		}
	}
	return location{cacheDir: cacheDir, workspace: root, dir: dir}
}

// hookContextAt pins the trail location to the checkout holding cwd, the directory the host
// reported its tool call runs in. A host runs its hooks from wherever it likes, and the
// process cwd is then the orchestrator's tree rather than the worker's: the marker bound
// with `magus session lease` lives in the worker's checkout, so the envelope's cwd is the
// only thing that finds it. A location already pinned (a test's) wins, and a cwd magus
// cannot resolve to a workspace changes nothing. A relative cwd is ignored rather than
// resolved against the hook process, whose directory is the thing it must not stand for.
func hookContextAt(ctx context.Context, deps Dependencies, cwd string) context.Context {
	if !filepath.IsAbs(cwd) {
		return ctx
	}
	if _, pinned := ctx.Value(locationKey{}).(location); pinned {
		return ctx
	}
	location := hookLocationAt(deps, cwd)
	if location.cacheDir == "" {
		return ctx
	}
	return context.WithValue(ctx, locationKey{}, location)
}
