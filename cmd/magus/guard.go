package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
)

// hookUsage describes the guard: it reads one command or path and answers with a
// verdict.
func hookUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus session hook [--path] [flags]   # the command or path arrives on stdin")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Evaluate ONE shell command, or one file path an edit is about to write,")
	fmt.Fprintln(w, "against this workspace's guard rules, and report a deny/advise/pass")
	fmt.Fprintln(w, "verdict. Built for an agent host's pre-tool-use hook: the input is read")
	fmt.Fprintln(w, "from stdin, so nothing has to be quoted through a shell twice.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Two input shapes are accepted. Plain text is the command (or path) itself.")
	fmt.Fprintln(w, "A JSON envelope from a host that writes one needs no --path and no jq: the")
	fmt.Fprintln(w, "envelope says what is about to run and whether it is a write. An explicit")
	fmt.Fprintln(w, "flag still wins, because a wrapper that passed it meant it.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	// Fprintf with %% : vet rejects a Printf directive inside an Fprintln, and the
	// example is worth more than the convenience. notifyUsage does the same.
	fmt.Fprintf(w, "  printf '%%s' 'go build ./...' | magus session hook\n")
	fmt.Fprintf(w, "  printf '%%s' 'MAGUS.md' | magus session hook --path\n")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --path                judge the input as a file path an edit is about to")
	fmt.Fprintln(w, "                        write, not as a shell command")
	fmt.Fprintln(w, "  --observe             record the path as one the agent REACHED and judge")
	fmt.Fprintln(w, "                        nothing; wire it to the tools that only look")
	fmt.Fprintln(w, "  --agent-name <name>   agent host this invocation came from (attribution")
	fmt.Fprintln(w, "                        only; the verdict never reads it)")
	fmt.Fprintln(w, "  --session <id>        the host's own session id, recorded on the event")
	fmt.Fprintln(w, "  --transcript <path>   the host's own log of this session, recorded as a")
	fmt.Fprintln(w, "                        pointer; magus never opens it")
	fmt.Fprintln(w, "  --event <name>        the host's hook event name (e.g. PreToolUse)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global display flags (-o, -s, -q, -v, --tee) are accepted; see `magus -h`.")
}

// guardVerdict is the neutral result of evaluating one shell command: exactly
// one decision, carrying the field that decision needs. This envelope is the
// stable contract an agent host's hook config shapes with -o template (or
// parses from -o json); the host-specific response dialects live in the
// documentation, never in code.
type guardVerdict struct {
	SchemaVersion int    `json:"schema_version"`
	Decision      string `json:"decision"`          // one of agent.GuardDecisions
	Reason        string `json:"reason,omitempty"`  // deny: the block reason, written for the model
	Context       string `json:"context,omitempty"` // advise: context to inject alongside the allowed call
}

