// Package guard holds the agent guard: the rules that judge one shell command, or one
// file path an edit is about to write, and answer with a deny, an advisory, or a pass.
//
// It lives outside cmd/magus so the rules are reachable from the surfaces that have to
// agree with them (the CLI hook, the MCP door, the dogfood tests), rather than restated
// in each. Everything host-specific stays in the caller: this package never reads a flag,
// a host's tool vocabulary, or the display options a verdict is rendered with.
package guard

import (
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

func (d Dependencies) spells() []*spells.Spell {
	if d.Spells == nil {
		return nil
	}
	return d.Spells()
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
	Host       string
	Session    string
	Transcript string
	Event      string
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
	who := hookAttribution{Host: req.Host, Session: req.Session, Transcript: req.Transcript, Event: req.Event}
	isPath := req.IsPath
	// A host that writes its hook payload as JSON needs no jq and no --path: the envelope
	// says what is about to run and whether it is a write. Explicit flags still win, since
	// a wrapper that passed them meant them.
	if env, isEnvelope := decodeHookEnvelope(input); isEnvelope {
		if env.NothingToJudge {
			// A host envelope whose tool_input carries no command, path or prompt (a todo
			// list, a search) has nothing any rule can read. Falling through judged the raw
			// JSON as a shell line, so a denied command merely NAMED inside a todo blocked
			// the tool call that wrote the todo.
			return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
		}
		input = env.Value
		hasInput = input != ""
		if env.IsPath {
			isPath = true
		}
		ctx = hookContextAt(ctx, deps, env.Cwd)
		if who.Session == "" {
			who.Session = env.Who.Session
		}
		if who.Transcript == "" {
			who.Transcript = env.Who.Transcript
		}
		if who.Event == "" {
			who.Event = env.Who.Event
		}
		if env.IsSpawn {
			// A spawn carries no verdict, so it returns the pass every other
			// non-finding does and never reaches the guard. Handled here rather than
			// beside the two guard arms because the whole point is that nothing judges
			// it: the handed context is prose, and a prompt that merely MENTIONS a
			// denied command would otherwise block the spawn that describes it.
			appendHookSpawn(ctx, deps, env, who)
			return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
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
	ctx = withJobStoreRows(ctx, location)
	actingLease := req.Lease
	if actingLease == "" {
		actingLease = job.ActingLease(location.cacheDir)
	}
	markers := hint.NewGate(location.cacheDir, who.Session)
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
	// call, and the stale-binary notice at the tail reaches this verdict like any other.
	// A pre-authorization does not stand it down either, since an id nobody declared means
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
		// Graded ahead of the rules, though it speaks near the end of them: the project
		// this write lands in is recorded whatever verdict they reach, so it cannot be
		// resolved inside a rung that a louder rule skips.
		drift := gradeScopeDrift(ctx, deps, markers, actingLease, input)
		// spoken reports that a rule MATCHED, which is not the same as a rule that
		// produced text. A once-per-session advisory that already fired this session
		// matched and stayed quiet, and the rules below it must not step into the silence
		// it left: without this the second write to a skill source would draw the
		// new-directory advisory instead of nothing.
		spoken := false
		// The checkout's own cache dir speaks before the job store, and it is the only rule
		// that does. Every lease-scoped verdict below is computed from files in there, so
		// a worker whose lane happens to cover the dir must not be told it owns the lane:
		// what it is editing is whether the lane was checked.
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
				advice, spoken = markers.Once(g.Kind, g.Context), true
			}
		}
		if verdict.Decision != "deny" {
			switch g := gradeHookWiringWrite(actingLease, input); g.Decision {
			case "deny":
				verdict.Decision, verdict.Reason = "deny", g.Reason
			case "advise":
				if !spoken {
					advice, spoken = markers.Once(g.Kind, g.Context), true
				}
			}
		}
		// The generated-output rule is definitive (it reads declared globs), so it
		// outranks the heuristics below; the memory nudge is a heuristic on the
		// filename and only fills the silence it leaves.
		if verdict.Decision == "pass" && !spoken {
			if text := adviseGeneratedWrite(ctx, deps, input); text != "" {
				advice, spoken = text, true
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
				advice, spoken = text, true
			}
		}
		if verdict.Decision == "pass" && !spoken {
			if text := adviseMemoryWrite(input); text != "" {
				advice, spoken = text, true
			}
		}
		// Both of these are inert outside magus's own checkout; see magusOwnSourceTree.
		if verdict.Decision == "pass" && !spoken {
			if text := adviseAgentSurfaceWrite(input); text != "" {
				advice, spoken = markers.Once(advisorySkillSource, text), true
			}
		}
		if verdict.Decision == "pass" && !spoken {
			if text := adviseDescriptorWrite(input); text != "" {
				advice, spoken = markers.Once(advisoryRegenSource, text), true
			}
		}
		// Above the new-directory rule because it is the wider question: whether this
		// write belongs in this session at all outranks how its unit is laid out.
		if verdict.Decision == "pass" && !spoken && drift.advice != "" {
			advice, spoken = drift.advice, true
		}
		// Mutually exclusive with the rung below: that one answers an empty directory,
		// this one a populated one. Held to one firing per session, where the new-directory
		// rule is not, because creating a file is ordinary work and creating a boundary
		// is not (internal/guard/file.go).
		if verdict.Decision == "pass" && !spoken {
			if text := adviseNewFileName(input); text != "" {
				advice, spoken = markers.Once(advisoryNewFile, text), true
			}
		}
		// Last rung, so it sets no flag: there is nothing below it to hold back.
		if verdict.Decision == "pass" && !spoken {
			advice = adviseNewSourceDir(input)
		}
		if verdict.Decision == "pass" && advice != "" {
			verdict.Decision = "advise"
			verdict.Context = advice
		}
		// A denied write never happens, so it never touched anything.
		if verdict.Decision != "deny" {
			drift.record()
		}
	default:
		// The sibling-checkout and cache-dir rules read the FILESYSTEM, so neither can
		// live inside Evaluate's pure rule set; ranking them is pure, and is
		// where the ordering is tested. The cache dir is outermost: what it refuses
		// outranks every other deny on the line (internal/guard/cachedir.go).
		v := rankSiblingCheckout(evaluateWith(deps, input, hookSearchHints(location.cacheDir)), denySiblingCheckout(input))
		v = rankInterpreterRewrite(v, denyInterpreterRewrite(location, input))
		switch v = rankCacheDirWrite(v, denyCacheDirCommand(location, input)); {
		case v.Deny != "":
			// These are the denies that hold for everyone, so a pre-authorization does not
			// reach them: whole-tree VCS, a pipe or redirect of magus's own output, a raw
			// language tool, a relocated checkout. A next magus served would not carry one
			// anyway, and the structural test is what says so before it ships.
			verdict.Decision = "deny"
			verdict.Reason = v.Deny
		case v.Context != "" && preauth == "":
			if held := markers.OnceOrBrief(v.Kind, v.Context, v.Brief); held != "" {
				verdict.Decision = "advise"
				verdict.Context = held
			}
		}
		denyUndeclared(input)
		// The job store's half of the command surface, ranked BELOW the rules above
		// (a sibling checkout's gate is the wrong tree before it is the wrong scope).
		// Every one is ROLE-scoped, which is what a pre-authorization stands down: the
		// command came from magus, computed for this role, so refusing it here would be
		// the tool disagreeing with itself.
		for _, rule := range []func(context.Context, Dependencies, string, string) string{denyLeaseScopedGate, denyLeaseScopedVCS, denyLeaseScopedRebind, denyLeaseScopedLaneWrite} {
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
					verdict.Decision, verdict.Context = "advise", held
				}
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
			}
		}
		// The guard's half of the index-staleness fact; the load-bearing half rides the
		// command's own output (staleindex.go). alreadyFired is asked BEFORE the rule, not
		// after: producing this text costs a directory walk, and once the session has been
		// told, paying for it again only to discard the answer is the cost nobody sees.
		if verdict.Decision == "pass" && preauth == "" && !markers.AlreadyFired(advisoryGraphStale) && commandReadsGraph(input) {
			if notice := markers.Once(advisoryGraphStale, deps.graphStaleAdvice(ctx)); notice != "" {
				verdict.Decision = "advise"
				verdict.Context = notice
			}
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
		if notice == "" || req.Observe || verdict.Decision == "deny" {
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
		verdict.Decision, verdict.Context = "advise", held
	}
	// Said last and on EVERY surface: a stale binary's verdicts are all suspect, not
	// just the ones that matched a rule.
	//
	// A deny most of all. That is the verdict the caller cannot see past, so a block
	// from rules they have already changed is the case this rule exists for, and the
	// first version of it skipped exactly that arm. The reason comes first, because
	// the block has to be explained before it can be doubted.
	//
	// It is the loudest of the repeated advisories and so the one held to once per
	// session, EXCEPT on a deny, where it is appended every time and spends no firing.
	// A denial explains itself in full whenever it refuses, and this is the sentence that
	// says the refusal may be coming from rules the caller has already changed.
	if notice := staleGuardNotice(); notice != "" && !req.Observe {
		if verdict.Decision == "deny" {
			verdict.Reason += "\n\n" + notice
		} else if held := markers.Once(advisoryStaleBinary, notice); held != "" {
			if verdict.Decision == "advise" {
				verdict.Context += "\n\n" + held
			} else {
				verdict.Decision, verdict.Context = "advise", held
			}
		}
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
	appendHookActivity(ctx, location, input, who, tool, actingLease, preauth, record)
	return verdict
}

// hookEnvelope is the JSON an agent host writes to a hook's stdin: which tool is about to
// run and with what. Only the fields the guard needs are modeled; everything else in the
// payload is ignored rather than rejected, since a host is free to add to it.
type hookEnvelope struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	// Cwd is the directory the host reports the tool call runs in. It is what locates the
	// WORKER's checkout when the host runs its hooks somewhere else, such as the
	// orchestrator's directory, and with it the lease marker bound there.
	Cwd string `json:"cwd"`
	// TranscriptPath is the host's own log of this session. Recorded as a pointer so a
	// session id in the activity view leads somewhere; magus never reads the file.
	TranscriptPath string `json:"transcript_path"`
	ToolName       string `json:"tool_name"`
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
		Session:    env.SessionID,
		Transcript: env.TranscriptPath,
		Event:      env.HookEventName,
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
	case envelopeWritePath(env.ToolInput) != "":
		req.Value, req.IsPath = envelopeWritePath(env.ToolInput), true
	case envelopeString(env.ToolInput, "prompt") != "":
		req.Value, req.IsSpawn = envelopeString(env.ToolInput, "prompt"), true
		req.Tool = env.ToolName
		// Most specific label first. A sub-agent TYPE names what was delegated to and repeats
		// across spawns, so it groups a spawn feed; a description is per-spawn prose; the
		// tool name is the last resort that at least says a spawn happened.
		for _, label := range []string{
			envelopeString(env.ToolInput, "subagent_type"),
			envelopeString(env.ToolInput, "description"),
			env.ToolName,
		} {
			if label != "" {
				req.Child = label
				break
			}
		}
	default:
		// A payload that identifies itself as a host hook is an envelope even when its
		// tool_input holds nothing this guard reads. Reporting "not an envelope" here sent
		// the raw JSON to the shell rules, which read a denied command quoted inside a todo
		// or a search string as the command about to run and blocked it.
		//
		// Keyed on the envelope's OWN fields, so a bare `{"tool_input":{}}` (which names no
		// host event and could be anything) still falls through to the literal form.
		if env.HookEventName == "" && env.ToolName == "" && env.SessionID == "" {
			return hookRequest{}, false
		}
		req.NothingToJudge = true
	}
	return req, true
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
	return env.SessionID, env.TranscriptPath
}

