package sessions

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/types"
)

// EventFromStdin accepts only a complete canonical event as structured input.
// Anything else remains a plain-text message. That strict boundary avoids
// quietly treating a producer's partial JSON object as a valid wire contract.
//
// A body that LOOKS like an envelope and fails is warned about first. The demotion is
// still the right outcome (the notification fires either way, and a hook must not exit
// non-zero over metadata), but silence made it undiagnosable: a producer sending a
// half-built envelope saw a working notification, an empty source, and therefore no
// attention request, with nothing anywhere saying why.
func EventFromStdin(ctx context.Context, body []byte) types.Event {
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var ev types.Event
		err := codec.Unmarshal([]byte(trimmed), &ev)
		if err == nil && ev.Message != "" && ev.Outcome != "" && ev.Source.Kind != "" {
			return ev
		}
		noteUnusableEnvelope(ctx, err, ev)
	}
	return types.Event{Message: trimmed}
}

// noteUnusableEnvelope reports a JSON object that could not be read as an event, and
// names which half failed: the parse, or the three fields an envelope must carry. A
// producer debugging this cannot see either from the outside: both paths produce the
// same working plain-text notification.
func noteUnusableEnvelope(ctx context.Context, err error, ev types.Event) {
	if err != nil {
		slog.With(attr.Component("session notify")).WarnContext(ctx, "stdin looks like a JSON envelope but did not parse, so it was sent as plain text; no attention request can be opened from a prose message",
			attr.Error(err))
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
	slog.With(attr.Component("session notify")).WarnContext(ctx, "stdin parsed as JSON but is not a complete event envelope, so it was sent as plain text; no attention request can be opened from a prose message",
		slog.String("missing", strings.Join(missing, ", ")),
		attr.Why("send message, outcome and source.kind together, and source.id to make the event addressable as a request"))
}

// NormalizeEvent fills the fields a producer may leave out.
func NormalizeEvent(ev *types.Event) {
	if ev.APIVersion == "" {
		ev.APIVersion = types.EventAPIVersion
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

// outcomeVocabulary orders the words ClassifyOutcome tries; the first match wins.
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

// ClassifyOutcome maps a producer's free-form word onto the outcome vocabulary.
func ClassifyOutcome(raw string) types.EventOutcome {
	lower := strings.ToLower(strings.TrimSpace(raw))
	for _, candidate := range outcomeVocabulary {
		if strings.Contains(lower, candidate.match) {
			return candidate.outcome
		}
	}
	return types.OutcomeOther
}

// severityForOutcome is how loudly an outcome asks for attention.
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

// RenderNotification is the title and body an event raises.
func RenderNotification(ev types.Event) (title, body string) {
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
		body += fmt.Sprintf(" (%s)", ev.Where.Workspace.Value)
	}
	return title, body
}