// hookCmd implements `magus session hook`: evaluate one shell command or file path read
// from stdin and emit a verdict. The caller owns extraction from its
// host-specific event shape; magus owns only the host-neutral policy.
//
// A guard must FAIL OPEN: an empty or unreadable input is a pass, never an error
// that would block every tool call.
func hookCmd(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("hook", flag.ContinueOnError)
	// --observe is observation, not policy. A wrapper sets it for a tool that only
	// LOOKS - no rule judges a read, so running the write rules over one would only
	// ever manufacture a false advisory about editing a file the agent opened
	// read-only. Which of a host's tools merely look is the wrapper's knowledge,
	// never magus's: see the tool-label constants and
	// TestNoHostSpecificBehaviorInCode.
	//
	// The attribution flags name WHO produced the observation, and the guard's
	// verdict never reads them. Every one is optional and unvalidated - including
	// the host name, which is an opaque label the caller chooses rather than a set
	// magus knows, because a magus that enumerated hosts would need a release per
	// host. A wrapper that cannot extract a session id must still get a verdict;
	// erroring here would block a tool call over metadata.
	hf := gen.BindSessionHook(fset)
	// The environment supplies the DEFAULT, so an explicit --delegation still wins: a shell
	// that exported the variable for a whole session must not outrank a per-call
	// override. Same shape `magus run` uses for MAGUS_SHARD.
	// trail.DelegationFromEnv, never a raw Getenv: the journal producers read the variable
	// through the same helper, and two readers with different trimming rules split one
	// exported delegation into a journal identity and an unguarded write.
	envDefault(fset, flagHookDelegation, trail.DelegationFromEnv())
	// The whole display set, not a hand-rolled -o: this command used to define
	// its own output flag and so silently lacked -s, -q, -v and --tee. That gap
	// is the reason for the rule - a flag accepted on most commands teaches
	// callers it is unreliable everywhere.
	bindDisplayFlags(fset)
	fset.Usage = func() { hookUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus session hook: takes no positional arguments (read the command or path from stdin)")
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}
	// Read through Lookup rather than off a bound variable so the value is the same
	// whichever registration above owns the flag. Goes with the TODO there.
	actingDelegation := hf.Delegation

	input, hasInput := readGuardInput(in)
	who := hookAttribution{Host: hf.AgentName, Session: hf.Session, Transcript: hf.Transcript, Event: hf.Event}
	// A host that writes its hook payload as JSON needs no jq and no --path: the envelope
	// says what is about to run and whether it is a write. Explicit flags still win, since
	// a wrapper that passed them meant them.
	if req, isEnvelope := decodeHookEnvelope(input.Value); isEnvelope {
		input.Value = req.Value
		if req.IsPath {
			hf.Path = true
		}
		if who.Session == "" {
			who.Session = req.Who.Session
		}
		if who.Transcript == "" {
			who.Transcript = req.Who.Transcript
		}
		if who.Event == "" {
			who.Event = req.Who.Event
		}
		if req.IsSpawn {
			// A delegation carries no verdict, so it returns the pass every other
			// non-finding does and never reaches the guard. Handled here rather than
			// beside the two guard arms because the whole point is that nothing judges
			// it: the handed context is prose, and a prompt that merely MENTIONS a
			// denied command would otherwise block the delegation that describes it.
			appendHookSpawn(ctx, req, who)
			return writeGuardVerdict(out, opts,
				guardVerdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"})
		}
	}
	tool := hookToolCommand
	switch {
	case hf.Observe:
		tool = hookToolRead
	case hf.Path:
		tool = hookToolWrite
	}
	verdict := guardVerdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
	switch {
	case !hasInput:
		// Nothing arrived on stdin. The verdict stays pass and the append below no-ops.
	case hf.Observe:
		// No rule judges a read or a search, so none is run: the observation IS the whole
		// contribution. Running the write rules here would only ever manufacture a false
		// advisory about editing a file the agent opened read-only.
	case hf.Path:
		// The delegation ledger speaks first. It is the only path rule whose verdict is
		// about a CONCURRENT AGENT rather than about the file itself, so nothing else can
		// outrank it: the regeneration advice below is still true after a collision, and
		// saying that instead would let two delegations edit one path in silence.
		context := ""
		switch g := gradeDelegatedWrite(ctx, actingDelegation, input.Value); g.Decision {
		case "deny":
			verdict.Decision = "deny"
			verdict.Reason = g.Reason
		case "advise":
			context = g.Context
		}
		// The generated-output rule is definitive (it reads declared globs), so it
		// outranks the heuristics below; the memory nudge is a heuristic on the
		// filename and only fills the silence it leaves.
		if verdict.Decision == "pass" && context == "" {
			context = adviseGeneratedWrite(ctx, input.Value)
		}
		// The notes rule DENIES, so it is checked before the advisories: a verdict
		// that blocks is not something to fall through to. It sits after the
		// generated-output rule only because a path cannot honestly be both, and if
		// it somehow were, the regeneration answer is the more actionable one.
		if verdict.Decision == "pass" && context == "" {
			if reason := denyNotesWrite(input.Value); reason != "" {
				verdict.Decision = "deny"
				verdict.Reason = reason
			}
		}
		if verdict.Decision == "pass" && context == "" {
			context = adviseInstalledSkillWrite(input.Value)
		}
		if verdict.Decision == "pass" && context == "" {
			context = adviseMemoryWrite(input.Value)
		}
		if verdict.Decision == "pass" && context == "" {
			context = adviseNewSourceDir(input.Value)
		}
		if verdict.Decision == "pass" && context != "" {
			verdict.Decision = "advise"
			verdict.Context = context
		}
	default:
		switch v := evaluateBashGuard(input.Value); {
		case v.Deny != "":
			verdict.Decision = "deny"
			verdict.Reason = v.Deny
		case v.Context != "":
			verdict.Decision = "advise"
			verdict.Context = v.Context
		}
		// Gated on the command being the GATE, not on it merely spawning work: the
		// advisory's own answer is to run a narrower target, and firing on that
		// narrower target argues with the caller for doing what it asked. The narrow
		// case is also the common one, so a rule that speaks there is a rule the
		// reader learns to skip.
		if verdict.Decision == "pass" && commandRunsGate(input.Value) {
			notice := adviseRepeatGate(workspaceRunsDir(globalCfg.Cache.Dir), time.Now())
			if notice != "" {
				verdict.Decision = "advise"
				verdict.Context = notice
			}
		}
	}
	// Said last and on EVERY surface: a stale binary's verdicts are all suspect, not
	// just the ones that matched a rule.
	//
	// A deny most of all. That is the verdict the caller cannot see past, so a block
	// from rules they have already changed is the case this rule exists for - and the
	// first version of it skipped exactly that arm. The reason comes first, because
	// the block has to be explained before it can be doubted.
	if notice := staleGuardNotice(); notice != "" && !hf.Observe {
		switch verdict.Decision {
		case "deny":
			verdict.Reason += "\n\n" + notice
		case "advise":
			verdict.Context += "\n\n" + notice
		default:
			verdict.Decision, verdict.Context = "advise", notice
		}
	}
	// An observation is not a judgment, and the trail already knows the difference: an
	// AgentCommand with no Decision previews as "observed" rather than "guard: <decision>".
	// Recording the pass verdict here would have every read claim the guard ran and cleared
	// it, which is exactly the conflation --observe exists to remove. The WIRE verdict is
	// unchanged - a host still needs a decision it can parse, and "pass" is the true one.
	record := verdict
	if hf.Observe {
		record.Decision, record.Reason, record.Context = "", "", ""
	}
	appendHookActivity(ctx, input, who, tool, record)
	if err := writeGuardVerdict(out, opts, verdict); err != nil {
		return err
	}
	return enforceVerdict(opts, verdict)
}

// guardDenyExitCode is what a denied command exits with.
//
// A hook that reports a deny and exits 0 blocks NOTHING - the host sees success
// and runs the command anyway, so the guard looks enforced and is not. 2 rather
// than 1 is what the dominant host reads as "block and show the reason to the
// model". The collision with the usage code is harmless: a guard that could not
// parse its input has not judged the command either.
const guardDenyExitCode = 2

// enforceVerdict turns a deny into a blocking exit. Applies to every format: an
// `-o json` caller with a zero status would be told the same lie in a different
// shape.
//
// The reason reaches stderr only when stdout does not already carry it as prose,
// i.e. every format but text. The guard templates read the verdict off stdout and
// one discards stderr outright, so an unconditional copy printed a kilobyte-plus
// reason twice to an audience with a context budget.
func enforceVerdict(opts OutputOptions, verdict guardVerdict) error {
	if verdict.Decision != "deny" {
		return nil
	}
	if opts.Format != FormatText {
		fmt.Fprintln(os.Stderr, verdict.Reason)
	}
	return errSilent{exitCode: guardDenyExitCode}
}

// writeGuardVerdict renders a verdict through the standard output arm.
func writeGuardVerdict(out io.Writer, opts OutputOptions, verdict guardVerdict) error {
	switch opts.Format {
	case FormatText:
		switch verdict.Decision {
		case "deny":
			fmt.Fprintln(out, "deny: "+verdict.Reason)
		case "advise":
			fmt.Fprintln(out, "advise: "+verdict.Context)
		default:
			fmt.Fprintln(out, "pass")
		}
		return nil
	case FormatName:
		fmt.Fprintln(out, verdict.Decision)
		return nil
	}
	return writeFormatted(out, opts, verdict)
}

// guardInput keeps the resolved command/path distinct from its rendering and
// audit policy. The hook's input is deliberately plain text: host event parsing
// belongs in the host wrapper, not in a durable magus CLI contract.
type guardInput struct {
	Value string
}

func readGuardInput(in io.Reader) (guardInput, bool) {
	b, err := io.ReadAll(in)
	value := strings.TrimSpace(string(b))
	if err != nil || value == "" {
		return guardInput{}, false
	}
	return guardInput{Value: value}, true
}

// hookEnvelope is the JSON an agent host writes to a hook's stdin: which tool is about to
// run and with what. Only the fields the guard needs are modeled; everything else in the
// payload is ignored rather than rejected, since a host is free to add to it.
type hookEnvelope struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	// TranscriptPath is the host's own log of this session. Recorded as a pointer so a
	// session id in the activity view leads somewhere; magus never reads the file.
	TranscriptPath string `json:"transcript_path"`
	ToolName       string `json:"tool_name"`
	ToolInput      struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		// A delegation: the context an orchestrator is about to hand a sub-agent, plus
		// whatever the host calls the callee. Field PATHS, not a host name - the same line
		// the two fields above already draw. magus does not know which tool produces them
		// and never switches on ToolName; a payload carrying a prompt IS a spawn.
		Prompt       string `json:"prompt"`
		Description  string `json:"description"`
		SubagentType string `json:"subagent_type"`
	} `json:"tool_input"`
}

