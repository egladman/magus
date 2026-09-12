package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
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
	fmt.Fprintln(w, "Global display flags (-o, -s, -q, -v, --tee) are accepted; see `magus -h`.")
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
	_ = envDefault(fset, gen.FlagSessionHookLease, trail.LeaseFromEnv())
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
		verdict := guard.Verdict{
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
	verdict := guard.Judge(ctx, hookDependencies(), guard.Request{
		Input:      input,
		IsPath:     hf.Path,
		Observe:    hf.Observe,
		Lease:      hf.Lease,
		Host:       hf.AgentName,
		Session:    hf.Session,
		Transcript: hf.Transcript,
		Event:      hf.Event,
	})
	if err := writeGuardVerdict(out, opts, verdict); err != nil {
		return err
	}
	return enforceVerdict(opts, verdict)
}

// hookDependencies hands the guard the five workspace facts its rules cannot resolve for
// themselves: the memoized inspect, this process's loaded config, the index-staleness
// advice, and the spell catalog, all of which live in the CLI rather than in the rules.
func hookDependencies() guard.Dependencies {
	return guard.Dependencies{
		Inspect: func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
			if root == "" {
				return inspectWorkspace(ctx, "")
			}
			return magus.Inspect(ctx, root,
				magus.WithLoadedConfig(globalCfg), magus.WithVersion(version))
		},
		CacheDir: func(root string) (string, error) {
			if root != "" {
				return magus.ResolveCacheDir(root)
			}
			found, err := magus.FindRoot("")
			if err != nil {
				return "", err
			}
			return magus.ResolveCacheDir(found, magus.WithLoadedConfig(globalCfg))
		},
		NotesShared:      globalCfg.Knowledge.Notes.Shared,
		GraphStaleAdvice: staleGraphAdvice,
		Spells:           project.DefaultSpellRegistry().All,
	}
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
func enforceVerdict(opts OutputOptions, verdict guard.Verdict) error {
	if verdict.Decision != "deny" {
		return nil
	}
	if opts.Format != FormatText {
		fmt.Fprintln(os.Stderr, verdict.Reason)
	}
	return errSilent{exitCode: guardDenyExitCode}
}

// writeGuardVerdict renders a verdict through the standard output arm.
func writeGuardVerdict(out io.Writer, opts OutputOptions, verdict guard.Verdict) error {
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
