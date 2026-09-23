package job

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// Attribution is which job a changed path belongs to, and the declaration that decided it.
//
// Ambiguous is the failure mode spelled out rather than hidden in a bool: when two live
// boundaries cover one path there is no tie to break, and the reader needs the two names
// to go and fix the plan. See [AttributeWrite].
type Attribution struct {
	// Job is the one live job whose write paths cover the path, empty when none does or
	// more than one does.
	Job string
	// WritePath is the declared write path that covered it, so a reader sees WHY the path
	// was attributed and not merely that it was.
	WritePath string
	// Ambiguous names every live job that claimed the path, set only when more than one
	// did, sorted so the pair reads the same way twice.
	Ambiguous []string
}

// AttributeWrite names the one LIVE job whose declared write paths cover rel, a
// repository-relative path. It is what lets a person watch a worker without the worker
// cooperating: nothing in the write itself says who made it, and the declaration does.
//
// THE ATTRIBUTION IS EXACT BECAUSE THE WRITE PATHS ARE DISJOINT, and that is a property the
// fork already proves rather than one assumed here: [RefuseSharedCheckout] refuses a fork
// whose write paths would collide, [Store.WriteProof] records what it could prove, and `magus
// ls jobs` prints every pair that claims common ground. So the usual answer is one job or
// none.
//
// When two live jobs' write paths DO cover one path the answer is NEITHER, with both names
// in Ambiguous. Picking one would tell a person a file moved under a worker that never
// touched it, and there is nothing in a path to break the tie with; the plan is already
// damaged at that point, and saying so is more use than a coin toss. A job's own deny
// paths cut its boundary first, so a shared build input inside a directory somebody leased
// is nobody's.
//
// A job that has ended holds nothing. Rows are kept after they finish, and a finished
// boundary that kept answering would collect the next holder's writes for as long as the
// row lived.
func AttributeWrite(rows []types.Job, rel string) (Attribution, bool) {
	var out Attribution
	var claimants []string
	for _, row := range rows {
		if !row.State.Live() {
			continue
		}
		declared, covered := matching(row.WritePaths, rel)
		if !covered {
			continue
		}
		if _, denied := matching(row.DenyPaths, rel); denied {
			continue
		}
		claimants = append(claimants, row.ID)
		out.Job, out.WritePath = row.ID, declared
	}
	if len(claimants) > 1 {
		slices.Sort(claimants)
		return Attribution{Ambiguous: claimants}, false
	}
	return out, out.Job != ""
}

// Covers reports whether a declared path covers a repository-relative one, on the rule the
// write paths are graded by: a glob matches as a glob, and a literal directory covers what
// is under it.
//
// Exported so a reader FILTERING by path asks the same question the guard asks when it
// grades a write. Two answers to "is this file inside that path" is how a console filter
// comes to hide the write a deny was about.
func Covers(declared, p string) bool { return covers(declared, p) }

// Feed event kinds. They are the three things a watcher can see a worker do, and each one
// comes from a different producer: the filesystem, the guard, and the run journal.
const (
	FeedFile = "file" // a path under the job's write paths changed
	FeedTool = "tool" // the guard observed a tool call under the job's lease
	FeedRun  = "run"  // a run magus recorded against the job
)

// FeedEvent is one line of a job's feed: something that happened, when, and how it was
// attributed. It is deliberately flat and small. This rides a stream that a terminal
// prints one line per event from and a drawer appends a row per event to, so a shape
// either of them has to unpack first would be paid for on every event.
type FeedEvent struct {
	Ts   int64  // unix milliseconds
	Kind string // one of FeedFile, FeedTool, FeedRun
	Job  string // the job this was attributed to, empty when nothing could attribute it
	// WritePath is the declaration that attributed a file event. Empty on the other kinds,
	// which are attributed by lease rather than by path.
	WritePath string
	// Action is what happened, in the kind's own vocabulary: the path for a file event,
	// the tool label for a tool call, the rendered command for a run.
	Action string
	// Actor is the recorded origin rendered as one label (types.Origin.Label).
	Actor   string
	Host    string
	Session string
	// Decision is the guard's verdict on a tool call: pass, advise, ask or deny. Empty on
	// an observation the guard did not judge, which is what --observe records, and on the
	// other two kinds.
	Decision string
	// Outcome is trail.OutcomeOK or trail.OutcomeError, and Error carries the text.
	Outcome string
	Error   string
	// Ref is the output ref behind a run, so a reader can open the log the feed line names
	// rather than going to find it.
	Ref string
	// Note is the one thing about this line the fields above cannot say: the gate a run
	// belongs to, or that a path is contested.
	Note string
	// Contested names every live job that claimed this path, set only when more than one
	// did, and Job is then empty. It is a LIST rather than a sentence because a reader
	// watching one job has to ask "am I one of these", and parsing prose to answer that is
	// how the one event a contested plan most needs to surface gets dropped.
	//
	// Not hypothetical: measured in this repository, two live jobs both declared
	// internal/trail, and a watcher filtering on Job alone saw nothing at all while that
	// directory was being edited.
	Contested []string
}

