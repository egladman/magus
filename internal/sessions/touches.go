package sessions

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/diff"
)

// A touch is one agent session's contact with one file: that it wrote the file, what it had
// READ beforehand in the same session, and where the host's own transcript of that session
// lives. It is the review's own diff.Touch, produced here rather than through a
// trail-side twin, because the read list is the part no other review tool has: a guard hook
// sees every path an agent reaches, so magus can say what an agent was LOOKING AT
// immediately before it wrote something, which is the closest thing to "why is this change
// shaped like this" that any tool can produce without asking the author.

// replayReadCap and replayRanCap bound what one Touch carries.
//
// Small on purpose. The question this answers is "what was it looking at just before it wrote
// this", and the answer is the last few things; a hundred paths is a session transcript, not
// an explanation, and the transcript pointer is right there for anyone who wants that.
const (
	replayReadCap = 6
	replayRanCap  = 3
)

// DefaultReplayEvents bounds a trail walk behind a review. Each event costs a small blob
// read, and a reader asking "what was this agent looking at" is asking about recent work by
// construction.
const DefaultReplayEvents = 2000

// observation is one agent event in session order, whichever store it came from: the hook
// trail records it live, a loaded transcript records it after the fact. Program is already
// reduced to the program name; Path is workspace-relative.
type observation struct {
	Host, Session, Transcript string
	Tool                      string
	Path, Program             string
	At                        time.Time
}

// AttachTouches folds the agent record onto the review in place: for every changed file,
// which sessions wrote it and what they had read first, from the guard hook's trail at base
// and from the transcripts loaded for root's repository. The trail wins for a session both
// stores hold, since it saw the write happen; a loaded transcript adds the sessions no hook
// was wired for.
//
// A missing blob, a missing store, an absent session store, or a host that supplied no
// session id all just contribute less. A review must still open when nothing was recorded,
// which is the normal case for a workspace whose agents have no guard hook wired, and a
// file nobody touched keeps a nil Touches. A blob that does not decode and a session store
// that refuses its read are returned, joined, after every touch the rest could give is
// attached.
func AttachTouches(ctx context.Context, rev *diff.Diff, root, base string) error {
	paths := make([]string, len(rev.Files))
	for i, f := range rev.Files {
		paths[i] = f.Path
	}
	byPath, errs := replayTrail(ctx, root, base, paths, DefaultReplayEvents)
	if dir, err := Dir(root); err == nil {
		fold, err := session.ReadAll(ctx, dir)
		if err != nil {
			errs = errors.Join(errs, err)
		} else {
			for path, touches := range replayLoaded(fold, paths) {
				for _, t := range touches {
					if !slices.ContainsFunc(byPath[path], func(h diff.Touch) bool { return h.Host == t.Host && h.Session == t.Session }) {
						byPath[path] = append(byPath[path], t)
					}
				}
			}
		}
	}
	for i := range rev.Files {
		if touches := byPath[rev.Files[i].Path]; len(touches) > 0 {
			rev.Files[i].Touches = touches
		}
	}
	return errs
}

