package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/egladman/magus/cmd/magus/gen"
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
//
// This is the ONLY producer of attention requests. An event that means the work
// has stopped until a person acts also opens a durable request `magus session attention`
// lists; there is deliberately no `attention raise` twin, because one ingest path
// is what keeps the queue's contents traceable to a single normalization.
func notifyCmd(ctx context.Context, root string, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("notify", flag.ContinueOnError)
	bindDisplayFlags(fset)
	nf := gen.BindSessionNotify(fset)
	fset.Usage = func() { notifyUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus session notify: takes no positional arguments (read the event from stdin)")
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}

	body, readErr := io.ReadAll(in)
	if readErr != nil {
		return fmt.Errorf("magus session notify: read stdin: %w", readErr)
	}

	ev := eventFromStdin(body)
	if nf.Outcome != "" {
		ev.Outcome = classifyOutcome(nf.Outcome)
	} else {
		ev.Outcome = classifyOutcome(string(ev.Outcome))
	}
	normalizeEvent(&ev)

	if err := recordAttentionOpen(root, ev); err != nil {
		noteAttentionOpenFailure(err)
	}

	if nf.Desktop {
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
//
// A body that LOOKS like an envelope and fails is warned about first. The demotion is
// still the right outcome - the notification fires either way, and a hook must not exit
// non-zero over metadata - but silence made it undiagnosable: a producer sending a
// half-built envelope saw a working notification, an empty source, and therefore no
// attention request, with nothing anywhere saying why.
func eventFromStdin(body []byte) types.Event {
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var ev types.Event
		err := json.Unmarshal([]byte(trimmed), &ev)
		if err == nil && ev.Message != "" && ev.Outcome != "" && ev.Source.Kind != "" {
			return ev
		}
		noteUnusableEnvelope(err, ev)
	}
	return types.Event{Message: trimmed}
}

// noteUnusableEnvelope reports a JSON object that could not be read as an event, and
// names which half failed: the parse, or the three fields an envelope must carry. A
// producer debugging this cannot see either from the outside - both paths produce the
// same working plain-text notification.
func noteUnusableEnvelope(err error, ev types.Event) {
	if err != nil {
		slog.Warn("magus session notify: stdin looks like a JSON envelope but did not parse, so it was sent as plain text; no attention request can be opened from a prose message",
			slog.String("error", err.Error()))
		return
	}
	var missing []string
	if ev.Message == "" {
		missing = append(missing, "message")
	}
	if ev.Outcome == "" {
		missing = append(missing, "outcome")
	}
	if ev.Source.Kind == "" {
		missing = append(missing, "source.kind")
	}
	slog.Warn("magus session notify: stdin parsed as JSON but is not a complete event envelope, so it was sent as plain text; no attention request can be opened from a prose message",
		slog.String("missing", strings.Join(missing, ", ")),
		slog.String("next", "send message, outcome and source.kind together, and source.id to make the event addressable as a request"))
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
	fmt.Fprintln(w, "Usage: magus session notify [--outcome <vocab>] [--desktop]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Raise one canonical attention event. Input may be plain text, a complete")
	fmt.Fprintln(w, "types.Event JSON envelope. --desktop additionally raises an OS notification.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "An event whose outcome is waiting or permission also opens a durable request")
	fmt.Fprintln(w, "in this repository, which `magus session attention` lists and only a person closes.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintf(w, "  printf '%%s\\n' 'needs approval' | magus session notify --outcome permission --desktop\n")
	fmt.Fprintf(w, "  printf '%%s\\n' '{\"outcome\":\"permission\",\"source\":{\"kind\":\"agent\"},\"message\":\"needs approval\"}' | magus session notify --desktop\n")
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