// FileEvents attributes changed paths to live jobs, newest-first order preserved from the
// caller's batch. An unattributable path still produces an event, with an empty Job: a
// write outside every declared boundary is the one a reader most wants to see, and dropping
// it is how a view comes to show a quiet tree while somebody is editing it.
func FileEvents(rows []types.Job, ts int64, paths []string) []FeedEvent {
	out := make([]FeedEvent, 0, len(paths))
	for _, p := range paths {
		at, _ := AttributeWrite(rows, p)
		e := FeedEvent{Ts: ts, Kind: FeedFile, Job: at.Job, WritePath: at.WritePath, Action: p, Outcome: trail.OutcomeOK}
		if len(at.Ambiguous) > 0 {
			e.Contested = at.Ambiguous
			e.Note = "contested: " + strings.Join(at.Ambiguous, " and ") + " both declare this path"
		}
		out = append(out, e)
	}
	return out
}

// ToolEvents projects the guard's trail onto the feed, attributed BY LEASE.
//
// By lease and not by session, though the event carries both. One session declares many
// jobs and an orchestrator's session declares every job in the plan, so a session id names
// no single job and joining on it would attribute the orchestrator's own commands to
// whichever row it happened to have written last. A missing join is a gap a reader can
// see; a wrong one is a gap they cannot. Session stays on the event as something to FILTER
// by, which is a question the caller asked rather than a claim magus made.
func ToolEvents(events []trail.Event) []FeedEvent {
	out := make([]FeedEvent, 0, len(events))
	for _, e := range events {
		if e.Kind != trail.KindAgentCommand && e.Kind != trail.KindSandboxDenial {
			continue
		}
		out = append(out, FeedEvent{
			Ts:       e.Ts,
			Kind:     FeedTool,
			Job:      e.Lease,
			Action:   e.Action,
			Actor:    e.Label(),
			Host:     e.Host,
			Session:  e.Session,
			Decision: decisionOf(e),
			Outcome:  e.Outcome,
			Error:    e.Error,
		})
	}
	return out
}

// decisionOf reads the guard's verdict off the event without opening its payload blob. The
// preview is written as "guard: <decision>" by the producer, and "observed" when the guard
// judged nothing; a feed line that had to fetch a blob to say "deny" would cost one round
// trip per row.
func decisionOf(e trail.Event) string {
	const prefix = "guard: "
	if rest, ok := strings.CutPrefix(e.Preview, prefix); ok {
		return rest
	}
	return ""
}

// RunEvents is every run magus recorded against one job: the primary check, each
// completion gate, and the daemon's own last run for a catalog job. They are facts the
// store already holds, so the feed serves them without asking the output store anything.
func RunEvents(row types.Job) []FeedEvent {
	out := make([]FeedEvent, 0, 2+len(row.GateAttempts))
	if row.Attempt != nil && row.Attempt.Found {
		out = append(out, runEvent(row.ID, *row.Attempt, "check"))
	}
	for _, gate := range row.GateAttempts {
		if gate.Attempt.Found {
			out = append(out, runEvent(row.ID, gate.Attempt, "gate "+gate.GateID))
		}
	}
	if run := row.LastRun; run != nil && run.Ended > 0 {
		e := FeedEvent{Ts: run.Ended, Kind: FeedRun, Job: row.ID, Action: run.Invocation, Ref: run.Invocation, Outcome: trail.OutcomeOK, Note: "last run"}
		if !run.OK {
			e.Outcome, e.Error = trail.OutcomeError, run.Error
		}
		out = append(out, e)
	}
	return out
}

func runEvent(id string, a types.JobAttempt, note string) FeedEvent {
	e := FeedEvent{Ts: a.TimestampMs, Kind: FeedRun, Job: id, Action: a.String(), Ref: a.Ref, Outcome: trail.OutcomeOK, Note: note}
	if a.Failed {
		e.Outcome = trail.OutcomeError
	}
	return e
}

// FeedFilter narrows a feed to what one reader asked for. The three fields AND together
// and an empty one does not constrain, matching the ActivityQuery grammar the console
// already speaks.
//
// Paths match the same way a declared write path does, so asking for a directory answers
// for what is under it.
type FeedFilter struct {
	Jobs     []string
	Sessions []string
	Paths    []string
}