// replayTrail reconstructs, for each of paths, which agent sessions the store at base saw
// write it and what they had read first, reading at most limit recent events. A store that
// cannot be read is its error. A request blob that does not decode is skipped and returned
// in the joined error, which is a [*recorder.BlobUndecodableError] or a
// [*recorder.RetiredDecisionError] per blob.
func replayTrail(ctx context.Context, root, base string, paths []string, limit int) (map[string][]diff.Touch, error) {
	events, err := recorder.RecentCalls(ctx, base, limit)
	if err != nil {
		return map[string][]diff.Touch{}, err
	}

	// REVERSED rather than sorted by Ts. The events file is append-only, so its order IS the
	// chronology; Ts is a lossy shadow of it, stamped in whole milliseconds. A burst of hook
	// observations (which is the normal shape, since an agent reads several files and then
	// writes one, all inside a millisecond) lands on identical timestamps, and a stable sort
	// over ties preserves whatever order it was handed. ReadRecent hands back newest-first, so
	// sorting by Ts silently walks the whole thing backwards and every read looks like it
	// happened after the write it explained.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	obs := make([]observation, 0, len(events))
	var errs []error
	for _, e := range events {
		if e.Kind != journal.CallKindAgentCommand || e.Request.Ref == "" {
			continue
		}
		raw, rerr := recorder.ReadCallPayload(base, e.Request)
		if rerr != nil {
			continue
		}
		var req recorder.AgentCommandRequest
		if err := recorder.DecodeRequestBlob(base, e.Request.Ref, raw, &req); err != nil {
			errs = append(errs, err)
			continue
		}
		// A hook records the path exactly as its host supplied it, which is an ABSOLUTE path
		// for every host observed so far, while a review speaks workspace-relative. Without
		// this the two vocabularies never meet and every file reports no history at all,
		// which looks identical to "no hook is wired" and is why it was worth a helper rather
		// than a comparison at each site.
		//
		// The command is reduced to its program at the point of INGEST, not at render: a
		// redaction that happens on the way out leaves the raw text in memory for whatever
		// else reads the touch, and every consumer then has to remember to redact.
		obs = append(obs, observation{
			Host: req.Host, Session: req.Session, Transcript: req.Transcript,
			Tool: req.Tool, Path: recorder.Relativize(root, req.Path), Program: recorder.CommandProgram(req.Command),
			At: e.Time,
		})
	}
	return touchesRead(obs, paths), errors.Join(errs...)
}

// replayLoaded answers replayTrail's question from the loaded transcripts in fold: which
// sessions wrote each of paths and what they had read first. Loads store the
// checkout-relative path and the reduced program, so nothing is relativized here; the events
// are ordered by the host's own clock within each session.
func replayLoaded(fold session.Fold, paths []string) map[string][]diff.Touch {
	var obs []observation
	for hostSession, ev := range session.EachAgentEvent(fold) {
		var tool string
		switch ev.Kind {
		case session.EventFileRead:
			tool = recorder.ToolFileRead
		case session.EventFileWrite:
			tool = recorder.ToolFileWrite
		case session.EventShellCommand:
			tool = recorder.ToolShellCommand
		default:
			continue
		}
		obs = append(obs, observation{
			Host: ev.Host, Session: hostSession, Transcript: ev.Transcript,
			Tool: tool, Path: ev.Text, Program: ev.Program,
			At: time.UnixMilli(ev.AtMs),
		})
	}
	slices.SortStableFunc(obs, func(a, b observation) int {
		if c := strings.Compare(a.Session, b.Session); c != 0 {
			return c
		}
		return a.At.Compare(b.At)
	})
	return touchesRead(obs, paths)
}

// touchesRead is the one reading of "what had it read before it wrote this": one pass over
// obs in session order, accumulating each session's reads and programs so that a write can
// take a snapshot of what came before it. Walking newest-first would mean knowing the writes
// before the reads that explain them, which is the wrong direction for the only question
// being asked. The result is keyed by written path, one entry per session.
func touchesRead(obs []observation, paths []string) map[string][]diff.Touch {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	type sessionState struct {
		read []string
		ran  []string
	}
	states := map[string]*sessionState{}
	out := map[string][]diff.Touch{}

	for _, o := range obs {
		// Session is the grouping key. A host that supplies none still produces attributable
		// events, but they cannot be threaded into a story, so they are skipped rather than
		// all collapsed into one fictional session.
		if o.Session == "" {
			continue
		}
		st := states[o.Session]
		if st == nil {
			st = &sessionState{}
			states[o.Session] = st
		}
		switch o.Tool {
		case recorder.ToolFileRead:
			if o.Path != "" {
				st.read = prependCapped(st.read, o.Path, replayReadCap)
			}
		case recorder.ToolShellCommand:
			if o.Program != "" {
				st.ran = prependCapped(st.ran, o.Program, replayRanCap)
			}
		case recorder.ToolFileWrite:
			if o.Path == "" || !want[o.Path] {
				continue
			}
			// The read list is snapshotted at the moment of the write, so a path the session
			// reached AFTER this edit does not retroactively become its explanation.
			t := diff.Touch{
				Host:       o.Host,
				Session:    o.Session,
				Transcript: o.Transcript,
				Read:       withoutSelf(st.read, o.Path),
				Ran:        append([]string(nil), st.ran...),
			}
			out[o.Path] = upsertLatest(out[o.Path], t)
		}
	}
	return out
}

