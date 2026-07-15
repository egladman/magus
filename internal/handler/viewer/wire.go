// Package viewer holds the magus.viewer.v1 wire contract: the code that maps captured
// DOMAIN events onto the versioned protobuf tool-page contract and encodes them for a
// browser (a URL-fragment blob for a finished run, or a live SSE stream), plus the
// viewer's filter DSL. It consumes domain types straight from the repositories (e.g. the
// cache output store's []journal.Event) and maps them explicitly to the wire proto - no
// intermediate DTOs, no single-use converters. This file is the viewer encoders; query.go
// is the filter grammar.
package viewer

import (
	"encoding/base64"

	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/render"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1"
)

// eventToProto maps a captured journal.Event onto the wire message. The domain's string
// enum values become their proto enum constants; the domain's `inv` is dropped here
// because a Journal/stream is already scoped to one invocation. The started event's
// command + version are carried through so a live stream (which has no Journal header)
// still learns the run's identity from its first frame.
func eventToProto(e journal.Event) *viewerv1.Event {
	out := &viewerv1.Event{
		Time:     tsFromMs(e.Ts),
		Project:  e.Project,
		Target:   e.Target,
		Kind:     kindToProto(e.Kind),
		Stream:   streamToProto(e.Stream),
		Level:    e.Level,
		Status:   statusToProto(e.Status),
		Ref:      e.Ref,
		Duration: durFromMs(e.DurMs),
		Text:     e.Text,
	}
	if e.Command != nil {
		out.Command = commandToProto(*e.Command)
		out.MagusVersion = e.MagusVersion
	}
	return out
}

// invocationToProto maps the domain invocation metadata (command/lineage/timing) onto
// the wire message.
func invocationToProto(inv journal.Invocation) *viewerv1.Invocation {
	return &viewerv1.Invocation{
		Id:           inv.ID,
		Command:      commandToProto(inv.Command),
		StartTime:    tsFromMs(inv.StartedMs),
		EndTime:      tsFromMs(inv.FinishedMs),
		MagusVersion: inv.MagusVersion,
	}
}

func commandToProto(c journal.Command) *viewerv1.Command {
	return &viewerv1.Command{
		Arguments: c.Arguments,
		Cwd:       c.Cwd,
		Trigger:   triggerToProto(c.Trigger),
	}
}

// journalToProto builds a Journal from an invocation's header and its events.
func journalToProto(inv journal.Invocation, events []journal.Event) *viewerv1.Journal {
	out := &viewerv1.Journal{Invocation: invocationToProto(inv), Events: make([]*viewerv1.Event, 0, len(events))}
	for _, e := range events {
		out.Events = append(out.Events, eventToProto(e))
	}
	return out
}

// EncodeJournalFragment marshals a journal to protobuf, then gzip+base64url encodes it
// for a `#data=` URL fragment - the static delivery path. The generated JS client
// reverses it (base64url -> gunzip -> Journal.fromBinary). Reuses the same fragment
// encoder graph open uses, so the tool pages share one wire envelope.
func EncodeJournalFragment(inv journal.Invocation, events []journal.Event) (string, error) {
	raw, err := proto.Marshal(journalToProto(inv, events))
	if err != nil {
		return "", err
	}
	return render.EncodeFragmentRaw(raw)
}

// EncodeEvent marshals one event to base64(protobuf) for an SSE `data:` line (SSE payloads
// must be UTF-8 text, so the binary message is base64-wrapped). The JS client base64-
// decodes then Event.fromBinary.
func EncodeEvent(e journal.Event) (string, error) {
	raw, err := proto.Marshal(eventToProto(e))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func kindToProto(k string) viewerv1.Kind {
	switch k {
	case journal.KindStarted:
		return viewerv1.Kind_KIND_STARTED
	case journal.KindFinished:
		return viewerv1.Kind_KIND_FINISHED
	case journal.KindExec:
		return viewerv1.Kind_KIND_EXEC
	case journal.KindOutput:
		return viewerv1.Kind_KIND_OUTPUT
	case journal.KindResult:
		return viewerv1.Kind_KIND_RESULT
	case journal.KindScope:
		return viewerv1.Kind_KIND_SCOPE
	case journal.KindWarn:
		return viewerv1.Kind_KIND_WARN
	default:
		return viewerv1.Kind_KIND_UNSPECIFIED
	}
}

func streamToProto(s string) viewerv1.Stream {
	switch s {
	case journal.StreamStdout:
		return viewerv1.Stream_STREAM_STDOUT
	case journal.StreamStderr:
		return viewerv1.Stream_STREAM_STDERR
	default:
		return viewerv1.Stream_STREAM_UNSPECIFIED
	}
}

func statusToProto(s string) viewerv1.Status {
	switch s {
	case journal.StatusPass:
		return viewerv1.Status_STATUS_PASS
	case journal.StatusFail:
		return viewerv1.Status_STATUS_FAIL
	case journal.StatusCached:
		return viewerv1.Status_STATUS_CACHED
	default:
		return viewerv1.Status_STATUS_UNSPECIFIED
	}
}

func triggerToProto(t string) viewerv1.Trigger {
	switch t {
	case journal.TriggerRun:
		return viewerv1.Trigger_TRIGGER_RUN
	case journal.TriggerAffected:
		return viewerv1.Trigger_TRIGGER_AFFECTED
	case journal.TriggerCI:
		return viewerv1.Trigger_TRIGGER_CI
	case journal.TriggerX:
		return viewerv1.Trigger_TRIGGER_X
	case journal.TriggerWatch:
		return viewerv1.Trigger_TRIGGER_WATCH
	case journal.TriggerDirect:
		return viewerv1.Trigger_TRIGGER_DIRECT
	default:
		return viewerv1.Trigger_TRIGGER_UNSPECIFIED
	}
}
