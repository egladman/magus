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
	"github.com/egladman/magus/internal/graph/knowledge"
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
	// CheckoutState reads the checkout holding dir for a command rule judging a push, nil
	// when its version control cannot report it.
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
	// HeadCommit is this checkout's current revision, abbreviated, or "" when there is no
	// VCS to ask. The push gate matches it against the commit each recorded gate run was
	// built from; with no answer that rule stands down rather than refusing on an absence.
	HeadCommit func(ctx context.Context) string
	// CheckoutBase is the checkout at root as `magus vcs checkpoint -o name` prints it:
	// `<rev>`, or `<rev>+<digest>` when dirty. "" when there is no VCS to ask. It is the
	// base an attributed spawn records for its job, the value `magus job exec` records.
	CheckoutBase func(ctx context.Context, root string) string
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

func (d Dependencies) headCommit(ctx context.Context) string {
	if d.HeadCommit == nil {
		return ""
	}
	return d.HeadCommit(ctx)
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

// Request is one call the guard was asked to judge: the payload, plus what the caller's
// own flags said about it. A host envelope arriving as Input still answers the same
// questions, and an explicit flag wins over what the envelope implies.
type Request struct {
	Input  string
	IsPath bool
	// Observe records the input as a path the agent REACHED and judges nothing.
	Observe bool
	// Lease is the job row this call acts as; empty falls back to the bound marker.
	Lease string
	// The attribution the caller knows about itself. No verdict reads any of it.
	Host string
	// Transport is the form of the installed hook that called, such as sh or buzz, as
	// that form declares it. Two forms wired into one session are two callers.
	Transport  string
	Session    string
	Transcript string
	Event      string
	// ObservesSkillLoads is the one CAPABILITY on this struct rather than attribution: the
	// host's wiring reports skill loads to magus, so a rule may require one. It is set by
	// the wiring that provides the observation, never inferred from Host, because guard
	// code may not branch on a host's name and a name would not prove the wiring anyway.
	//
	// False is the safe answer: rules that need it stand down, which is what keeps them
	// from denying forever on a host that can never satisfy them.
	ObservesSkillLoads bool
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
	// Rule names the stable rule or advisory that produced this verdict, when it can
	// identify itself. Host adapters still render Reason and Context; this is what a
	// PERSON looks up, reports as a false positive, or greps a trail for, and the text
	// arm prints it beside the decision for exactly that reason.
	//
	// Empty is an honest answer, not a gap to paper over: several path advisories are
	// heuristics with no marker kind of their own, and inventing a slug for one would
	// promise a catalog entry that does not exist.
	Rule string `json:"rule,omitempty"`
	// Lease is the row this verdict was graded under, empty when the call named none.
	//
	// A session bound to a typo'd id, a session bound to a finished row, and a session
	// nobody leased are otherwise indistinguishable, and two of the three run unguarded.
	// An added optional field is not a schema bump: a glue that does not read it is
	// unaffected, which is the rule agent.GuardSchemaVersion states.
	Lease string `json:"lease,omitempty"`
}

// Judge evaluates one request against this workspace's rules and reports the verdict.
//
// An EMPTY input passes: a wrapper that hands the hook nothing must not have every tool
// call blocked. The caller owns the opposite case, a payload that failed to READ, because
// nothing here saw it.
func Judge(ctx context.Context, deps Dependencies, req Request) Verdict {
	input := req.Input
	hasInput := input != ""
	who := hookAttribution{Host: req.Host, Transport: req.Transport, Session: req.Session, Transcript: req.Transcript, Event: req.Event}
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
		who.Agent = env.Who.Agent
		if env.LoadedSkill != "" {
			// Recorded, never judged. The gate is built here rather than reusing the one
			// below because this arm returns before it: same cacheDir, same session.
			recordSkillLoad(hint.NewGate(hookLocation(ctx, deps).cacheDir, who.sessionKey()), env.LoadedSkill)
			return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
		}
		if env.NothingToJudge {
			// A host envelope whose tool_input carries no command, path or prompt (a todo
			// list, a search) has nothing any rule can read. Falling through judged the raw
			// JSON as a shell line, so a denied command merely NAMED inside a todo blocked
			// the tool call that wrote the todo.
			if env.AgentTranscript != "" {
				recordAgentUsage(hint.NewGate(hookLocation(ctx, deps).cacheDir, who.sessionKey()), who.Agent, env.AgentTranscript)
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
			return judgeAgentEvent(ctx, deps, req, env, who)
		}
	}
	// One gate for the whole invocation, holding each enrolled advisory to one firing per
	// session. Built AFTER the envelope is decoded: a host that reports its session id only
	// inside the payload would otherwise be graded as having reported none, and every
	// session on that host would share the anonymous bucket. The acting lease is resolved
	// here for the same reason: the envelope's cwd is what locates the worker's marker.
	// An explicit --lease wins; otherwise the same resolution the sandbox applies, so the
	// two tiers cannot disagree about who is acting (see job.LeaseMarkerName).
	location := hookLocation(ctx, deps)
	policyDigest := recordPolicy(ctx, deps, location, false)
	ctx = withJobStoreRows(ctx, location)
	markers := hint.NewGate(location.cacheDir, who.callerKey())
	facts := hint.NewGate(location.cacheDir, who.sessionKey())
	actingLease := actingLeaseFor(who, location, facts, req.Lease)
	tool := hookToolCommand
	switch {
	case req.Observe:
		tool = hookToolRead
	case isPath:
		tool = hookToolWrite
	}
	verdict := Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass", Lease: actingLease}
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
		}
	}
	// A served next is magus's own suggestion, and the guard does not argue with it: no
	// advisory fires on it, and the role-scoped rules stand down. The workspace-wide
	// denies do not, and they are the ones whose reasons say why (see internal/guard/preauth.go).
	var ruleRecord workspaceRuleRecord
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
			verdict.Decision, verdict.Reason = "deny", reason
		}
		denyUndeclared("")
		if verdict.Decision != "deny" {
			switch g := gradeLeasedWrite(ctx, deps, actingLease, input); g.Decision {
			case "deny":
				verdict.Decision = "deny"
				verdict.Reason = g.Reason
			case "advise":
				advice, adviceKind, spoken = markers.Once(g.Kind, g.Context), g.Kind, true
			}
		}
		if verdict.Decision != "deny" {
			switch g := gradeHookWiringWrite(actingLease, input); g.Decision {
			case "deny":
				verdict.Decision, verdict.Reason = "deny", g.Reason
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
		// The generated-output rule is definitive (it reads declared globs), so it
		// outranks the heuristics below; the memory nudge is a heuristic on the
		// filename and only fills the silence it leaves.
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
			}
		}
		if verdict.Decision == "pass" && !spoken {
			if text := adviseInstalledSkillWrite(input); text != "" {
				advice, adviceKind, spoken = text, advisoryInstalledSkill, true
			}
		}
		if verdict.Decision == "pass" && !spoken {
			if text := adviseMemoryWrite(input); text != "" {
				advice, adviceKind, spoken = text, advisoryMemoryWrite, true
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
		verdict, ruleRecord = gradeWorkspaceWrite(ctx, deps, verdict, input, write, actingLease, who, location)
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
		v := rankOwnBuild(evaluateWith(deps, input, hookSearchHints(location.cacheDir)), ownBuildVerdict(deps, callDir, input, shellD))
		v = rankSiblingCheckout(v, denySiblingCheckout(input, shellD))
		v = rankInterpreterRewrite(v, denyInterpreterRewrite(location, input, shellD))
		v = rankCacheDirWrite(v, denyCacheDirCommand(location, input, shellD))
		// Outside Evaluate for the same reason the two rules above are: it reads session
		// state (which skills have loaded) rather than the line alone, and Evaluate's
		// verdict is a pure function of what was handed in.
		switch v = rankBuzzAuthor(v, denyBuzzAuthorWithoutSkill(facts, req.ObservesSkillLoads, location.workspace, input, shellD)); {
		case v.Deny != "":
			// These are the denies that hold for everyone, so a pre-authorization does not
			// reach them: whole-tree VCS, a pipe or redirect of magus's own output, a raw
			// language tool, a relocated checkout. A next magus served would not carry one
			// anyway, and the structural test is what says so before it ships.
			verdict.Decision = "deny"
			verdict.Reason = v.Deny
			verdict.Rule = v.RuleName()
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
		for _, rule := range []func(context.Context, Dependencies, string, string) string{denyLeaseScopedGate, denyLeaseScopedVCS, denyLeaseScopedRebind, denyWriteOutsideLease} {
			if verdict.Decision == "deny" || preauth != "" {
				break
			}
			if reason := rule(ctx, deps, actingLease, input); reason != "" {
				verdict.Decision, verdict.Reason, verdict.Context = "deny", reason, ""
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
			commit := deps.headCommit(ctx)
			cover := gateVerdictAt(location.workspace, commit)
			if cover == gateUnknown {
				cover = gateCoverageAt(workspaceRunsDir(location.cacheDir), commit)
			}
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
		}, who, location)
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
	// Worded after every arm has spoken, so whichever rule refused is the one held to a
	// full explanation per session. An ask is never shortened: it waits on a person.
	verdictRef := ""
	if verdict.Decision == "deny" && verdict.Rule != "" && !req.Observe {
		note := ""
		if tool == hookToolCommand {
			note = nothingRanNote(input, effectiveDialect(deps.ShellDialect))
		}
		verdict.Reason, verdictRef = shapeDeny(ctx, markers, verdict.Rule, verdict.Reason, note)
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
	appendHookActivity(ctx, location, input, who, tool, actingLease, preauth, verdictRef, policyDigest, record, ruleRecord)
	return verdict
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
// TestNoHostSpecificBehaviorInCode matches host NAMES, so a switch over "Read"/"Bash" (a
// per-host branch in everything but spelling) passes it untouched.
// TestGuardDoesNotBranchOnHostToolVocabulary is the layer that catches that one: a host's
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
// A payload carrying a PROMPT rather than either is a spawn: it is RECORDED and EXEMPT from
// judgment. There is no command and no path to judge, only a context transfer to note, and a
// prompt that merely MENTIONS a denied command would otherwise block the spawn describing it.
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
		// being present: requiring `op` left nineteen of magus's twenty-one tools,
		// magus_run_target and magus_run_affected among them, reaching no rule at all.
		req.Value = renderMCPCall(tool, env.ToolInput)
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
			Content: envelopeString(env.ToolInput, "content"),
			OldText: envelopeString(env.ToolInput, "old_string"),
			NewText: envelopeString(env.ToolInput, "new_string"),
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
	Host       string
	Transport  string
	Session    string
	Transcript string
	Event      string
	// Agent is the subagent making the call, "" for a root session or a host that does
	// not say.
	Agent string
}

// callerKeyEscaper keeps the key's delimiter out of every part. '%' is escaped too, so a
// part that already holds "%2F" cannot collide with one that held "/".
var callerKeyEscaper = strings.NewReplacer("%", "%25", "/", "%2F")

// sessionKey keys FACTS about a session, what a rule reads as "did this happen": a skill
// load, the projects written. `<host>/<session>`, each part escaped, because two hosts may
// present the same id. The transport is left out so a session wiring some surfaces as sh
// and others as Buzz sees one set of facts. Empty without a session, so hint.Gate falls
// back to its anonymous window rather than keying every unattributed caller together.
func (who hookAttribution) sessionKey() string { return SessionKey(who.Host, who.Session) }

// SessionKey is the marker key the guard files a session's facts under, for a reader
// outside the package: `<host>/<session>` with each part escaped, or "" without a session.
func SessionKey(host, session string) string {
	session = strings.TrimSpace(session)
	if session == "" {
		return ""
	}
	return callerKeyEscaper.Replace(host) + "/" + callerKeyEscaper.Replace(session)
}

// callerKey keys TEXT a caller has already rendered, a fire-once notice or a deny's full
// reason: `<host>/<transport>/<session>`. The sh and Buzz forms of one hook are two
// readers of their own replies, so each is told a rule in full once. Empty without a
// session, like sessionKey.
func (who hookAttribution) callerKey() string {
	session := strings.TrimSpace(who.Session)
	if session == "" {
		return ""
	}
	return callerKeyEscaper.Replace(who.Host) + "/" + callerKeyEscaper.Replace(who.Transport) + "/" + callerKeyEscaper.Replace(session)
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
	rows, err := job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}).List()
	return job.WithSnapshot(ctx, job.Snapshot{Rows: rows, Err: err})
}

// leaseRows reports the pinned job store, reading it when nothing pinned one. A test that
// calls a single rule gets its own read, which is what every rule used to do.
func leaseRows(ctx context.Context, at location) ([]types.Job, error) {
	if pinned, ok := job.SnapshotFromContext(ctx); ok {
		return pinned.Rows, pinned.Err
	}
	return job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}).List()
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
// trail used by MCP and daemon actions. It deliberately runs before rendering the guard response:
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
		PolicyDigest:    policyDigest,
		DecidedBy:       cmp.Or(rule.decidedBy, decidedBy(verdict)),
		RuleFailures:    rule.failures,
		Actor:           "agent",
		Workspace:       location.workspace,
		Host:            who.Host,
		Session:         who.Session,
		Transcript:      who.Transcript,
		Event:           who.Event,
		Tool:            tool,
		Lease:           lease,
		PreauthorizedBy: preauth,
		Decision:        verdict.Decision,
		Reason:          verdict.Reason,
		Context:         verdict.Context,
		Rule:            verdict.Rule,
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
		Actor:         "agent",
		Workspace:     location.workspace,
		Host:          who.Host,
		Session:       who.Session,
		Event:         who.Event,
		Tool:          req.Tool,
		Child:         req.Child,
		Context:       req.Value,
		DeclaredModel: req.DeclaredModel,
	})
}

// hookSearchHints builds the search translator scoped to the projects the
// knowledge manifest records, so a caught search can be answered with a
// project=-scoped query. An absent or unreadable manifest yields the unscoped
// default, identical to a workspace that never built a graph.
func hookSearchHints(cacheDir string) *hint.Translator {
	if cacheDir == "" {
		return searchHints
	}
	paths := knowledge.ProjectPaths(cacheDir)
	if len(paths) == 0 {
		return searchHints
	}
	return hint.NewTranslator(hint.WithProjects(paths))
}

// hookLocation resolves the local workspace cache because a hook runs as a short-lived
// client process, outside the daemon's memory. Tests can pin a temporary base through context so
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
