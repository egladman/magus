package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/ledger"
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
	fmt.Fprintln(w, "  --lease <id>          the ledger row this call acts as; the lease-scoped")
	fmt.Fprintln(w, "                        rules are graded against it, and the verdict names")
	fmt.Fprintln(w, "                        it. Defaults to magus.lease in $BAGGAGE; an id this")
	fmt.Fprintln(w, "                        workspace's ledger does not declare is an error")
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
	// Lease is the row this verdict was graded under, empty when the call named none.
	//
	// It answers a question every other field left open: which declaration decided this.
	// A session bound to a typo'd id, a session bound to a row that has already finished,
	// and a session nobody leased all produced identical verdicts before this field
	// existed, and two of the three were running unguarded (friction synthesis
	// 2026-09-11, C3). An added optional field is not a schema bump: a glue that does not
	// read it is unaffected, which is the rule agent.GuardSchemaVersion states.
	Lease string `json:"lease,omitempty"`
}

// hookCmd implements `magus session hook`: evaluate one shell command or file path read
// from stdin and emit a verdict. The caller owns extraction from its
// host-specific event shape; magus owns only the host-neutral policy.
//
// An EMPTY stdin fails OPEN: a wrapper that hands the hook nothing must not have
// every tool call blocked. A stdin that fails to READ is the opposite case and
// fails CLOSED, because bytes were on their way and were lost, so the guard has
// judged nothing and the command it never saw must not be reported as cleared.
func hookCmd(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("hook", flag.ContinueOnError)
	// --observe is observation, not policy. A wrapper sets it for a tool that only
	// LOOKS: no rule judges a read, so running the write rules over one would only
	// ever manufacture a false advisory about editing a file the agent opened
	// read-only. Which of a host's tools merely look is the wrapper's knowledge,
	// never magus's: see the tool-label constants and
	// TestNoHostSpecificBehaviorInCode.
	//
	// The attribution flags name WHO produced the observation, and the guard's
	// verdict never reads them. Every one is optional and unvalidated, including
	// the host name, which is an opaque label the caller chooses rather than a set
	// magus knows, because a magus that enumerated hosts would need a release per
	// host. A wrapper that cannot extract a session id must still get a verdict;
	// erroring here would block a tool call over metadata.
	hf := gen.BindSessionHook(fset)
	// The environment supplies the DEFAULT, so an explicit --lease still wins: a shell
	// that exported the variable for a whole session must not outrank a per-call
	// override. Same shape `magus run` uses for MAGUS_SHARD.
	// trail.LeaseFromEnv, never a raw Getenv: the journal producers read the variable
	// through the same helper, and two readers with different trimming rules split one
	// exported lease into a journal identity and an unguarded write.
	// Discarded, unlike `magus run`'s shard pair: --lease is a plain string flag whose
	// Set cannot fail, and a hook that refused to answer over a malformed lease would
	// block the tool call this comment's first line says must always get a verdict.
	_ = envDefault(fset, flagHookLease, trail.LeaseFromEnv())
	// The whole display set, not a hand-rolled -o: this command used to define
	// its own output flag and so silently lacked -s, -q, -v and --tee. That gap
	// is the reason for the rule: a flag accepted on most commands teaches
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
	input, readErr := readGuardInput(in)
	// A failed read is not an empty stdin, and collapsing the two cleared every
	// command whose payload arrived truncated. Answered as a deny so the exit is 2,
	// which is what the manpage promises: deny and unreadable input share the code,
	// so a host that blocks on 2 fails closed in both cases. --observe is exempt
	// because it carries no verdict: there is nothing to fail closed about, and the
	// documented contract is that it always exits 0.
	if readErr != nil && !hf.Observe {
		verdict := guardVerdict{
			SchemaVersion: agent.GuardSchemaVersion,
			Decision:      "deny",
			Reason: "magus session hook could not read its input from stdin: " + readErr.Error() + "\n\n" +
				"Nothing was judged, so this call is blocked rather than cleared. Retry it; if it keeps " +
				"failing, run the hook by hand with the same input to see the error, and check the hook " +
				"wiring in your agent host.",
		}
		if err := writeGuardVerdict(out, opts, verdict); err != nil {
			return err
		}
		return enforceVerdict(opts, verdict)
	}
	hasInput := input != ""
	who := hookAttribution{Host: hf.AgentName, Session: hf.Session, Transcript: hf.Transcript, Event: hf.Event}
	// A host that writes its hook payload as JSON needs no jq and no --path: the envelope
	// says what is about to run and whether it is a write. Explicit flags still win, since
	// a wrapper that passed them meant them.
	if req, isEnvelope := decodeHookEnvelope(input); isEnvelope {
		if req.NothingToJudge {
			// A host envelope whose tool_input carries no command, path or prompt (a todo
			// list, a search) has nothing any rule can read. Falling through judged the raw
			// JSON as a shell line, so a denied command merely NAMED inside a todo blocked
			// the tool call that wrote the todo.
			return writeGuardVerdict(out, opts,
				guardVerdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"})
		}
		input = req.Value
		hasInput = input != ""
		if req.IsPath {
			hf.Path = true
		}
		ctx = hookContextAt(ctx, req.Cwd)
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
			// A spawn carries no verdict, so it returns the pass every other
			// non-finding does and never reaches the guard. Handled here rather than
			// beside the two guard arms because the whole point is that nothing judges
			// it: the handed context is prose, and a prompt that merely MENTIONS a
			// denied command would otherwise block the spawn that describes it.
			appendHookSpawn(ctx, req, who)
			return writeGuardVerdict(out, opts,
				guardVerdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"})
		}
	}
	// One gate for the whole invocation, holding each enrolled advisory to one firing per
	// session. Built AFTER the envelope is decoded: a host that reports its session id only
	// inside the payload would otherwise be graded as having reported none, and every
	// session on that host would share the anonymous bucket. The acting lease is resolved
	// here for the same reason: the envelope's cwd is what locates the worker's marker.
	// An explicit --lease wins; otherwise the same resolution the sandbox applies, so the
	// two tiers cannot disagree about who is acting (see ledger.LeaseMarkerName).
	location := hookActivityTrail(ctx)
	actingLease := hf.Lease
	if actingLease == "" {
		actingLease = ledger.ActingLease(location.base)
	}
	gate := newAdvisoryGate(location.base, who.Session)
	tool := hookToolCommand
	switch {
	case hf.Observe:
		tool = hookToolRead
	case hf.Path:
		tool = hookToolWrite
	}
	verdict := guardVerdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass", Lease: actingLease}
	// Where the acting lease STANDS, read once and before any rule. An id the ledger does
	// not carry is refused here rather than passed down, because every lease-scoped rule
	// below reads that row and finding nothing is how they all fall silent at once: the
	// call would be graded by nobody while looking exactly like a guarded one.
	//
	// --observe is exempt, as it is from every other verdict: it carries none.
	standing := actingLeaseStanding(ctx, actingLease)
	if !hf.Observe {
		if reason := denyUndeclaredLease(standing, actingLease); reason != "" {
			verdict.Decision, verdict.Reason = "deny", reason
			appendHookActivity(ctx, location, input, who, tool, actingLease, "", verdict)
			if err := writeGuardVerdict(out, opts, verdict); err != nil {
				return err
			}
			return enforceVerdict(opts, verdict)
		}
	}
	// A served next is magus's own suggestion, and the guard does not argue with it: no
	// advisory fires on it, and the role-scoped rules stand down. The workspace-wide
	// denies do not, and they are the ones whose reasons say why (see guard_preauth.go).
	preauth := ""
	if hasInput && !hf.Observe && !hf.Path {
		preauth = servedNextPreauthorizes(gate, input)
	}
	switch {
	case !hasInput:
		// Nothing arrived on stdin. The verdict stays pass and the append below no-ops.
	case hf.Observe:
		// No rule judges a read or a search, so none is run: the observation IS the whole
		// contribution. Running the write rules here would only ever manufacture a false
		// advisory about editing a file the agent opened read-only.
	case hf.Path:
		// The lease ledger speaks first. It is the only path rule whose verdict is
		// about a CONCURRENT AGENT rather than about the file itself, so nothing else can
		// outrank it: the regeneration advice below is still true after a collision, and
		// saying that instead would let two leases edit one path in silence.
		advice := ""
		// Graded ahead of the rules, though it speaks near the end of them: the project
		// this write lands in is recorded whatever verdict they reach, so it cannot be
		// resolved inside a rung that a louder rule skips.
		drift := gradeScopeDrift(ctx, gate, actingLease, input)
		// spoken reports that a rule MATCHED, which is not the same as a rule that
		// produced text. A once-per-session advisory that already fired this session
		// matched and stayed quiet, and the rules below it must not step into the silence
		// it left: without this the second write to a skill source would draw the
		// new-directory advisory instead of nothing.
		spoken := false
		// The checkout's own cache dir speaks before the ledger, and it is the only rule
		// that does. Every lease-scoped verdict below is computed from files in there, so
		// a worker whose lane happens to cover the dir must not be told it owns the lane:
		// what it is editing is whether the lane was checked.
		if reason := denyCacheDirPath(location, input); reason != "" {
			verdict.Decision, verdict.Reason = "deny", reason
		}
		if verdict.Decision != "deny" {
			switch g := gradeLeasedWrite(ctx, actingLease, input); g.Decision {
			case "deny":
				verdict.Decision = "deny"
				verdict.Reason = g.Reason
			case "advise":
				advice, spoken = gate.once(g.Kind, g.Context), true
			}
		}
		// The guard's own installation, ranked directly under the collision report and
		// above everything else. It is definitive the way the ledger rule is (a known
		// list of files, not a heuristic on the name), and what it refuses is the switch
		// every rule below it depends on. It cannot outrank the collision, because that
		// one is about a concurrent agent and stays true whatever this file is.
		if verdict.Decision != "deny" {
			switch g := gradeHookWiringWrite(actingLease, input); g.Decision {
			case "deny":
				verdict.Decision, verdict.Reason = "deny", g.Reason
			case "advise":
				if !spoken {
					advice, spoken = gate.once(g.Kind, g.Context), true
				}
			}
		}
		// The generated-output rule is definitive (it reads declared globs), so it
		// outranks the heuristics below; the memory nudge is a heuristic on the
		// filename and only fills the silence it leaves.
		if verdict.Decision == "pass" && !spoken {
			if text := adviseGeneratedWrite(ctx, input); text != "" {
				advice, spoken = text, true
			}
		}
		// The notes rule DENIES, so it is checked before the advisories: a verdict
		// that blocks is not something to fall through to. It sits after the
		// generated-output rule only because a path cannot honestly be both, and if
		// it somehow were, the regeneration answer is the more actionable one.
		if verdict.Decision == "pass" && !spoken {
			if reason := denyNotesWrite(input); reason != "" {
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
		// Both of these name paths and a target belonging to magus's own checkout, and
		// both are inert anywhere else; see magusOwnSourceTree. They sit above the
		// new-directory rule because a new skill directory is both, and which method to
		// load is the more useful of the two answers.
		if verdict.Decision == "pass" && !spoken {
			if text := adviseAgentSurfaceWrite(input); text != "" {
				advice, spoken = gate.once(advisorySkillSource, text), true
			}
		}
		if verdict.Decision == "pass" && !spoken {
			if text := adviseDescriptorWrite(input); text != "" {
				advice, spoken = gate.once(advisoryRegenSource, text), true
			}
		}
		// Above the new-directory rule because it is the wider question: whether this
		// write belongs in this session at all outranks how the unit it belongs to is
		// laid out. Every rule above it says the write itself is wrong, which is more
		// actionable than either.
		if verdict.Decision == "pass" && !spoken && drift.advice != "" {
			advice, spoken = drift.advice, true
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
		// The sibling-checkout rule ranks with the throwaway-copy deny it generalizes,
		// but reads the filesystem, so it cannot live inside evaluateBashGuard's pure
		// rule set. Ranking the two is pure, and is where the ordering is tested.
		// The cache-dir rule is outermost for the same reason and one more: it reads the
		// resolved cache location, and what it refuses outranks every other deny on the
		// line (guard_cachedir.go).
		//
		// The hint graph is built here unconditionally: the manifest read behind it is
		// one small file, and laziness would buy nothing on a hook this short-lived.
		switch v := rankCacheDirWrite(
			rankSiblingCheckout(evaluateBashGuardWith(input, hookSearchHints(location.base)), denySiblingCheckout(input)),
			denyCacheDirCommand(location, input)); {
		case v.Deny != "":
			// These are the denies that hold for everyone, so a pre-authorization does not
			// reach them: whole-tree VCS, a pipe or redirect of magus's own output, a raw
			// language tool, a relocated checkout. A next magus served would not carry one
			// anyway, and the structural test is what says so before it ships.
			verdict.Decision = "deny"
			verdict.Reason = v.Deny
		case v.Context != "" && preauth == "":
			if held := gate.onceOrBrief(v.Kind, v.Context, v.Brief); held != "" {
				verdict.Decision = "advise"
				verdict.Context = held
			}
		}
		// The lease ledger's half of the command surface. Ranked BELOW the rules above,
		// unlike the write arm where it speaks first: those refuse a command whoever runs
		// it, and a sibling checkout's gate is the wrong tree before it is the wrong scope.
		//
		// Every one of them is ROLE-scoped, which is what a pre-authorization stands
		// down: the command came from magus, computed for this role, so refusing it here
		// would be the tool disagreeing with itself in front of a reader who cannot tell
		// which half to believe.
		for _, rule := range []func(context.Context, string, string) string{denyLeaseScopedGate, denyLeaseScopedVCS, denyLeaseScopedRebind} {
			if verdict.Decision == "deny" || preauth != "" {
				break
			}
			if reason := rule(ctx, actingLease, input); reason != "" {
				verdict.Decision, verdict.Reason, verdict.Context = "deny", reason, ""
			}
		}
		// The focus rule. Its DENY outranks any advisory above it, because that one is
		// about a boundary an orchestrator declared; its advisory only fills a silence.
		// Nothing runs once a deny stands: a wrong tree is a bigger mistake than a wrong
		// project, and the rule that caught it is also the cheaper one to have run.
		if verdict.Decision != "deny" && preauth == "" {
			focus := gradeFocusRead(ctx, actingLease, input)
			switch {
			case focus.Decision == "deny":
				verdict.Decision, verdict.Reason, verdict.Context = "deny", focus.Reason, ""
			case verdict.Decision == "pass" && focus.Decision == "advise" && !gate.fireOnce(advisoryFocusPath(focus.Rel)):
				if held := gate.onceOrBrief(advisoryFocus, focus.Context, focus.Brief); held != "" {
					verdict.Decision, verdict.Context = "advise", held
				}
			}
		}
		// Gated on the command being the GATE, not on it merely spawning work: the
		// advisory's own answer is to run a narrower target, and firing on that
		// narrower target argues with the caller for doing what it asked. The narrow
		// case is also the common one, so a rule that speaks there is a rule the
		// reader learns to skip.
		if verdict.Decision == "pass" && preauth == "" && commandRunsGate(input) {
			full, brief := adviseRepeatGate(workspaceRunsDir(hookActivityTrail(ctx).base), time.Now())
			if notice := gate.onceOrBrief(advisoryGateRepeat, full, brief); notice != "" {
				verdict.Decision = "advise"
				verdict.Context = notice
			}
		}
		// The guard's half of the index-staleness fact; the load-bearing half rides the
		// command's own output (staleindex.go). gate.seen is asked BEFORE the rule, not
		// after: producing this text costs a directory walk, and once the session has been
		// told, paying for it again only to discard the answer is the cost nobody sees.
		if verdict.Decision == "pass" && preauth == "" && !gate.seen(advisoryGraphStale) && commandReadsGraph(input) {
			if notice := gate.once(advisoryGraphStale, staleGraphAdvice(ctx)); notice != "" {
				verdict.Decision = "advise"
				verdict.Context = notice
			}
		}
	}
	// A row that has already finished still names a session, and every rule keyed on it
	// has quietly stopped applying: liveLeases drops a terminal row, so the verdicts that
	// follow are the un-enrolled ones. Said once per session, and never on a deny, which
	// already explains itself and was reached by a rule that did not need the row.
	if notice := noticeTerminalLease(standing, actingLease); notice != "" && !hf.Observe && verdict.Decision != "deny" {
		if held := gate.once(advisoryLeaseTerminal, notice); held != "" {
			if verdict.Decision == "advise" {
				verdict.Context += "\n\n" + held
			} else {
				verdict.Decision, verdict.Context = "advise", held
			}
		}
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
	if notice := staleGuardNotice(); notice != "" && !hf.Observe {
		if verdict.Decision == "deny" {
			verdict.Reason += "\n\n" + notice
		} else if held := gate.once(advisoryStaleBinary, notice); held != "" {
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
	if hf.Observe {
		record.Decision, record.Reason, record.Context = "", "", ""
	}
	appendHookActivity(ctx, location, input, who, tool, actingLease, preauth, record)
	if err := writeGuardVerdict(out, opts, verdict); err != nil {
		return err
	}
	return enforceVerdict(opts, verdict)
}

// guardDenyExitCode is what a denied command exits with.
//
// A hook that reports a deny and exits 0 blocks NOTHING: the host sees success
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

// guardInputLimit bounds the payload one hook call may carry.
//
// An MCP params object is caller-controlled and can be megabytes, the raw-event wiring
// forwards the WHOLE event rather than one extracted command string, and the template
// holds a second copy of it under the host's hook timeout. Overflow is a read FAILURE
// rather than a truncation, for the reason readGuardInput gives: a truncated payload is
// exactly the "not the command the guard was shown" case.
const guardInputLimit = 1 << 20

// readGuardInput reports the payload, and the read failure separately from an empty one.
// "No input" is a host that sent nothing and "the read broke" is a payload magus was
// supposed to receive, and only the second one means the command about to run is not the
// command the guard was shown.
func readGuardInput(in io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(in, guardInputLimit+1))
	if err != nil {
		return "", err
	}
	if len(b) > guardInputLimit {
		return "", fmt.Errorf("the payload is larger than the %d-byte limit, so it was not read whole", guardInputLimit)
	}
	return strings.TrimSpace(string(b)), nil
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
// A payload carrying a PROMPT rather than either is a spawn handoff: it is RECORDED and
// EXEMPT from judgment. No rule is evaluated against a prompt, so the guard never denies one:
// there is no command and no path to judge, only a context transfer to note. It is tested last on
// purpose, so that adding this branch cannot change the verdict on any payload the guard already
// judged.
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

// mcpJudgedParams are the tool parameters a guard rule reads, in the order they render.
//
// Every field ledger.Merge applies, plus the two that name the call. A key this list omits
// reaches the row with no rule having seen it, which is how a bound worker rewrote the
// checkpoint its own work is graded against; TestMCPJudgedParamsCoverEveryMergedField holds
// the two sides together.
var mcpJudgedParams = append([]string{
	"op", "id", "owned_paths", "forbidden_paths", "focus", "depends_on",
	"validation", "read_only", "parent", "state", "checkpoint", "tier", "goal",
}, mcpRenamedParams...)

// mcpRenamedParams are the row's parameters under their other spelling.
//
// compat(until: no ledger door accepts the spellings above any more; observe it by calling
// ledger.Merge with each of those names and finding it rejected): both vocabularies are
// judged for one cycle, so a put cannot dodge a rule by picking the word on whichever side
// of the rename the guard has not learned yet.
var mcpRenamedParams = []string{"write_paths", "read_paths", "deny_paths", "model", "check"}

// The two spellings of the one list a bound caller may shrink. Both are named here rather
// than spelled at each use so the rebind rule and the renderer cannot learn one of them.
const (
	ownedPathsParam        = "owned_paths"
	ownedPathsRenamedParam = "write_paths"
)

// mcpElidedParams render as a presence marker instead of their value. No rule reads this
// one, and a goal is free prose: the rendered line is recorded in the activity trail, so
// copying it there would put a caller's sentences into an audit record shaped like a
// command. Presence is all the rebind rule needs, since naming it at all is a rewrite.
var mcpElidedParams = map[string]bool{"goal": true}

// mcpElidedValue stands in for an elided value. A word rather than an empty string: an
// empty value is how the merge spells an explicit clear, and the two must not render alike.
const mcpElidedValue = "..."

// mcpCLIEquivalent is the CLI command a magus MCP tool is the other door to.
type mcpCLIEquivalent struct {
	command hint.Command
	// operands are the tool parameters that render as positional arguments, in order.
	operands []string
	// flags are the fixed flags that make the rendering the same work the tool does, so
	// a REPORT about the gate does not render as a run of it.
	flags []string
}

// mcpCLIEquivalents route each magus tool to the command line that does the same thing, so
// the rules already written for the CLI judge the tool call rather than a second copy of
// them being written for MCP.
//
// magus_insight and magus_ledger are absent for opposite reasons: nothing in internal/hint
// spells `insight`, so there is no command to render; the ledger tool is judged on its
// PARAMETERS by the rebind rule, which is the one rule that reads an MCP call directly.
var mcpCLIEquivalents = map[hint.ToolName]mcpCLIEquivalent{
	hint.ToolRunTarget:       {command: hint.Run, operands: []string{"target", "projects"}},
	hint.ToolRunAffected:     {command: hint.Affected, operands: []string{"target"}},
	hint.ToolAffectedPlan:    {command: hint.Affected, operands: []string{"target"}, flags: []string{"--plan"}},
	hint.ToolAffectedExplain: {command: hint.Affected, operands: []string{"project"}, flags: []string{"--explain"}},
	hint.ToolVCSCheckpoint:   {command: hint.VCSCheckpoint},
	hint.ToolQuery:           {command: hint.Query, operands: []string{"query"}},
	hint.ToolExplain:         {command: hint.Explain, operands: []string{"node"}},
	hint.ToolRefs:            {command: hint.Refs, operands: []string{"symbol"}},
	hint.ToolPath:            {command: hint.Path, operands: []string{"from", "to"}},
	hint.ToolDescribe:        {command: hint.Describe, operands: []string{"kind", "name"}},
	hint.ToolDescribeFile:    {command: hint.DescribeFile, operands: []string{"paths"}},
	hint.ToolWhere:           {command: hint.Where, operands: []string{"filter"}},
	hint.ToolOutput:          {command: hint.QueryOutput, operands: []string{"ref"}},
	hint.ToolStats:           {command: hint.GraphStats},
	hint.ToolDiff:            {command: hint.Diff},
	hint.ToolDoctor:          {command: hint.Doctor},
	hint.ToolStatus:          {command: hint.Status},
	hint.ToolConfigGet:       {command: hint.ConfigView},
}

// renderMCPCall normalizes an MCP call to a magus tool into a command line.
//
// Three shapes, in the order they are decided. The ledger tool renders `<tool> key=value`,
// because its parameters ARE what the rebind rule judges. A tool with a CLI equivalent
// renders that argv, so the command rules read it as the work it is. Anything else renders
// its bare tool name: nothing judges it, and the activity trail still records that it
// happened.
//
// Rendering and re-parsing rather than handing the rules a map keeps ONE judged value per
// call: the string the rules read is the string the trail records, so what a person audits
// later is what was graded. A value holding a space is quoted, which the shell parser the
// rules already run unquotes.
func renderMCPCall(name string, input map[string]any) string {
	if name == hint.ToolLedger.String() {
		out := []string{name}
		for _, key := range mcpJudgedParams {
			value, ok := input[key]
			if !ok {
				continue
			}
			if mcpElidedParams[key] {
				out = append(out, key+"="+mcpElidedValue)
				continue
			}
			out = append(out, key+"="+quoteMCPValue(mcpValueString(value)))
		}
		return strings.Join(out, " ")
	}
	cli, ok := mcpCLIEquivalents[hint.ToolName(name)]
	if name == hint.ToolMemory.String() {
		// The one tool whose op picks the verb: a put writes, everything else reads.
		cli, ok = mcpCLIEquivalent{command: hint.MemoryLs}, true
		if envelopeString(input, "op") == "put" {
			cli = mcpCLIEquivalent{command: hint.MemoryPut, operands: []string{"name"}}
		}
	}
	if !ok {
		return name
	}
	out := append([]string{cli.command.String()}, cli.flags...)
	for _, key := range cli.operands {
		if value := mcpValueString(input[key]); value != "" {
			out = append(out, quoteMCPValue(value))
		}
	}
	return strings.Join(out, " ")
}

// mcpValueString flattens one parameter value. A list parameter arrives as either a
// comma-separated string or an array (both are accepted by the tool), and it renders the
// same way from both, so a rule cannot be dodged by picking a spelling.
func mcpValueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, mcpValueString(item))
		}
		return strings.Join(parts, ",")
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// quoteMCPValue keeps a value with whitespace as one word on the rendered line.
func quoteMCPValue(value string) string {
	if strings.ContainsAny(value, " \t\"'\\") {
		return strconv.Quote(value)
	}
	return value
}

// mcpMagusPrefix is how a host spells a call to magus's own MCP server. The prefix is the
// host's, the name after it is magus's.
const mcpMagusPrefix = "mcp__magus__"

// magusToolCall returns the magus MCP tool a host's tool name refers to, or "" when it
// refers to none.
//
// The bare name or magus's own prefix, and nothing else. A suffix match let any other
// server's `whatever__magus_ledger` decode as a magus ledger call, be rendered into
// magus's activity trail, and be judged by magus's rules.
func magusToolCall(toolName string) string {
	name := strings.TrimPrefix(toolName, mcpMagusPrefix)
	if !slices.ContainsFunc(hint.AllToolNames, func(t hint.ToolName) bool { return t.String() == name }) {
		return ""
	}
	return name
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
	// dir is where the tool call runs, which the focus rule needs and the trail does
	// not: a session opened in a subdirectory stands in a different project than the
	// workspace root does, and that difference is the whole of what focus judges.
	dir string
}

type hookActivityLocationKey struct{}

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
func appendHookActivity(ctx context.Context, location hookActivityLocation, input string, who hookAttribution, tool, lease, preauth string, verdict guardVerdict) {
	if input == "" || location.base == "" {
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
		Lease:      lease,
		Preauth:    preauth,
		Decision:   verdict.Decision,
		Reason:     verdict.Reason,
		Context:    verdict.Context,
	}
	if tool == hookToolCommand {
		command.Command = input
	} else {
		command.Path = input
	}
	trail.AppendAgentCommand(ctx, location.base, command)
}

// appendHookSpawn records a spawn handoff into the same trail, so a person auditing the
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

// hookActivityTrail resolves the local workspace cache because a hook runs as a short-lived
// client process, outside the daemon's memory. Tests can pin a temporary base through context so
// a guard unit test never writes its checkout's real activity trail; hookContextAt pins the
// checkout a host's envelope named the same way.
func hookActivityTrail(ctx context.Context) hookActivityLocation {
	if location, ok := ctx.Value(hookActivityLocationKey{}).(hookActivityLocation); ok {
		return location
	}
	return hookActivityLocationAt("")
}

// hookActivityLocationAt resolves the workspace holding dir, or the process cwd for "".
// The process cwd's workspace is the one globalCfg was loaded for, so its resolved config
// applies; another checkout reads its own magus.yaml, because a cache dir configured in
// the orchestrator's tree says nothing about where a worker's cache lives.
func hookActivityLocationAt(dir string) hookActivityLocation {
	root, err := magus.FindRoot(dir)
	if err != nil {
		return hookActivityLocation{}
	}
	var opts []magus.Option
	if dir == "" {
		opts = append(opts, magus.WithLoadedConfig(globalCfg))
	}
	cacheDir, err := magus.ResolveCacheDir(root, opts...)
	if err != nil {
		return hookActivityLocation{}
	}
	if dir == "" {
		// The process cwd, which for "" is what root was found from. Read rather than
		// assumed to be the root: a session opened in a subdirectory is exactly the
		// case focus exists for, and collapsing it to the root would hide it.
		if wd, wderr := os.Getwd(); wderr == nil {
			dir = wd
		}
	}
	return hookActivityLocation{base: cacheDir, workspace: root, dir: dir}
}

// hookContextAt pins the trail location to the checkout holding cwd, the directory the host
// reported its tool call runs in. A host runs its hooks from wherever it likes, and the
// process cwd is then the orchestrator's tree rather than the worker's: the marker bound
// with `magus session lease` lives in the worker's checkout, so the envelope's cwd is the
// only thing that finds it. A location already pinned (a test's) wins, and a cwd magus
// cannot resolve to a workspace changes nothing. A relative cwd is ignored rather than
// resolved against the hook process, whose directory is the thing it must not stand for.
func hookContextAt(ctx context.Context, cwd string) context.Context {
	if !filepath.IsAbs(cwd) {
		return ctx
	}
	if _, pinned := ctx.Value(hookActivityLocationKey{}).(hookActivityLocation); pinned {
		return ctx
	}
	location := hookActivityLocationAt(cwd)
	if location.base == "" {
		return ctx
	}
	return context.WithValue(ctx, hookActivityLocationKey{}, location)
}