// Activity is what one checkout's activity log holds for a host session:
// the guard's observations while that session ran. It is scoped to the
// checkout whose cache dir was read, because that is where the hook wrote.
type Activity struct {
	// Commands is how many tool calls the guard observed; Denied how many it refused.
	Commands int `json:"commands"`
	Denied   int `json:"denied"`
	// Leases are the ledger leases the observations were made under, first seen first.
	Leases []string `json:"leases,omitempty"`
	// Spawns are the subagents this session started, oldest first.
	Spawns []activitySpawn `json:"spawns,omitempty"`
	// Calls are the observations in the same order, with the cost facts each recorded. They
	// feed the cost summary and stay out of the encoded form, which would otherwise carry
	// one row per tool call.
	Calls []activityCall `json:"-"`
	// Repeats are facts, not a verdict: policy says which to report.
	Repeats []RepeatRun `json:"repeats,omitempty"`
}

// RepeatRun is Count consecutive calls of one thread with the same call and
// result digests and no write by that thread between. Worker is the host's
// subagent id, empty for the main thread; ThreadFrom says if the host could
// tell. A write by a shell command or another process is invisible to it.
type RepeatRun struct {
	Worker       string             `json:"worker,omitempty"`
	Thread       types.AgentThread  `json:"thread,omitempty"`
	ThreadFrom   types.ThreadSource `json:"thread_from,omitempty"`
	Tool         string             `json:"tool"`
	CallDigest   string             `json:"call_digest"`
	ResultDigest string             `json:"result_digest"`
	Count        int                `json:"count"`
	First        time.Time          `json:"first"`
	Last         time.Time          `json:"last"`
	ToolMs       int64              `json:"tool_ms"`
	SpanMs       int64              `json:"span_ms"`
}

// repeats folds calls into the runs of two or more. A call missing either
// digest joins no run: an absent digest would equal every other.
func repeats(calls []activityCall) []RepeatRun {
	ordered := slices.Clone(calls)
	slices.SortStableFunc(ordered, func(a, b activityCall) int { return a.At.Compare(b.At) })
	open := map[string]map[string]*RepeatRun{}
	var out []RepeatRun
	finish := func(run *RepeatRun) {
		if run.Count >= 2 {
			run.SpanMs = run.Last.Sub(run.First).Milliseconds()
			out = append(out, *run)
		}
	}
	for _, call := range ordered {
		runs := open[call.Worker]
		if call.Action == recorder.ToolFileWrite {
			for _, run := range runs {
				finish(run)
			}
			delete(open, call.Worker)
			continue
		}
		if call.ToolMs == 0 || call.CallDigest == "" || call.ResultDigest == "" {
			continue
		}
		if runs == nil {
			runs = map[string]*RepeatRun{}
			open[call.Worker] = runs
		}
		run := runs[call.CallDigest]
		if run != nil && run.ResultDigest == call.ResultDigest {
			run.Count++
			run.Last = call.At
			run.ToolMs += call.ToolMs
			continue
		}
		if run != nil {
			finish(run)
		}
		runs[call.CallDigest] = startRun(call)
	}
	for _, runs := range open {
		for _, run := range runs {
			finish(run)
		}
	}
	sortRepeatRuns(out)
	return out
}

func sortRepeatRuns(runs []RepeatRun) {
	slices.SortFunc(runs, func(a, b RepeatRun) int {
		return cmp.Or(a.First.Compare(b.First), strings.Compare(a.CallDigest, b.CallDigest))
	})
}

func startRun(call activityCall) *RepeatRun {
	return &RepeatRun{
		Worker: call.Worker, Thread: call.Thread, ThreadFrom: call.ThreadFrom,
		Tool: call.Action, CallDigest: call.CallDigest, ResultDigest: call.ResultDigest,
		Count: 1, First: call.At, Last: call.At, ToolMs: call.ToolMs,
	}
}

// activityCall is one guard observation of a call and the cost facts recorded beside it.
// The facts are the caller's; see AgentCommand. A fact the record did not carry reads as
// its zero value, except Thread, which reads as types.AgentThreadUnknown.
//
// Decision is the hook verdict's: defer, advise, ask or deny.
type activityCall struct {
	At       time.Time
	Decision verdict.Decision
	Rule     string
	// Worker is the host's subagent id, empty for the main thread. ToolMs is
	// the host's tool time, set on tool-time rows only.
	Action string
	Worker string
	ToolMs int64
	types.AgentFacts
}