// Match reports whether e is what the filter asked for.
//
// A job filter matches a CONTESTED event that names it, even though the event is attributed
// to nobody. The two facts are different questions: "whose write is this" has no answer, and
// "is this something the person watching that job needs to see" plainly does.
func (f FeedFilter) Match(e FeedEvent) bool {
	if len(f.Jobs) > 0 && !slices.Contains(f.Jobs, e.Job) && !slices.ContainsFunc(f.Jobs, func(id string) bool {
		return slices.Contains(e.Contested, id)
	}) {
		return false
	}
	if len(f.Sessions) > 0 && !slices.Contains(f.Sessions, e.Session) {
		return false
	}
	if len(f.Paths) > 0 {
		if e.Kind != FeedFile {
			return false
		}
		if _, ok := matching(f.Paths, e.Action); !ok {
			return false
		}
	}
	return true
}

// Empty reports whether the filter constrains nothing, which is what lets a caller skip
// the walk entirely.
func (f FeedFilter) Empty() bool {
	return len(f.Jobs) == 0 && len(f.Sessions) == 0 && len(f.Paths) == 0
}

// SortFeed orders events newest first, stably, so events sharing a millisecond keep the
// order their producer emitted them in rather than shuffling between calls.
func SortFeed(events []FeedEvent) {
	slices.SortStableFunc(events, func(a, b FeedEvent) int { return cmp.Compare(b.Ts, a.Ts) })
}

// Ascending reverses a newest-first trail read. A follower reads forward, and reversing at
// the seam keeps the one sort in the reader rather than growing a second policy about what
// "in order" means.
func Ascending(events []trail.Event) []trail.Event {
	out := slices.Clone(events)
	slices.Reverse(out)
	return out
}

// TrailCursor is how far forward a follower has read the trail.
//
// It carries a COUNT alongside the timestamp because the trail's resolution is a
// millisecond and two observations routinely share one: a cursor holding only a timestamp
// either replays that millisecond's events on every tick or drops the second of them, and
// both read to a person as a bug in the guard.
//
// Shared by the streaming RPC and `magus job watch` so the two cannot disagree about what
// "since I last looked" means.
type TrailCursor struct {
	ts   int64
	seen int
}

// CursorAt places a cursor at the end of asc, so a follower that skipped the backfill does
// not receive the whole retained window on its first tick.
func CursorAt(asc []trail.Event) *TrailCursor {
	c := &TrailCursor{}
	if len(asc) == 0 {
		return c
	}
	c.ts = asc[len(asc)-1].Ts
	c.seen = countAt(asc, c.ts)
	return c
}

// Next returns the events in asc this cursor has not yielded, and advances past them.
func (c *TrailCursor) Next(asc []trail.Event) []trail.Event {
	var out []trail.Event
	at := 0
	for _, e := range asc {
		if e.Ts < c.ts {
			continue
		}
		if e.Ts == c.ts {
			at++
			if at <= c.seen {
				continue
			}
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil
	}
	c.ts = out[len(out)-1].Ts
	c.seen = countAt(asc, c.ts)
	return out
}

func countAt(asc []trail.Event, ts int64) int {
	n := 0
	for _, e := range asc {
		if e.Ts == ts {
			n++
		}
	}
	return n
}

// RunCursor remembers which of a job's recorded runs have been reported.
//
// A run is identified by its job, its output ref and the note naming which check it was,
// because a row's runs are REWRITTEN IN PLACE rather than appended: a re-run keeps its gate
// id and takes a new ref, so a cursor keyed on time alone would either re-report the whole
// set every tick or miss the re-run entirely.
type RunCursor struct{ sent map[string]bool }

func NewRunCursor() *RunCursor { return &RunCursor{sent: map[string]bool{}} }

// Prime marks everything the store already holds as reported, without yielding it.
func (c *RunCursor) Prime(rows []types.Job) {
	for _, row := range rows {
		for _, e := range RunEvents(row) {
			c.sent[runKey(e)] = true
		}
	}
}

// Next returns the runs recorded since the last call.
func (c *RunCursor) Next(rows []types.Job) []FeedEvent {
	var out []FeedEvent
	for _, row := range rows {
		for _, e := range RunEvents(row) {
			key := runKey(e)
			if c.sent[key] {
				continue
			}
			c.sent[key] = true
			out = append(out, e)
		}
	}
	return out
}

func runKey(e FeedEvent) string { return e.Job + "\x00" + e.Ref + "\x00" + e.Note }
