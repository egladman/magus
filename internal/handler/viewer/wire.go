// Package viewer holds the magus.viewer.v1alpha1 wire contract: the code that maps captured
// DOMAIN events onto the versioned protobuf tool-page contract and encodes them for a
// browser (a URL-fragment blob for a finished run, or a live SSE stream), plus the
// viewer's filter DSL. It consumes domain types straight from the repositories (e.g. the
// cache output store's []journal.Event) and maps them explicitly to the wire proto: no
// intermediate DTOs, no single-use converters. This file is the viewer encoders; query.go
// is the filter grammar.
package viewer

import (
	"encoding/base64"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1"
)

// flat is a record's fields as the wire and the filter name them, whatever its
// kind.
type flat struct {
	project, target, level, ref, text string
	stream                            journal.OutputStream
	status                            journal.Status
	duration                          time.Duration
}

func flatten(r journal.Record) flat {
	if b, ok := r.Finished(); ok {
		return flat{status: b.Status}
	}
	if b, ok := r.Exec(); ok {
		return flat{project: b.Project, target: b.Target, text: b.Command}
	}
	if b, ok := r.Output(); ok {
		return flat{project: b.Project, target: b.Target, stream: b.Stream, text: b.Text}
	}
	if b, ok := r.Run(); ok {
		return flat{project: b.Project, target: b.Target, level: b.Level, status: b.Status, ref: b.Ref, duration: b.Duration, text: b.Error}
	}
	if b, ok := r.Secret(); ok {
		return flat{project: b.Project, target: b.Target, text: b.Text}
	}
	return flat{}
}

// eventToProto maps a record onto the wire message. inv is dropped: a Journal
// or stream is scoped to one invocation. A started record carries its command
// so a live stream learns the run's identity from its first frame.
func eventToProto(r journal.Record) *viewerv1.Event {
	f := flatten(r)
	out := &viewerv1.Event{
		Time:     tsFromMs(unixMilli(r.Time)),
		Project:  f.project,
		Target:   f.target,
		Kind:     kindToProto(r.Kind()),
		Stream:   streamToProto(f.stream),
		Level:    f.level,
		Status:   statusToProto(f.status),
		Ref:      f.ref,
		Duration: durFromMs(f.duration.Milliseconds()),
		Text:     f.text,
	}
	if started, ok := r.Started(); ok {
		out.Command = commandToProto(started.Command)
		out.MagusVersion = started.Version
	}
	if scope, ok := r.Scope(); ok {
		for _, s := range scope.Undeclared {
			out.Undeclared = append(out.Undeclared, &viewerv1.UndeclaredSeed{
				Project: s.Project,
				Files:   s.Files,
				Inputs:  s.Inputs,
			})
		}
	}
	return out
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// invocationToProto maps the domain invocation metadata (command/lineage/timing) onto
// the wire message.
func invocationToProto(inv journal.Invocation) *viewerv1.Invocation {
	return &viewerv1.Invocation{
		Id:      inv.ID,
		Command: commandToProto(inv.Command),
		// Status was omitted here while ListInvocations set it from the same field, so GetInvocation
		// and the shared Journal envelope reported every run as STATUS_UNSPECIFIED: a reader could
		// not tell a passing run from one whose outcome was never recorded.
		Status:       statusToProto(inv.Status),
		StartTime:    tsFromMs(unixMilli(inv.Started)),
		EndTime:      tsFromMs(unixMilli(inv.Finished)),
		MagusVersion: inv.Version,
	}
}

func commandToProto(c journal.Command) *viewerv1.Command {
	return &viewerv1.Command{
		Arguments: c.Arguments,
		Cwd:       c.Cwd,
		Trigger:   triggerToProto(c.Trigger),
	}
}

// journalToProto builds a Journal from an invocation's header and its records.
func journalToProto(inv journal.Invocation, records []journal.Record) *viewerv1.Journal {
	out := &viewerv1.Journal{Invocation: invocationToProto(inv), Events: make([]*viewerv1.Event, 0, len(records))}
	for _, r := range records {
		out.Events = append(out.Events, eventToProto(r))
	}
	return out
}

// EncodeJournalFragment marshals a journal to protobuf, then gzip+base64url encodes it
// for a `#data=` URL fragment, the static delivery path. The generated JS client
// reverses it (base64url -> gunzip -> Journal.fromBinary). Reuses the same fragment
// encoder graph open uses, so the tool pages share one wire envelope.
func EncodeJournalFragment(inv journal.Invocation, records []journal.Record) (string, error) {
	raw, err := proto.Marshal(journalToProto(inv, records))
	if err != nil {
		return "", err
	}
	return render.EncodeFragmentRaw(raw)
}

// EncodeEvent marshals one event to base64(protobuf) for an SSE `data:` line (SSE payloads
// must be UTF-8 text, so the binary message is base64-wrapped). The JS client base64-
// decodes then Event.fromBinary.
func EncodeEvent(r journal.Record) (string, error) {
	raw, err := proto.Marshal(eventToProto(r))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// kindToProto is the one place that knows both vocabularies: KindRun is
// KIND_RESULT on the wire. KIND_WARN has no producer and no case.
func kindToProto(k journal.Kind) viewerv1.Kind {
	switch k {
	case journal.KindStarted:
		return viewerv1.Kind_KIND_STARTED
	case journal.KindFinished:
		return viewerv1.Kind_KIND_FINISHED
	case journal.KindExec:
		return viewerv1.Kind_KIND_EXEC
	case journal.KindOutput:
		return viewerv1.Kind_KIND_OUTPUT
	case journal.KindRun:
		return viewerv1.Kind_KIND_RESULT
	case journal.KindScope:
		return viewerv1.Kind_KIND_SCOPE
	case journal.KindSecret:
		return viewerv1.Kind_KIND_SECRET
	default:
		return viewerv1.Kind_KIND_UNSPECIFIED
	}
}

func streamToProto(s journal.OutputStream) viewerv1.Stream {
	switch s {
	case journal.Stdout:
		return viewerv1.Stream_STREAM_STDOUT
	case journal.Stderr:
		return viewerv1.Stream_STREAM_STDERR
	default:
		return viewerv1.Stream_STREAM_UNSPECIFIED
	}
}

func statusToProto(s journal.Status) viewerv1.Status {
	switch s {
	case journal.StatusPass:
		return viewerv1.Status_STATUS_PASS
	case journal.StatusFail:
		return viewerv1.Status_STATUS_FAIL
	case journal.StatusCached:
		return viewerv1.Status_STATUS_CACHED
	case journal.StatusPermitted:
		return viewerv1.Status_STATUS_PERMITTED
	default:
		return viewerv1.Status_STATUS_UNSPECIFIED
	}
}

func triggerToProto(t journal.Trigger) viewerv1.Trigger {
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