// activitySpawn is one recorded spawn: the label the host gave the callee,
// the lease its context named, when, and the model the caller claimed.
// DeclaredModel is "" both when none was named and when the payload could
// not be read.
type activitySpawn struct {
	Child         string    `json:"child"`
	Lease         string    `json:"lease,omitempty"`
	At            time.Time `json:"at"`
	DeclaredModel string    `json:"declared_model,omitempty"`
}

// readActivity folds the activity log at base for one host session, reading
// at most limit recent calls. A missing log is an empty result and one that
// cannot be read is the error. A payload that cannot be read is left out,
// except one holding a retired guard decision, which is the error.
func readActivity(ctx context.Context, base, session string, limit int) (Activity, error) {
	var out Activity
	if session == "" {
		return out, nil
	}
	events, err := recorder.RecentCalls(ctx, base, limit)
	if err != nil {
		return out, err
	}
	var retired *recorder.RetiredDecisionError
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Session != session {
			continue
		}
		switch e.Kind {
		case journal.CallKindAgentCommand:
			out.Commands++
			call := activityCall{At: e.Time, Action: e.Name, Worker: e.Agent, ToolMs: e.Duration.Milliseconds(), AgentFacts: types.AgentFacts{Thread: types.AgentThreadUnknown}}
			if raw, err := recorder.ReadCallPayload(base, e.Response); err == nil {
				var resp recorder.AgentCommandResponse
				switch err := recorder.DecodeAgentBlob(base, e.Response.Ref, raw, &resp); {
				case errors.As(err, &retired):
					return Activity{}, err
				case err == nil:
					call.Decision, call.Rule = resp.Decision, resp.Rule
					if resp.Decision == verdict.Deny {
						out.Denied++
					}
				}
			}
			if raw, err := recorder.ReadCallPayload(base, e.Request); err == nil {
				var req recorder.AgentCommandRequest
				switch err := recorder.DecodeAgentBlob(base, e.Request.Ref, raw, &req); {
				case errors.As(err, &retired):
					return Activity{}, err
				case err == nil:
					call.AgentFacts = req.AgentFacts
					call.Thread = cmp.Or(call.Thread, types.AgentThreadUnknown)
				}
			}
			out.Calls = append(out.Calls, call)
		case journal.CallKindAgentSpawn:
			if e.Name == recorder.NameAgentContinue {
				continue
			}
			sp := activitySpawn{Child: e.Name, Lease: e.Lease, At: e.Time}
			if raw, err := recorder.ReadCallPayload(base, e.Request); err == nil {
				var req recorder.AgentSpawnRequest
				if codec.Unmarshal(raw, &req) == nil {
					sp.DeclaredModel = req.DeclaredModel
				}
			}
			out.Spawns = append(out.Spawns, sp)
		default:
			continue
		}
		if e.Lease != "" && !slices.Contains(out.Leases, e.Lease) {
			out.Leases = append(out.Leases, e.Lease)
		}
	}
	out.Repeats = repeats(out.Calls)
	return out, nil
}

// prependCapped puts v at the front and bounds the list, dropping a duplicate so a file read
// five times in a row does not fill the whole window with itself.
func prependCapped(xs []string, v string, cap int) []string {
	out := make([]string, 0, cap)
	out = append(out, v)
	for _, x := range xs {
		if x == v {
			continue
		}
		if len(out) == cap {
			break
		}
		out = append(out, x)
	}
	return out
}

// withoutSelf drops the written file from its own read list. An agent almost always reads a
// file before editing it, and reporting that as context is noise that crowds out the paths
// that actually explain the edit.
func withoutSelf(xs []string, self string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x != self && !strings.EqualFold(x, self) {
			out = append(out, x)
		}
	}
	return out
}

// upsertLatest keeps ONE entry per session, the most recent write. A session that edits a file
// eleven times is one story, not eleven, and listing each pass would bury the sessions that
// touched it once.
func upsertLatest(xs []diff.Touch, t diff.Touch) []diff.Touch {
	for i := range xs {
		if xs[i].Session == t.Session {
			xs[i] = t
			return xs
		}
	}
	return append(xs, t)
}
