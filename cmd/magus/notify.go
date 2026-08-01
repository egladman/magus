package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// notifyCmd turns one attention event into a stable types.Event record. It is a
// local command: a notification belongs on the caller's desktop, never on a
// daemon host. A producer sends either the canonical JSON envelope or plain
// text on stdin. A host-specific wrapper is responsible for shaping its own
// event before it crosses this boundary.
//
// Notification delivery is deliberately best effort. Hosts must be able to
// invoke this from a hook without a missing desktop notifier breaking the agent
// session that the notification is meant to help.
func notifyCmd(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("notify", flag.ContinueOnError)
	bindDisplayFlags(fset)
	outcome := fset.String("outcome", "", "Event outcome (waiting|permission|failed|finished|diagnostic|update|other), or producer vocabulary classified by substring")
	desktop := fset.Bool("desktop", false, "Also raise a desktop notification (macOS osascript, Linux notify-send)")
	fset.Usage = func() { notifyUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus notify: takes no positional arguments (read the event from stdin)")
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}

	body, readErr := io.ReadAll(in)
	if readErr != nil {
		return fmt.Errorf("magus notify: read stdin: %w", readErr)
	}

	ev := eventFromStdin(body)
	if *outcome != "" {
		ev.Outcome = classifyOutcome(*outcome)
	} else {
		ev.Outcome = classifyOutcome(string(ev.Outcome))
	}
	normalizeEvent(&ev)

	if *desktop {
		_ = raiseDesktopNotification(ctx, ev)
	}

	switch opts.Format {
	case FormatText:
		title, message := renderNotification(ev)
		fmt.Fprintf(out, "%s: %s\n", title, message)
		return nil
	case FormatName:
		fmt.Fprintln(out, ev.Outcome)
		return nil
	}
	return writeFormatted(out, opts, ev)
}

// eventFromStdin accepts only a complete canonical event as structured input.
// Anything else remains a plain-text message. That strict boundary avoids
// quietly treating a producer's partial JSON object as a valid wire contract.
func eventFromStdin(body []byte) types.Event {
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var ev types.Event
		if err := json.Unmarshal([]byte(trimmed), &ev); err == nil && ev.Message != "" && ev.Outcome != "" && ev.Source.Kind != "" {
			return ev
		}
	}
	return types.Event{Message: trimmed}
}

func normalizeEvent(ev *types.Event) {
	if ev.SchemaVersion == 0 {
		ev.SchemaVersion = types.EventSchemaVersion
	}
	if ev.Source.Kind == "" {
		ev.Source.Kind = "magus"
		ev.Source.Sub = "notify"
	}
	if ev.Severity == "" {
		ev.Severity = severityForOutcome(ev.Outcome)
	}
	if ev.Where == nil {
		if root, err := os.Getwd(); err == nil {
			ev.Where = &types.EventLocation{Workspace: types.Path{Value: root, IsDir: true}}
		}
	}
}

var outcomeVocabulary = []struct {
	match   string
	outcome types.EventOutcome
}{
	{"permission", types.OutcomePermission},
	{"approval", types.OutcomePermission},
	{"authorize", types.OutcomePermission},
	{"waiting", types.OutcomeWaiting},
	{"await", types.OutcomeWaiting},
	{"idle", types.OutcomeWaiting},
	{"input", types.OutcomeWaiting},
	{"prompt", types.OutcomeWaiting},
	{"notif", types.OutcomeWaiting},
	{"fail", types.OutcomeFailed},
	{"error", types.OutcomeFailed},
	{"stop", types.OutcomeFinished},
	{"finish", types.OutcomeFinished},
	{"complete", types.OutcomeFinished},
	{"done", types.OutcomeFinished},
	{"diagnos", types.OutcomeDiagnostic},
	{"update", types.OutcomeUpdate},
}

func classifyOutcome(raw string) types.EventOutcome {
	lower := strings.ToLower(strings.TrimSpace(raw))
	for _, candidate := range outcomeVocabulary {
		if strings.Contains(lower, candidate.match) {
			return candidate.outcome
		}
	}
	return types.OutcomeOther
}

func severityForOutcome(outcome types.EventOutcome) types.EventSeverity {
	switch outcome {
	case types.OutcomePermission, types.OutcomeFailed:
		return types.SeverityCritical
	case types.OutcomeWaiting, types.OutcomeDiagnostic:
		return types.SeverityWarning
	case types.OutcomeFinished:
		return types.SeverityInfo
	default:
		return types.SeverityNotice
	}
}

func notifyUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus notify [--outcome <vocab>] [--desktop]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Raise one canonical attention event. Input may be plain text, a complete")
	fmt.Fprintln(w, "types.Event JSON envelope. --desktop additionally raises an OS notification.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintf(w, "  printf '%%s\\n' 'needs approval' | magus notify --outcome permission --desktop\n")
	fmt.Fprintf(w, "  printf '%%s\\n' '{\"outcome\":\"permission\",\"source\":{\"kind\":\"agent\"},\"message\":\"needs approval\"}' | magus notify --desktop\n")
}

func renderNotification(ev types.Event) (title, body string) {
	switch ev.Outcome {
	case types.OutcomePermission:
		title = "magus: needs approval"
	case types.OutcomeWaiting:
		title = "magus: waiting for you"
	case types.OutcomeFailed:
		title = "magus: action failed"
	case types.OutcomeFinished:
		title = "magus: finished"
	default:
		title = "magus: needs attention"
	}
	body = ev.Message
	if body == "" {
		body = "no detail supplied"
	}
	if ev.Where != nil && ev.Where.Workspace.Value != "" {
		body += " (" + ev.Where.Workspace.Value + ")"
	}
	return title, body
}

func raiseDesktopNotification(ctx context.Context, ev types.Event) error {
	title, body := renderNotification(ev)
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "osascript", "-e", `on run argv
	display notification item 1 of argv with title item 2 of argv
end run`, body, title).Run()
	case "linux":
		return exec.CommandContext(ctx, "notify-send", title, body).Run()
	}
	return nil
}
