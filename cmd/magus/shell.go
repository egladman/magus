package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
)

// magus shell is the guard with one door.
//
// The rules it applies are the workspace's own conventions, and nothing about them is
// agent-shaped: `grep -r` has a better answer here whoever typed it, and so does a raw
// `go build`. They reached an agent host first only because a host had a hook to put them
// in, which is an accident of wiring rather than what they are for. A new contributor
// learns a workspace's tooling the slow way, by reading a CONTRIBUTING file nobody
// maintains or by being corrected in review a week later; this answers at the prompt, in
// the moment the habit is forming.
//
// A person and a host therefore run the SAME command, not two that agree. The only
// difference is how the input arrives: an operand for someone typing, stdin for a wrapper
// that already has the command in a variable. Verdict, flags, output formats and exit
// codes are one implementation, so there is no second surface to keep honest.
//
// It is deliberately not a sandbox and not a supervisor. Nothing is executed and nothing
// is prevented; the exit code is what a host chooses to block on.

// shellUsage describes the guard surface.
func shellUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus shell '<command>' [flags]   # judge one command")
	fmt.Fprintln(w, "       magus shell [flags]               # the command or path arrives on stdin")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Check a shell command against this workspace's conventions before running")
	fmt.Fprintln(w, "it, and print what to run instead when there is a better answer. The rules")
	fmt.Fprintln(w, "are the workspace's, not an agent's: a raw `go build` misses the cache and")
	fmt.Fprintln(w, "a `grep -r` misses what the symbol index already knows, whoever typed it.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Nothing is executed and nothing is prevented. This reports; you decide.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "A person passes the command as one quoted argument; a host pipes it on")
	fmt.Fprintln(w, "stdin, as plain text or as the JSON envelope it already writes. Same")
	fmt.Fprintln(w, "command, same verdict, same exit codes.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  magus shell 'go test ./...'")
	fmt.Fprintln(w, "  magus shell 'grep -rn HandleFoo internal/'")
	fmt.Fprintln(w, "  magus shell --path MAGUS.md")
	// Fprintf with %% : vet rejects a Printf directive inside an Fprintln, and the
	// example is worth more than the convenience. hookUsage did the same.
	fmt.Fprintf(w, "  printf '%%s' 'go build ./...' | magus shell\n")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --path                judge the input as a file path an edit is about to")
	fmt.Fprintln(w, "                        write, not as a shell command")
	fmt.Fprintln(w, "  --observe             record the path as one the agent REACHED and judge")
	fmt.Fprintln(w, "                        nothing; wire it to the tools that only look")
	fmt.Fprintln(w, "  --lease <id>          the job row this call acts as; the lease-scoped")
	fmt.Fprintln(w, "                        rules are graded against it, and the verdict names")
	fmt.Fprintln(w, "                        it. Defaults to magus.lease in $BAGGAGE; an id this")
	fmt.Fprintln(w, "                        workspace's job store does not declare is an error")
	fmt.Fprintln(w, "  --agent-name <name>   agent host this invocation came from (attribution")
	fmt.Fprintln(w, "                        only; the verdict never reads it)")
	fmt.Fprintln(w, "  --session <id>        the host's own session id, recorded on the event")
	fmt.Fprintln(w, "  --transcript <path>   the host's own log of this session, recorded as a")
	fmt.Fprintln(w, "                        pointer; magus never opens it")
	fmt.Fprintln(w, "  --event <name>        the host's hook event name (e.g. PreToolUse)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Exit codes: 2 is a deny, everything else is allowed. An advisory exits 0")
	fmt.Fprintln(w, "on purpose. Unreadable input is 2, so a host that blocks on 2 fails closed")
	fmt.Fprintln(w, "when bytes were lost on the way in.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "A verdict names the rule that produced it, in brackets. `"+hint.DescribeRules.String()+"`")
	fmt.Fprintln(w, "lists every rule this workspace enforces; `"+hint.DescribeRule.With("<name>")+"` details one.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global display flags (-o, -s, -q, -v, --tee) are accepted; see `magus -h`.")
}

// shellCmd implements `magus shell`: evaluate one shell command or file path and emit a
// verdict. The caller owns extraction from its own event shape; magus owns only the
// host-neutral policy.
//
// An EMPTY input fails OPEN: a wrapper that hands this nothing must not have every tool
// call blocked. An input that fails to READ is the opposite case and fails CLOSED,
// because bytes were on their way and were lost, so the guard has judged nothing and the
// command it never saw must not be reported as cleared.
func shellCmd(ctx context.Context, args []string) error {
	return shellCmdWithErrorWriter(ctx, os.Stdin, os.Stdout, os.Stderr, args)
}

// shellCmdWithErrorWriter is shellCmd's transport seam. The CLI reports a deny reason on
// stderr for machine-readable output, while a host adapter has already carried that
// reason in its host reply and must not leak a second, non-protocol message into the
// host's hook stream.
func shellCmdWithErrorWriter(ctx context.Context, in io.Reader, out, errOut io.Writer, args []string) error {
	fset := flag.NewFlagSet("magus shell", flag.ContinueOnError)
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
	sf := gen.BindShell(fset)
	// The environment supplies the DEFAULT, so an explicit --lease still wins: a shell
	// that exported the variable for a whole session must not outrank a per-call
	// override. Same shape `magus run` uses for MAGUS_SHARD.
	// trail.LeaseFromEnv, never a raw Getenv: the journal producers read the variable
	// through the same helper, and two readers with different trimming rules split one
	// exported lease into a journal identity and an unguarded write.
	// Discarded, unlike `magus run`'s shard pair: --lease is a plain string flag whose
	// Set cannot fail, and a guard that refused to answer over a malformed lease would
	// block the tool call this comment's first line says must always get a verdict.
	_ = envDefault(fset, gen.FlagShellLease, trail.LeaseFromEnv())
	// The whole display set, not a hand-rolled -o: this command used to define
	// its own output flag and so silently lacked -s, -q, -v and --tee. That gap
	// is the reason for the rule: a flag accepted on most commands teaches
	// callers it is unreliable everywhere.
	bindDisplayFlags(fset)
	fset.Usage = func() { shellUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}

	input, readErr := shellInput(in, fset.Args())
	if err, ok := readErr.(errUsage); ok { //nolint:errorlint // a usage error is this function's own, never wrapped
		return err
	}
	// A failed read is not an empty input, and collapsing the two cleared every
	// command whose payload arrived truncated. Answered as a deny so the exit is 2,
	// which is what the manpage promises: deny and unreadable input share the code,
	// so a host that blocks on 2 fails closed in both cases. --observe is exempt
	// because it carries no verdict: there is nothing to fail closed about, and the
	// documented contract is that it always exits 0.
	if readErr != nil && !sf.Observe {
		verdict := guard.Verdict{
			SchemaVersion: agent.GuardSchemaVersion,
			Decision:      "deny",
			// "from stdin" is accurate whichever way this command is called: an operand
			// is read off argv and cannot fail, so a read error is always the piped path.
			Reason: "magus shell could not read its input from stdin: " + readErr.Error() + "\n" +
				"Nothing was judged, so this call is blocked rather than cleared. Retry it.",
		}
		if err := writeGuardVerdict(out, opts, verdict); err != nil {
			return err
		}
		return enforceVerdictTo(errOut, opts, verdict)
	}
	verdict := guard.Judge(ctx, guardDependencies(), guard.Request{
		Input:      input,
		IsPath:     sf.Path,
		Observe:    sf.Observe,
		Lease:      sf.Lease,
		Host:       sf.AgentName,
		Session:    sf.Session,
		Transcript: sf.Transcript,
		Event:      sf.Event,
	})
	// -q and -s mean the exit code IS the answer, which this command can honor exactly
	// because its whole output is one verdict. They bound a run's progress chatter
	// everywhere else; here there is no progress, so suppressing the verdict is the only
	// reading that leaves them meaning anything at all. A script asking "would this be
	// refused" wants the status and nothing on its stdout.
	if global.quiet || global.silent {
		return enforceVerdictTo(io.Discard, opts, verdict)
	}
	if err := writeGuardVerdict(out, opts, verdict); err != nil {
		return err
	}
	return enforceVerdictTo(errOut, opts, verdict)
}

// shellInput resolves the command from the operand a person typed, or from stdin when
// they typed none.
//
// The operand wins, and stdin is not consulted when one is present: a person running this
// from a terminal has stdin attached to their keyboard, and reading it would hang on a
// command that was already supplied. Several operands means the quotes were left off,
// which is worth saying rather than judging the first word alone, because judging `go`
// and clearing it would report a pass for a command nobody ran.
func shellInput(in io.Reader, operands []string) (string, error) {
	switch len(operands) {
	case 0:
		return readGuardInput(in)
	case 1:
		return operands[0], nil
	default:
		return "", usagef("magus shell: quote the whole command as one argument: magus shell '%s'",
			strings.Join(operands, " "))
	}
}
