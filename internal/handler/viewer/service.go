package viewer

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1/viewerv1alpha1connect"
)

// Service implements viewerv1alpha1connect.ViewerServiceHandler over the same two stores the
// plain-JSON run-browser routes read.
type Service struct {
	outputs outputSource
	runs    runSource
}

// NewService builds the ViewerService Connect handler reading from the run and output stores.
func NewService(outputs outputSource, runs runSource) *Service {
	return &Service{outputs: outputs, runs: runs}
}

var _ viewerv1alpha1connect.ViewerServiceHandler = (*Service)(nil)

// resolveInvocation turns a GetInvocationRequest-style name into an invocation id. A name is
// either an invocation id already, or an output ref that names the run which produced it.
func (s *Service) resolveInvocation(name string) (string, error) {
	if strings.HasPrefix(name, "inv") {
		return name, nil
	}
	d, err := s.runs.DescriptorByRef(name)
	if err != nil {
		return "", connect.NewError(connect.CodeNotFound, err)
	}
	if d.Inv == "" {
		// A run stored before journalling: the output survives, the invocation around it does
		// not. Saying so beats returning an empty journal that reads as "this run did nothing".
		return "", connect.NewError(connect.CodeNotFound, errors.New("viewer: "+name+" predates run journalling"))
	}
	return d.Inv, nil
}

// GetInvocation returns one run's header.
func (s *Service) GetInvocation(_ context.Context, req *connect.Request[viewerv1.GetInvocationRequest]) (*connect.Response[viewerv1.Invocation], error) {
	inv, err := s.resolveInvocation(req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	header, _, err := s.runs.InvocationEventsByID(inv)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(invocationToProto(header)), nil
}

// GetJournal returns one run whole - header plus every event.
func (s *Service) GetJournal(_ context.Context, req *connect.Request[viewerv1.GetJournalRequest]) (*connect.Response[viewerv1.Journal], error) {
	inv, err := s.resolveInvocation(req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	header, events, err := s.runs.InvocationEventsByID(inv)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(journalToProto(header, events)), nil
}

// ListEvents returns a page of one run's events. The store returns the journal whole, so the page
// token is an offset into it rather than a cursor it could resume from.
func (s *Service) ListEvents(_ context.Context, req *connect.Request[viewerv1.ListEventsRequest]) (*connect.Response[viewerv1.ListEventsResponse], error) {
	inv, err := s.resolveInvocation(req.Msg.GetParent())
	if err != nil {
		return nil, err
	}
	header, events, err := s.runs.InvocationEventsByID(inv)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	_ = header

	from, to, next, err := page(len(events), req.Msg.GetPageToken(), int(req.Msg.GetPageSize()))
	if err != nil {
		return nil, err
	}

	out := &viewerv1.ListEventsResponse{NextPageToken: next}
	for _, e := range events[from:to] {
		out.Events = append(out.Events, eventToProto(e))
	}
	return connect.NewResponse(out), nil
}

// StreamEvents is not served here: live events ride the SSE route in live.go, which multiplexes
// them onto the connection the console already holds open.
func (s *Service) StreamEvents(_ context.Context, _ *connect.Request[viewerv1.StreamEventsRequest], _ *connect.ServerStream[viewerv1.StreamEventsResponse]) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("viewer: live events stream over the SSE route, not this RPC"))
}

// ListOutputs returns the stored runs' descriptors, newest first.
func (s *Service) ListOutputs(_ context.Context, req *connect.Request[viewerv1.ListOutputsRequest]) (*connect.Response[viewerv1.ListOutputsResponse], error) {
	all := s.outputs.ListDescriptors()

	from, to, next, err := page(len(all), req.Msg.GetPageToken(), int(req.Msg.GetPageSize()))
	if err != nil {
		return nil, err
	}

	out := &viewerv1.ListOutputsResponse{NextPageToken: next}
	for _, d := range all[from:to] {
		out.Outputs = append(out.Outputs, &viewerv1.Output{
			Ref:        d.Ref,
			Project:    d.Project,
			Target:     d.Target,
			Invocation: d.Inv,
			Failed:     d.Failed,
			Error:      d.ErrMsg,
			CreateTime: tsFromMs(d.TimestampMs),
			Duration:   durFromMs(d.DurationMs),
		})
	}
	return connect.NewResponse(out), nil
}

// GetOutput returns one stored run's captured bytes verbatim.
func (s *Service) GetOutput(_ context.Context, req *connect.Request[viewerv1.GetOutputRequest]) (*connect.Response[viewerv1.GetOutputResponse], error) {
	body, _, err := s.outputs.ByRef(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&viewerv1.GetOutputResponse{Body: body}), nil
}

// ListInvocations returns the retained run journals, newest first.
func (s *Service) ListInvocations(_ context.Context, req *connect.Request[viewerv1.ListInvocationsRequest]) (*connect.Response[viewerv1.ListInvocationsResponse], error) {
	// 0 is every journal kept. NOT cache.DefaultMaxRuns: that is the RETENTION bound, and
	// borrowing it would change what this returns the day somebody tunes how much history to keep.
	logs := s.runs.ListRunLogs(0)
	from, to, next, err := page(len(logs), req.Msg.GetPageToken(), int(req.Msg.GetPageSize()))
	if err != nil {
		return nil, err
	}

	out := &viewerv1.ListInvocationsResponse{NextPageToken: next}
	for _, l := range logs[from:to] {
		// A RunLog carries no cwd - the header is read without opening the journal - so Command
		// holds the argv and the trigger only.
		out.Invocations = append(out.Invocations, &viewerv1.Invocation{
			Id: l.Inv,
			Command: &viewerv1.Command{
				Arguments: l.Arguments,
				Trigger:   triggerToProto(l.Trigger),
			},
			StartTime:    tsFromMs(l.StartedMs),
			EndTime:      tsFromMs(l.FinishedMs),
			MagusVersion: l.MagusVersion,
			Status:       statusToProto(l.Status),
			SizeBytes:    l.SizeBytes,
		})
	}
	return connect.NewResponse(out), nil
}