// The tool labels recorded on an activity event. They are magus's OWN vocabulary, chosen by
// which flags the wrapper passed - never a host's tool name.
//
// That division is the whole design: only the wrapper knows that its host calls a read
// "Read" or "read_file", and mapping those names here would be a per-host branch, so the
// next change to any host would mean a magus release. The matcher in a host's own config is
// where the host's vocabulary lives.
//
// Nothing MECHANICALLY enforces this particular case, which is why it is written down here.
// TestNoHostSpecificBehaviorInCode matches host NAMES, so a switch over "Read"/"Bash" - a
// per-host branch in everything but spelling - passes it untouched.
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
// The envelope cannot tell a read from a write on its own - both arrive carrying a
// file_path - so it does not try. --observe is what separates them, and only the wrapper
// can set it, because only the wrapper knows which of its host's tools merely look.
//
// A payload carrying a PROMPT rather than either is a delegation handoff: it is RECORDED and
// EXEMPT from judgment. No rule is evaluated against a prompt, so the guard never denies one -
// there is no command and no path to judge, only a context transfer to note. It is tested last on
// purpose, so that adding this branch cannot change the verdict on any payload the guard already
// judged.
//
// Anything that is not an object with a usable tool_input is left alone and judged as the
// literal text it is - the bare-command form keeps working exactly as before.
func decodeHookEnvelope(raw string) (hookRequest, bool) {
	if !strings.HasPrefix(raw, "{") {
		return hookRequest{}, false
	}
	var env hookEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return hookRequest{}, false
	}
	req := hookRequest{Who: hookAttribution{
		Session:    env.SessionID,
		Transcript: env.TranscriptPath,
		Event:      env.HookEventName,
	}}
	switch {
	case env.ToolInput.Command != "":
		req.Value = env.ToolInput.Command
	case env.ToolInput.FilePath != "":
		req.Value, req.IsPath = env.ToolInput.FilePath, true
	case env.ToolInput.Prompt != "":
		req.Value, req.IsSpawn = env.ToolInput.Prompt, true
		req.Tool = env.ToolName
		// Most specific label first. A sub-agent TYPE names what was delegated to and repeats
		// across spawns, so it groups a delegation feed; a description is per-spawn prose; the
		// tool name is the last resort that at least says a spawn happened.
		for _, label := range []string{env.ToolInput.SubagentType, env.ToolInput.Description, env.ToolName} {
			if label != "" {
				req.Child = label
				break
			}
		}
	default:
		return hookRequest{}, false
	}
	return req, true
}