// hookRequest is what a host's payload asked the guard to judge: the text, whether it is a
// path rather than a command, and who reported it. A spawn asks for nothing to be judged: it
// carries the handed context and the callee's label, and is recorded rather than evaluated.
type hookRequest struct {
	Value  string
	IsPath bool
	// Cwd is where the host says the call runs; "" when the envelope carried none.
	Cwd string
	// NothingToJudge is a recognized host envelope carrying no command, path or prompt.
	// Distinct from "not an envelope", which is judged as the literal text it is.
	NothingToJudge bool
	IsSpawn        bool
	Tool           string
	Child          string
	Who            hookAttribution
}

// hookAttribution is what the host wrapper knows about itself and cannot be
// derived here: a hook runs as a short-lived client process with no way to
// discover which agent host started it. It travels beside the input rather than
// inside the judged text because the guard's verdict must never depend on it.
type hookAttribution struct {
	Host       string
	Session    string
	Transcript string
	Event      string
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

type jobStoreRowsKey struct{}

// jobStoreRows is the job store as one call read it, error included: an unreadable
// store and a store with no rows are different facts, and only the first means a rule
// could not be evaluated at all.
type jobStoreRows struct {
	rows []types.Job
	err  error
}

// withJobStoreRows reads the job store once and pins it for the rules below.
//
// Four of them grade against it on one command arm, and each used to open and parse the
// same file for itself. Nothing inside a hook call writes the store, so one snapshot is
// what those four reads already agreed on.
func withJobStoreRows(ctx context.Context, at location) context.Context {
	if at.cacheDir == "" {
		return ctx
	}
	rows, err := job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}).List()
	return context.WithValue(ctx, jobStoreRowsKey{}, jobStoreRows{rows: rows, err: err})
}

