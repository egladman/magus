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

	json "github.com/egladman/magus/internal/codec"
)

// attentionEvent is the neutral shape of "an agent needs a human".
//
// Mirrors guardVerdict deliberately: one envelope, host-specific dialects
// expressed as documented wiring rather than as code. Every agent host spells
// its events differently and renames them between releases, so a switch over
// host names in here would be wrong within a month - and would break the
// host-agnostic rule the arch test enforces. The per-host mapping lives in the
// documentation, which is the only place it can be corrected without a release.
//
// WHY A HOOK AND NOT MCP. An MCP server only observes tool CALLS. When an agent
// is blocked on a permission prompt or sitting idle waiting for input, it makes
// no call at all - the blockage IS the silence, and silence is exactly what MCP
// cannot report. The agent host's own hook system is the only surface that fires
// on it, which is why this is a hook sink and not a tool.
type attentionEvent struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`                // waiting | stopped | permission | other
	Session       string `json:"session,omitempty"`   // host session id, when it supplies one
	Message       string `json:"message,omitempty"`   // what the host said it needs
	Workspace     string `json:"workspace,omitempty"` // repo root, so a multi-repo desktop can tell them apart
	Title         string `json:"title"`               // rendered, ready for a notifier
	Body          string `json:"body"`                // rendered
}

const attentionSchemaVersion = 1

// attentionKinds maps event vocabulary onto the four kinds this envelope
// carries, by case-insensitive substring.
//
// Every entry is a GENERIC English word for the situation, never a particular
// host's event identifier. That is a hard rule here, not a preference: encoding
// one host's spelling would both rot on its next release and violate the
// host-agnostic boundary. A host whose event name happens to contain none of
// these words is wired by passing the kind directly - the four canonical names
// are accepted verbatim, so the documented per-host invocation can always
// resolve it without magus knowing anything about that host.
//
// An unrecognized kind is not an error. It becomes "other" and still notifies,
// because a missed alert is the failure this command exists to prevent.
var attentionKinds = []struct{ match, kind string }{
	{"permission", "permission"},
	{"approval", "permission"},
	{"authorize", "permission"},
	{"waiting", "waiting"},
	{"await", "waiting"},
	{"idle", "waiting"},
	{"input", "waiting"},
	{"prompt", "waiting"},
	{"notif", "waiting"},
	{"stop", "stopped"},
	{"finish", "stopped"},
	{"complete", "stopped"},
	{"done", "stopped"},
}

func classifyAttention(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	for _, k := range attentionKinds {
		if strings.Contains(lower, k.match) {
			return k.kind
		}
	}
	return "other"
}

// agentNotifyCmd implements `magus agent notify`: turn one agent-host event into
// a neutral attention record, render it, and optionally raise a desktop
// notification.
//
// Like `agent hook`, it FAILS OPEN. A notifier that errors must never become a
// hook that blocks the session it was meant to watch.
func agentNotifyCmd(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("agent notify", flag.ContinueOnError)
	bindDisplayFlags(fset)
	kind := fset.String("kind", "", "Event kind: one of waiting|stopped|permission|other, or whatever your host calls it (classified by substring)")
	fromJSON := fset.String("from-json", "", "Read the event from a JSON document on stdin, taking the message at this dot-separated path")
	kindJSON := fset.String("kind-from-json", "", "Read the event KIND from the same stdin document at this dot-separated path")
	desktop := fset.Bool("desktop", false, "Also raise a desktop notification (macOS osascript, Linux notify-send)")
	fset.Usage = func() { agentNotifyUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}

	ev := attentionEvent{SchemaVersion: attentionSchemaVersion, Message: strings.Join(fset.Args(), " ")}
	if *fromJSON != "" || *kindJSON != "" {
		doc, err := io.ReadAll(in)
		if err == nil {
			if *fromJSON != "" {
				if s, err := extractJSONString(doc, *fromJSON); err == nil {
					ev.Message = s
				}
			}
			if *kindJSON != "" {
				if s, err := extractJSONString(doc, *kindJSON); err == nil && *kind == "" {
					*kind = s
				}
			}
			if s, err := extractJSONString(doc, "session_id"); err == nil {
				ev.Session = s
			}
		}
	}
	ev.Kind = classifyAttention(*kind)
	if root, err := os.Getwd(); err == nil {
		ev.Workspace = root
	}
	ev.Title, ev.Body = renderAttention(ev)

	if *desktop {
		// Best effort by design: a missing notifier is not a hook failure.
		_ = raiseDesktopNotification(ctx, ev)
	}

	switch opts.Format {
	case FormatText:
		fmt.Fprintf(out, "%s: %s\n", ev.Title, ev.Body)
		return nil
	case FormatName:
		fmt.Fprintln(out, ev.Kind)
		return nil
	}
	return writeFormatted(out, opts, ev)
}

func agentNotifyUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent notify [--kind <host-event>] [--from-json <path>] [--desktop] [message...]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Turn one agent-host event into a neutral attention record. Wire it to")
	fmt.Fprintln(w, "whichever event your host fires when it needs a human; the per-host")
	fmt.Fprintln(w, "event names are in the Agents documentation, not in this binary.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "MCP cannot serve this: a blocked agent makes no tool call, so the")
	fmt.Fprintln(w, "blockage is the silence. Only the host's hook system fires on it.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  magus agent notify --kind permission --desktop \"needs approval\"")
	fmt.Fprintln(w, "  magus agent notify --kind-from-json hook_event_name --from-json message --desktop")
}

// renderAttention produces the one-line title and body a notifier wants.
func renderAttention(ev attentionEvent) (title, body string) {
	switch ev.Kind {
	case "permission":
		title = "magus: agent needs approval"
	case "waiting":
		title = "magus: agent is waiting for you"
	case "stopped":
		title = "magus: agent finished"
	default:
		title = "magus: agent needs attention"
	}
	body = ev.Message
	if body == "" {
		body = "no detail supplied by the host"
	}
	if ev.Workspace != "" {
		body += " (" + ev.Workspace + ")"
	}
	return title, body
}

// raiseDesktopNotification uses whatever the platform already has, rather than
// taking a dependency for it.
func raiseDesktopNotification(ctx context.Context, ev attentionEvent) error {
	switch runtime.GOOS {
	case "darwin":
		// AppleScript string literals need their quotes and backslashes escaped,
		// or a message containing either silently breaks the script.
		script := fmt.Sprintf("display notification %s with title %s",
			osaQuote(ev.Body), osaQuote(ev.Title))
		return exec.CommandContext(ctx, "osascript", "-e", script).Run()
	case "linux":
		return exec.CommandContext(ctx, "notify-send", ev.Title, ev.Body).Run()
	default:
		return nil
	}
}

func osaQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
