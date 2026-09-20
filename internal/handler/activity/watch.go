package activity

import (
	"context"
	"slices"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	jobstore "github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	activityv1 "github.com/egladman/magus/proto/gen/go/magus/activity/v1alpha1"
	"github.com/egladman/magus/types"
)

// defaultPoll is how often the follow loop re-reads the trail and the job rows. Both are
// small local files, and a person watching a worker is reading at human speed: a tighter
// cadence would buy latency nobody can see and pay for it on every open drawer.
const defaultPoll = time.Second

// Option configures a Service. Variadic so the daemon can hand over the sources it happens
// to have: a bridge with no job store still serves the trail half, which is what the view
// did before any of this existed.
type Option func(*Service)

// WithJobs gives the view the repository's job plan, read at call time rather than
// captured: rows change state, release paths and appear while the daemon runs, and a slice
// taken at mount time would attribute against a plan that stopped being true.
func WithJobs(rows func() []types.Job) Option {
	return func(s *Service) { s.jobs = rows }
}

// WithFileChanges gives the view a subscription to attributed file changes, one channel per
// watcher. The channel yields events the producer has ALREADY attributed (see
// [jobstore.FileEvents]), because attribution needs the job plan and the watcher is the
// thing holding the paths; doing it here would mean this package owning a second answer to
// which job a path belongs to.
//
// A subscription rather than one shared channel: two readers pulling from one channel each
// get half the events, which is a bug that looks exactly like a quiet worker.
func WithFileChanges(sub func(context.Context) <-chan jobstore.FeedEvent) Option {
	return func(s *Service) { s.files = sub }
}

// WatchActivityEvents follows the merged feed until the caller hangs up.
//
// Backfill first, then follow. A drawer that opened onto a blank panel would read as
// "nothing has happened here", and a job that has been running for an hour has a past; the
// question a watcher is asking is "how is it going", which is about both.
//
// THE THREE PRODUCERS ARE MERGED HERE rather than left to the client, because only the
// server can time-order them: the trail, the watcher and the job rows are three files and
// three clocks, and a client stitching them would show a deny after the run it blocked.
func (s *Service) WatchActivityEvents(ctx context.Context, req *connect.Request[activityv1.WatchActivityEventsRequest], stream *connect.ServerStream[activityv1.ActivityEvent]) error {
	filter := req.Msg.GetFilter()
	backfill := min(int(req.Msg.GetBackfill()), maxPageSize)

	// The cursors live in internal/job so `magus job watch` follows the feed exactly the way
	// this stream does. Two answers to "what has happened since I last looked" is how a
	// terminal and a drawer come to disagree about whether a worker is still going.
	past := jobstore.Ascending(readMerged(s.loaded(), maxWindow))
	cursor := jobstore.CursorAt(past)
	runs := jobstore.NewRunCursor()

	if backfill > 0 {
		history := s.history(past, filter)
		if len(history) > backfill {
			history = history[len(history)-backfill:]
		}
		for _, e := range history {
			if err := stream.Send(e); err != nil {
				return err
			}
		}
	}
	// The run cursor is primed whether or not anything was backfilled: a job's recorded runs
	// are a snapshot rather than an append-only log, so an unprimed cursor would re-send
	// every run the store already held on the first tick.
	runs.Prime(s.rows())

	files := s.subscribe(ctx)
	tick := time.NewTicker(s.pollInterval())
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-files:
			if !ok {
				files = nil // a closed watcher stops the file half; the trail half keeps serving
				continue
			}
			wire := wireFeedEvent(e)
			if !matchWire(wire, filter) {
				continue
			}
			if err := stream.Send(wire); err != nil {
				return err
			}
		case <-tick.C:
			fresh := cursor.Next(jobstore.Ascending(readMerged(s.loaded(), maxWindow)))
			for _, e := range fresh {
				if !matchFilter(e, filter) {
					continue
				}
				if err := stream.Send(wireEvent(e)); err != nil {
					return err
				}
			}
			for _, e := range runs.Next(s.rows()) {
				wire := wireFeedEvent(e)
				if !matchWire(wire, filter) {
					continue
				}
				if err := stream.Send(wire); err != nil {
					return err
				}
			}
		}
	}
}

