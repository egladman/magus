package guard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// takeDelegation marks ctx with the binary that handed this call on, if one
// did, and bounds it to the deadline that binary passed. Both variables leave
// this process's environment, so nothing it spawns, a daemon included, reads
// itself delegated.
func takeDelegation(ctx context.Context) (context.Context, context.CancelFunc, error) {
	by, deadline := os.Getenv(delegatedEnv), os.Getenv(HookDeadlineEnv)
	_ = os.Unsetenv(delegatedEnv)    // a constant name, so Unsetenv cannot fail
	_ = os.Unsetenv(HookDeadlineEnv) // a constant name, so Unsetenv cannot fail
	if by == "" {
		return ctx, func() {}, nil
	}
	return WithInheritedHookDeadline(context.WithValue(ctx, delegatedByKey{}, by), deadline)
}

type delegatedByKey struct{}

// delegatedBy is the binary that handed this call on, "" when none did.
func delegatedBy(ctx context.Context) string {
	by, _ := ctx.Value(delegatedByKey{}).(string)
	return by
}

// delegatedEnv marks a guard call one magus handed to the checkout's own ./magus, so that
// one judges instead of handing it on again.
const delegatedEnv = "__MAGUS_DELEGATED_BY"

// hookEvent is the part of a host's event that says where the call lands.
type hookEvent struct {
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command      string `json:"command"`
		Cmd          string `json:"cmd"`
		Workdir      string `json:"workdir"`
		FilePath     string `json:"file_path"`
		Path         string `json:"path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

// hookCallDir is the directory a host event's call lands in: the file a write names, else the
// workspace a shell line's calls all run in, else the shell tool's workdir, else the
// session's cwd. "" when the input is a bare command or names none.
func hookCallDir(input string) string {
	if !strings.HasPrefix(strings.TrimSpace(input), "{") {
		return ""
	}
	var ev hookEvent
	if codec.Unmarshal([]byte(input), &ev) != nil {
		return ""
	}
	at := func(p string) string {
		if p == "" || filepath.IsAbs(p) || ev.Cwd == "" {
			return p
		}
		return filepath.Join(ev.Cwd, p)
	}
	for _, p := range []string{ev.ToolInput.FilePath, ev.ToolInput.NotebookPath, ev.ToolInput.Path} {
		if p != "" {
			return filepath.Dir(at(p))
		}
	}
	dir := ev.Cwd
	if ev.ToolInput.Workdir != "" {
		dir = at(ev.ToolInput.Workdir)
	}
	if root := LineWorkspace(cmp.Or(ev.ToolInput.Command, ev.ToolInput.Cmd), dir); root != "" {
		return root
	}
	return dir
}

// BinaryCheckout is the workspace root bin sits at, or "" when bin is not
// a checkout's own ./magus. The glue starts the session checkout's binary.
// The event's cwd is no anchor: a persisted cd or a workdir moves it.
func BinaryCheckout(bin string) string {
	dir := filepath.Dir(bin)
	root, err := workspace.FindRoot(dir)
	if err != nil || evalOrClean(root) != evalOrClean(dir) {
		return ""
	}
	return dir
}

// UnpinnedBinary says why the session's job store does not pin own, "" when a
// live row records own and its hash (ADR 0045 section 4). The read skips the
// sweep's writes.
func (e HookEnv) UnpinnedBinary(ctx context.Context, own, session string) string {
	store, err := e.OpenJobs(ctx, session)
	if err != nil {
		return fmt.Sprintf("the job store of %s, which pins every binary this hands a call to, did not open: %v", session, err)
	}
	why, err := store.PinRefusal(ctx, own)
	if err != nil {
		return fmt.Sprintf("whether a job row pins it cannot be read: %v", err)
	}
	return why
}

// evalOrClean is p with symlinks resolved, or p cleaned when they cannot be. Not realPath,
// which resolves the nearest existing ancestor instead.
func evalOrClean(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// CheckoutBinary is the ./magus of the workspace dir lies in, when it is executable and is
// not the binary at running; "" otherwise. A directory not created yet is judged by the
// nearest one that exists.
func CheckoutBinary(dir, running string) string {
	for dir != "" {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		if parent := filepath.Dir(dir); parent != dir {
			dir = parent
			continue
		}
		return ""
	}
	if dir == "" {
		return ""
	}
	root, err := workspace.FindRoot(dir)
	if err != nil {
		return ""
	}
	own := filepath.Join(root, "magus")
	info, err := os.Stat(own)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	if mine, err := os.Stat(running); err == nil && os.SameFile(info, mine) {
		return ""
	}
	return own
}

// delegateToCheckout hands a hook call, argv and input, to the ./magus of the
// checkout it lands in, when a live row of the session's job store pins it.
// A ./magus behind HEAD judges too, and its own stale-binary
// rules name the bootstrap. An empty argv is declined, since a bare ./magus
// answers with its usage. On handled false this binary judges, and declined
// says why.
func (e HookEnv) delegateToCheckout(ctx context.Context, argv []string, input string, opts output.Options, out, errOut io.Writer) (handled bool, declined checkoutDecline, err error) {
	if delegatedBy(ctx) != "" {
		return false, checkoutDecline{}, nil
	}
	running, err := e.HookBinaryPath()
	if err != nil {
		slog.WarnContext(ctx, "this binary's own path is unknown, so it judges the call instead of handing it on",
			attr.Error(err), attr.Component("magus shell"))
		return false, checkoutDecline{}, nil
	}
	own := CheckoutBinary(hookCallDir(input), running)
	if own == "" {
		return false, checkoutDecline{}, nil
	}
	decline := func(root, why string) checkoutDecline {
		d := checkoutDecline{root: root, note: fmt.Sprintf("Judged by %s, not by %s: %s.", running, own, why)}
		slog.WarnContext(ctx, d.note, attr.Component("magus shell"))
		return d
	}
	if len(argv) == 0 {
		return false, decline("", "the hook's command line was not handed to this binary, so it has nothing to forward"), nil
	}
	session := BinaryCheckout(running)
	if session == "" {
		return false, decline("", "this binary is no checkout's own ./magus, so it trusts no checkout's binary"), nil
	}
	// An unpinned root's rules are not loaded either: this binary judges the
	// call as it would one into a tree with no ./magus.
	if why := e.UnpinnedBinary(ctx, own, session); why != "" {
		return false, decline("", why), nil
	}
	// The sentinel keeps the delegate from re-execing into the ./magus of this process's
	// directory, which is the binary handing the call over.
	env := append(os.Environ(), fmt.Sprintf("%s=%s", delegatedEnv, running), workspace.BootstrapExecSentinelVar+"=1")
	end, err := RunDelegate(ctx, own, argv, env, strings.NewReader(input), out, errOut)
	switch end {
	case DelegateExited:
	case DelegateKilledUnanswered:
		verdict := verdict.Judgment{
			APIVersion: agent.GuardAPIVersion,
			Decision:   verdict.Deny,
			Reason:     fmt.Sprintf("%s was killed unanswered at the hook's deadline, so this call is blocked. Retry it.", own),
		}
		if werr := e.writeVerdict(out, opts, verdict); werr != nil {
			return true, checkoutDecline{}, werr
		}
		return true, checkoutDecline{}, verdictExit(errOut, opts, verdict)
	case DelegateKilledAnswering:
		// A second verdict after its bytes would garble the host's reply.
		return true, checkoutDecline{}, exit.Reported(guardDenyExitCode)
	}
	var exited *exec.ExitError
	switch {
	case err == nil, errors.Is(err, exec.ErrWaitDelay):
		// ErrWaitDelay: it exited 0, and a grandchild held its output open.
		return true, checkoutDecline{}, nil
	case errors.As(err, &exited):
		return true, checkoutDecline{}, exit.Reported(exited.ExitCode())
	}
	return false, decline(filepath.Dir(own), fmt.Sprintf("it did not start: %v", err)), nil
}

// checkoutDecline is a checkout whose own ./magus was passed over, and the note saying so.
type checkoutDecline struct{ root, note string }

// unloadableCheckout is root when this binary cannot load its guard policy, "" otherwise.
// The load costs a judgment only after its checkout's ./magus was passed over.
func (e HookEnv) unloadableCheckout(ctx context.Context, root string) string {
	if root == "" {
		return ""
	}
	if _, err := e.LoadRules(ctx, root); err == nil {
		return ""
	}
	return root
}

// judgedBy appends note, which says which binary judged, to the verdict's text. A pass
// carries no text to append to.
func judgedBy(verdict verdict.Judgment, note string) verdict.Judgment {
	if note == "" {
		return verdict
	}
	switch verdict.Decision {
	case "deny", "ask":
		verdict.Reason = strings.TrimSpace(strings.Join([]string{verdict.Reason, note}, "\n"))
	case "advise":
		verdict.Context = strings.TrimSpace(strings.Join([]string{verdict.Context, note}, "\n"))
	}
	return verdict
}