// leaseRows reports the pinned job store, reading it when nothing pinned one. A test that
// calls a single rule gets its own read, which is what every rule used to do.
func leaseRows(ctx context.Context, at location) ([]types.Job, error) {
	if pinned, ok := ctx.Value(jobStoreRowsKey{}).(jobStoreRows); ok {
		return pinned.rows, pinned.err
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
func appendHookActivity(ctx context.Context, location location, input string, who hookAttribution, tool, lease, preauth string, verdict Verdict) {
	if input == "" || location.cacheDir == "" {
		return
	}
	command := trail.AgentCommand{
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
	}
	if tool == hookToolCommand {
		command.Command = input
	} else {
		command.Path = input
	}
	trail.AppendAgentCommand(ctx, location.cacheDir, command)
}

// appendHookSpawn records a spawn into the same trail, so a person auditing the
// activity log later can see WHAT CONTEXT an orchestrator handed a sub-agent, not merely that it
// spawned one. Like appendHookActivity it is best-effort and cannot fail the tool call; unlike it
// there is no verdict to record, because a spawn is not a guard surface.
func appendHookSpawn(ctx context.Context, deps Dependencies, req hookRequest, who hookAttribution) {
	if req.Value == "" {
		return
	}
	location := hookLocation(ctx, deps)
	if location.cacheDir == "" {
		return
	}
	trail.AppendAgentSpawn(ctx, location.cacheDir, trail.AgentSpawn{
		Actor:     "agent",
		Workspace: location.workspace,
		Host:      who.Host,
		Session:   who.Session,
		Event:     who.Event,
		Tool:      req.Tool,
		Child:     req.Child,
		Context:   req.Value,
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