// history is what the feed already holds, oldest last: the guard's observations and the
// runs recorded against every job, time-ordered together. File changes contribute nothing
// here on purpose, because nothing records them: the watcher reports a change as it
// happens and keeps no log, so a backfilled file event would have to be invented.
func (s *Service) history(past []trail.Event, filter *activityv1.ActivityQuery) []*activityv1.ActivityEvent {
	out := make([]*activityv1.ActivityEvent, 0, len(past))
	for _, e := range past {
		if matchFilter(e, filter) {
			out = append(out, wireEvent(e))
		}
	}
	for _, row := range s.rows() {
		for _, e := range jobstore.RunEvents(row) {
			wire := wireFeedEvent(e)
			if matchWire(wire, filter) {
				out = append(out, wire)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b *activityv1.ActivityEvent) int {
		return a.GetTime().AsTime().Compare(b.GetTime().AsTime())
	})
	return out
}

// rows is the job plan this call reads, empty when no source was supplied.
func (s *Service) rows() []types.Job {
	if s.jobs == nil {
		return nil
	}
	return s.jobs()
}

// subscribe opens the file-change channel for this stream, nil when nothing watches. A nil
// channel blocks forever in a select, which is exactly right: the other arms keep serving.
func (s *Service) subscribe(ctx context.Context) <-chan jobstore.FeedEvent {
	if s.files == nil {
		return nil
	}
	return s.files(ctx)
}

func (s *Service) pollInterval() time.Duration {
	if s.poll > 0 {
		return s.poll
	}
	return defaultPoll
}

// wireFeedEvent maps a feed event the trail never recorded (a file change, a run) onto the
// one activity envelope. The kinds are distinct so a reader switches rather than guessing
// from a field's shape, and unit carries the job for all of them, which is what lets one
// filter narrow the whole merged stream.
func wireFeedEvent(e jobstore.FeedEvent) *activityv1.ActivityEvent {
	out := &activityv1.ActivityEvent{
		Time:    timestamppb.New(time.UnixMilli(e.Ts)),
		Actor:   e.Actor,
		Action:  e.Action,
		Host:    e.Host,
		Session: e.Session,
		Unit:    e.Job,
		Outcome: encodeOutcome(e.Outcome),
		Error:   e.Error,
		Preview: e.Note,
	}
	switch e.Kind {
	case jobstore.FeedFile:
		out.Kind = activityv1.Kind_KIND_FILE_CHANGE
		out.Contested = e.Contested
		if out.Preview == "" {
			out.Preview = e.WritePath
		}
	case jobstore.FeedRun:
		out.Kind = activityv1.Kind_KIND_RUN
		out.ResponseRef = e.Ref
	default:
		out.Kind = activityv1.Kind_KIND_AGENT_COMMAND
	}
	return out
}

// matchWire asks the filter about a wire event, including the one thing a stored event
// cannot express: a CONTESTED file change is attributed to nobody and is still the business
// of every lease that claimed it.
//
// A claimant is stood in for the unit clause so the rest of the filter is asked exactly
// once. The probe is a predicate input, never the event: nothing that leaves here says the
// contested write belonged to the lease that matched it.
func matchWire(e *activityv1.ActivityEvent, q *activityv1.ActivityQuery) bool {
	probe := fromWire(e)
	if units := q.GetUnits(); len(units) > 0 && probe.Lease == "" {
		for _, u := range units {
			if slices.Contains(e.GetContested(), u) {
				probe.Lease = u
				break
			}
		}
	}
	return matchFilter(probe, q)
}

// fromWire is the narrow slice of a wire event matchFilter reads, so one filter serves both
// the recorded events and the synthesized ones. Only the fields the filter looks at are
// carried: this exists to answer a predicate, not to round-trip an event.
func fromWire(e *activityv1.ActivityEvent) trail.Event {
	return trail.Event{
		Ts:      e.GetTime().AsTime().UnixMilli(),
		Actor:   e.GetActor(),
		Action:  e.GetAction(),
		Session: e.GetSession(),
		Lease:   e.GetUnit(),
		Kind:    decodeKind(e.GetKind()),
	}
}

// decodeKind maps back the two kinds this package synthesizes. The rest never round-trip:
// a recorded event is filtered as the trail.Event it already is.
func decodeKind(k activityv1.Kind) trail.Kind {
	switch k {
	case activityv1.Kind_KIND_FILE_CHANGE:
		return trail.Kind(kindFileChange)
	case activityv1.Kind_KIND_RUN:
		return trail.Kind(kindRun)
	default:
		return ""
	}
}

// The two synthesized kinds' store spellings. They are NOT trail.Kind constants: nothing
// writes either of them to a trail, and adding them there would claim the store records
// something it does not.
const (
	kindFileChange = "file_change"
	kindRun        = "run"
)
