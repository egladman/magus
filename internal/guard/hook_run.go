package guard

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// RunHook judges one hook call whose command line parsed to call. The CLI reports a deny
// reason on stderr for machine-readable output, while a host adapter has already carried
// that reason in its host reply and must not leak a second, non-protocol message into the
// host's hook stream.
func (e HookEnv) RunHook(ctx context.Context, call HookCall, in io.Reader, out, errOut io.Writer) error {
	sf, opts := call.Flags, call.Output
	started := time.Now()
	// Started here rather than in Judge, so loading the rules spends the same budget.
	ctx, cancelInherited, deadlineErr := takeDelegation(ctx)
	defer cancelInherited()
	ctx, cancel := WithHookDeadline(ctx)
	defer cancel()
	// A deadline the handing magus spent is answered as a deny, never as an exit: a host
	// reads a failed guard as no verdict. --observe and --message carry no verdict to deny.
	if deadlineErr != nil && !sf.Observe && !sf.Message && sf.Duration == 0 {
		verdict := verdict.Judgment{
			APIVersion: agent.GuardAPIVersion,
			Decision:   verdict.Deny,
			Reason:     fmt.Sprintf("%s\nNothing was judged, so this call is blocked rather than cleared. Retry it.", deadlineErr.Error()),
		}
		if err := e.writeVerdict(out, opts, verdict); err != nil {
			return err
		}
		return verdictExit(errOut, opts, verdict)
	}

	input, readError := shellInput(in, call.Operands)
	if exit.Code(readError) == exit.Usage {
		return readError
	}
	// A failed read is not an empty input, and collapsing the two cleared every
	// command whose payload arrived truncated. Answered as a deny so the exit is 2,
	// which is what the manpage promises: deny and unreadable input share the code,
	// so a host that blocks on 2 fails closed in both cases. --observe and --message are
	// exempt because they carry no verdict: there is nothing to fail closed about, and
	// the documented contract is that they always exit 0.
	if readError != nil && !sf.Observe && !sf.Message && sf.Duration == 0 {
		verdict := verdict.Judgment{
			APIVersion: agent.GuardAPIVersion,
			Decision:   verdict.Deny,
			// "from stdin" is accurate whichever way this command is called: an operand
			// is read off argv and cannot fail, so a read error is always the piped path.
			Reason: fmt.Sprintf("magus shell could not read its input from stdin: %s\nNothing was judged, so this call is blocked rather than cleared. Retry it.", readError.Error()),
		}
		if err := e.writeVerdict(out, opts, verdict); err != nil {
			return err
		}
		return verdictExit(errOut, opts, verdict)
	}
	var judge, judgeRoot string
	if sf.Transport != "" {
		handled, declined, err := e.delegateToCheckout(ctx, call.Argv, input, opts, out, errOut)
		if handled {
			return err
		}
		judge, judgeRoot = declined.note, e.unloadableCheckout(ctx, declined.root)
	}
	if by := delegatedBy(ctx); by != "" {
		self, _ := e.BinaryPath()
		judge = fmt.Sprintf("Judged by %s, this checkout's own ./magus, which %s handed the call to.", self, by)
	}
	// An environment or magus.yaml this magus cannot read loads no policy, so nothing
	// judges the call: only the workspace's repair passes, as the glue's startup answers.
	envRefusal, _ := ctx.Value(envRefusalKey{}).(error)
	staleConfig, _ := ctx.Value(staleConfigKey{}).(error)
	if cause := cmp.Or(envRefusal, staleConfig); cause != nil && !sf.Observe && !sf.Message && sf.Duration == 0 {
		root, _ := e.FindRoot(judgeRoot)
		answer, reason := UnjudgedEvent(root, input, cause)
		verdict := verdict.Judgment{APIVersion: agent.GuardAPIVersion, Decision: answer.Decision(), Reason: reason, Rule: answer.Rule()}
		if verdict.Decision == "advise" {
			verdict.Reason, verdict.Context = "", reason
		}
		if err := e.writeVerdict(out, opts, verdict); err != nil {
			return err
		}
		return verdictExit(errOut, opts, verdict)
	}
	// A hook's stdout is the host's pipe; a terminal on it means someone typed this.
	window := call.Window
	entry := job.EntryPointHook
	if window != "" {
		entry = job.EntryPointCLI
	}
	req := Request{
		Input:      input,
		IsPath:     sf.Path,
		Observe:    sf.Observe,
		Message:    sf.Message,
		Lease:      sf.Lease,
		Host:       sf.AgentName,
		Form:       sf.Transport,
		Session:    strings.TrimSpace(sf.Session),
		Agent:      strings.TrimSpace(sf.Agent),
		Window:     window,
		Transcript: sf.Transcript,
		Event:      sf.Event,
		// Declared by the wiring, because only a glue that renders the host's approval
		// prompt can say so, and one that predates ask renders it as an allow.
		RendersAsk:     sf.RendersAsk,
		RendersContext: sf.RendersContext,
		// The call's own word on how the host runs it.
		CallBackground: sf.CallBackground,
		CallTimeout:    sf.CallTimeout,
		ToolMs:         sf.Duration.Milliseconds(),
		CallDigest:     sf.CallDigest,
		ResultDigest:   sf.ResultDigest,
	}
	// A misnamed host is refused, never judged as another.
	// --observe and --message carry no verdict, so they log it.
	limits, known, refusal := e.applyHostFacts(ctx, &req)
	if refusal != nil {
		if !sf.Observe && !sf.Message && sf.Duration == 0 {
			verdict := verdict.Judgment{
				APIVersion: agent.GuardAPIVersion,
				Decision:   verdict.Deny,
				Reason: fmt.Sprintf("%s\nNothing was judged, so this call is blocked rather than cleared.\nNext: %s",
					refusal.err.Error(), refusal.next.Run),
				NextSteps: remedies([]types.NextStep{refusal.next}),
			}
			if err := e.writeVerdict(out, opts, verdict); err != nil {
				return err
			}
			return verdictExit(errOut, opts, verdict)
		}
		slog.WarnContext(ctx, "the hook entry names the wrong harness",
			attr.Error(refusal.err), attr.Notice(""), attr.Component("magus shell"))
	}
	// Marked before the rules load, so a hook its host kills while loading them is still
	// reported to the session's next judgment.
	ctx = BeginJudgment(ctx, e.CacheDir(""), req)
	ctx = recorder.ContextWithEntryPoint(ctx, entry)
	// The spell catalog is compiled in, so the raw-tool rule judges a lapsed call too.
	base := Dependencies{CacheDir: e.CacheDir(""), BinaryVersion: e.Version, Spells: project.DefaultSpellRegistry().All}
	base.Binary, _ = e.BinaryPath() // an unresolved binary leaves Binary empty, which the rules read as unknown
	ctx = agent.ContextWithToolRows(ctx, req.Tools)
	ctx, finishSnapshot := WithPolicySnapshotWrite(ctx)
	// The rules load inside the bound too: a magusfile's imports parse to the end however
	// long they take.
	verdict := JudgeWithin(ctx, base, req, func(ctx context.Context) verdict.Judgment {
		// The hook ignores --root and loads no workspace up front: the one it judges in is
		// wherever the host runs it, or a checkout whose own ./magus was passed over and whose
		// tree this binary cannot load, so its stale-binary rule refuses what changes state.
		if judgeRoot != "" {
			if cache, err := e.CacheDir(judgeRoot)(judgeRoot); err == nil {
				ctx = WithLocation(ctx, cache, judgeRoot, judgeRoot)
			}
		}
		// A leased call is judged by its row's checkout and authority, so
		// the lease resolves before the policy loads.
		deps, _ := e.loadDependenciesAt(ctx, judgeRoot, ResolveAuthority(ctx, base, req))
		stopJudge := e.phase(ctx, "guard.judge")
		defer stopJudge()
		return Judge(ctx, deps, req)
	})
	verdict = routeDeny(ctx, limits, known, judgedBy(verdict, judge))
	// -q and -s mean the exit code IS the answer, which this command can honor exactly
	// because its whole output is one verdict. They bound a run's progress chatter
	// everywhere else; here there is no progress, so suppressing the verdict is the only
	// reading that leaves them meaning anything at all. A script asking "would this be
	// refused" wants the status and nothing on its stdout.
	var exitErr error
	if call.Quiet {
		exitErr = verdictExit(io.Discard, opts, verdict)
	} else {
		if err := e.writeVerdict(out, opts, verdict); err != nil {
			return err
		}
		exitErr = verdictExit(errOut, opts, verdict)
	}
	if err := finishSnapshot(ctx, snapshotBudget(limits, known, started)); err != nil {
		slog.WarnContext(ctx, "the policy snapshot was not written, so the next judgment loads the policy too",
			attr.Error(err), attr.Component("magus shell"))
	}
	return exitErr
}

// snapshotBudget is what a hook may spend on a snapshot write its judgment
// left over. The host's hook timeout less a second bounds the whole hook.
// A host that declares no timeout gets no wait.
func snapshotBudget(limits types.Limits, known bool, started time.Time) time.Duration {
	if !known || limits.HookTimeoutMs <= 0 {
		return 0
	}
	return time.Duration(limits.HookTimeoutMs)*time.Millisecond - time.Second - time.Since(started)
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
		return "", exit.Usagef("magus shell: quote the whole command as one argument: magus shell '%s'",
			strings.Join(operands, " "))
	}
}