// hookRequest is what a host's payload asked the guard to judge: the text, whether it is a
// path rather than a command, and who reported it. A spawn asks for nothing to be judged - it
// carries the handed context and the callee's label, and is recorded rather than evaluated.
type hookRequest struct {
	Value   string
	IsPath  bool
	IsSpawn bool
	Tool    string
	Child   string
	Who     hookAttribution
}

// hookAttribution is what the host wrapper knows about itself and cannot be
// derived here: a hook runs as a short-lived client process with no way to
// discover which agent host started it. It travels beside the input rather than
// inside guardInput because the guard's verdict must never depend on it.
type hookAttribution struct {
	Host       string
	Session    string
	Transcript string
	Event      string
}

type hookActivityLocation struct {
	base      string
	workspace string
}

type hookActivityLocationKey struct{}

// appendHookActivity contributes a best-effort, normalized observation to the same durable
// trail used by MCP and daemon actions. It deliberately runs before rendering the guard response:
// the host may choose not to execute a denied command, and a pre-hook never learns the eventual
// exit status. An audit failure must therefore be invisible to both the verdict and the command.
func appendHookActivity(ctx context.Context, input guardInput, who hookAttribution, tool string, verdict guardVerdict) {
	if input.Value == "" {
		return
	}
	location := hookActivityTrail(ctx)
	if location.base == "" {
		return
	}
	command := trail.AgentCommand{
		Actor:      "agent",
		Workspace:  location.workspace,
		Host:       who.Host,
		Session:    who.Session,
		Transcript: who.Transcript,
		Event:      who.Event,
		Tool:       tool,
		Decision:   verdict.Decision,
		Reason:     verdict.Reason,
		Context:    verdict.Context,
	}
	if tool == hookToolCommand {
		command.Command = input.Value
	} else {
		command.Path = input.Value
	}
	trail.AppendAgentCommand(ctx, location.base, command)
}

// appendHookSpawn records a delegation handoff into the same trail, so a person auditing the
// activity log later can see WHAT CONTEXT an orchestrator handed a sub-agent, not merely that it
// spawned one. Like appendHookActivity it is best-effort and cannot fail the tool call; unlike it
// there is no verdict to record, because a spawn is not a guard surface.
func appendHookSpawn(ctx context.Context, req hookRequest, who hookAttribution) {
	if req.Value == "" {
		return
	}
	location := hookActivityTrail(ctx)
	if location.base == "" {
		return
	}
	trail.AppendAgentSpawn(ctx, location.base, trail.AgentSpawn{
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

// hookActivityTrail resolves the local workspace cache because a hook runs as a short-lived
// client process, outside the daemon's memory. Tests can pin a temporary base through context so
// a guard unit test never writes its checkout's real activity trail.
func hookActivityTrail(ctx context.Context) hookActivityLocation {
	if location, ok := ctx.Value(hookActivityLocationKey{}).(hookActivityLocation); ok {
		return location
	}
	root, err := magus.FindRoot("")
	if err != nil {
		return hookActivityLocation{}
	}
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return hookActivityLocation{}
	}
	return hookActivityLocation{base: cacheDir, workspace: root}
}
