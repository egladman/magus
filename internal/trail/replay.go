package trail

import (
	"slices"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// A touch is one agent session's contact with one file: that it wrote the file, what it had
// READ beforehand in the same session, and where the host's own transcript of that session
// lives. It is the review's own types.DiffTouch, produced here rather than through a
// trail-side twin, because the read list is the part no other review tool has: a guard hook
// sees every path an agent reaches, so magus can say what an agent was LOOKING AT
// immediately before it wrote something, which is the closest thing to "why is this change
// shaped like this" that any tool can produce without asking the author.

// The tool labels magus itself writes; see cmd/magus/agent.go. Matched rather than a host's
// own tool names, which magus deliberately never learns.
const (
	toolWrite = "file.write"
	toolRead  = "file.read"
	toolShell = "shell.command"
)

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
// Best-effort throughout: an unreadable blob, a missing trail, an absent session store, or
// a host that supplied no session id all just contribute less. A review must still open when
// nothing was recorded, which is the normal case for a workspace whose agents have no guard
// hook wired, and a file nobody touched keeps a nil Touches.
func AttachTouches(rev *types.Diff, root, base string) {
	paths := make([]string, len(rev.Files))
	for i, f := range rev.Files {
		paths[i] = f.Path
	}
	byPath := replayTrail(root, base, paths, DefaultReplayEvents)
	if dir, err := sessions.Dir(root); err == nil {
		if fold, err := sessions.ReadAll(dir); err == nil {
			for path, touches := range replayLoaded(fold, paths) {
				for _, t := range touches {
					if !slices.ContainsFunc(byPath[path], func(h types.DiffTouch) bool { return h.Host == t.Host && h.Session == t.Session }) {
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
}

// replayTrail reconstructs, for each of paths, which agent sessions the trail at base saw
// write it and what they had read first, reading at most limit recent events.
func replayTrail(root, base string, paths []string, limit int) map[string][]types.DiffTouch {
	events, err := ReadRecent(base, limit)
	if err != nil || len(events) == 0 {
		return map[string][]types.DiffTouch{}
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
	for _, e := range events {
		if e.Kind != KindAgentCommand || e.RequestRef == "" {
			continue
		}
		raw, rerr := ReadBlob(base, e.RequestRef)
		if rerr != nil {
			continue
		}
		var req agentCommandRequest
		if json.Unmarshal(raw, &req) != nil {
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
			Tool: req.Tool, Path: relativize(root, req.Path), Program: commandProgram(req.Command),
			At: time.UnixMilli(e.Ts),
		})
	}
	return touchesFrom(obs, paths)
}

// replayLoaded answers replayTrail's question from the loaded transcripts in fold: which
// sessions wrote each of paths and what they had read first. Loads store the
// checkout-relative path and the reduced program, so nothing is relativized here; the events
// are ordered by the host's own clock within each session.
func replayLoaded(fold sessions.Fold, paths []string) map[string][]types.DiffTouch {
	var obs []observation
	for session, ev := range sessions.EachAgentEvent(fold) {
		var tool string
		switch ev.Kind {
		case sessions.EventFileRead:
			tool = toolRead
		case sessions.EventFileWrite:
			tool = toolWrite
		case sessions.EventShellCommand:
			tool = toolShell
		default:
			continue
		}
		obs = append(obs, observation{
			Host: ev.Host, Session: session, Transcript: ev.Transcript,
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
	return touchesFrom(obs, paths)
}

// touchesFrom is the one reading of "what had it read before it wrote this": one pass over
// obs in session order, accumulating each session's reads and programs so that a write can
// take a snapshot of what came before it. Walking newest-first would mean knowing the writes
// before the reads that explain them, which is the wrong direction for the only question
// being asked. The result is keyed by written path, one entry per session.
func touchesFrom(obs []observation, paths []string) map[string][]types.DiffTouch {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	type sessionState struct {
		read []string
		ran  []string
	}
	states := map[string]*sessionState{}
	out := map[string][]types.DiffTouch{}

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
		case toolRead:
			if o.Path != "" {
				st.read = prependCapped(st.read, o.Path, replayReadCap)
			}
		case toolShell:
			if o.Program != "" {
				st.ran = prependCapped(st.ran, o.Program, replayRanCap)
			}
		case toolWrite:
			if o.Path == "" || !want[o.Path] {
				continue
			}
			// The read list is snapshotted at the moment of the write, so a path the session
			// reached AFTER this edit does not retroactively become its explanation.
			t := types.DiffTouch{
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

// SessionTrail is what one checkout's trail holds for a host session: the join between a
// transcript a host wrote and the observations the guard made while that session ran. It is
// scoped to the checkout whose cache dir was read, because that is where the hook wrote.
type SessionTrail struct {
	// Commands is how many tool calls the guard observed; Denied how many it refused.
	Commands int `json:"commands"`
	Denied   int `json:"denied"`
	// Leases are the ledger leases the observations were made under, first seen first.
	Leases []string `json:"leases,omitempty"`
	// Spawns are the handoffs this session made to sub-agents, oldest first.
	Spawns []SessionSpawn `json:"spawns,omitempty"`
}

// SessionSpawn is one recorded handoff: the label the host gave the callee, the lease the
// handed context named, and when.
type SessionSpawn struct {
	Child string    `json:"child"`
	Lease string    `json:"lease,omitempty"`
	At    time.Time `json:"at"`
}

// ForSession folds the trail at base for one host session, reading at most limit recent
// events. A trail that cannot be read is an empty result, never an error: the join is context
// for a transcript that stands on its own.
func ForSession(base, session string, limit int) SessionTrail {
	var out SessionTrail
	if session == "" {
		return out
	}
	events, err := ReadRecent(base, limit)
	if err != nil {
		return out
	}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Session != session {
			continue
		}
		switch e.Kind {
		case KindAgentCommand:
			out.Commands++
			if raw, err := ReadBlob(base, e.ResponseRef); err == nil {
				var resp agentCommandResponse
				if json.Unmarshal(raw, &resp) == nil && resp.Decision == "deny" {
					out.Denied++
				}
			}
		case KindAgentSpawn:
			out.Spawns = append(out.Spawns, SessionSpawn{Child: e.Action, Lease: e.Lease, At: time.UnixMilli(e.Ts)})
		default:
			continue
		}
		if e.Lease != "" && !slices.Contains(out.Leases, e.Lease) {
			out.Leases = append(out.Leases, e.Lease)
		}
	}
	return out
}

// relativize turns a recorded path into the workspace-relative form a review speaks. A path
// already relative, or one outside the workspace entirely, is returned unchanged; the latter
// then simply matches nothing, which is the honest outcome for a file this review is not about.
func relativize(root, p string) string {
	if p == "" || root == "" || !strings.HasPrefix(p, root) {
		return p
	}
	return strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
}

// ObservedCounts reports how many of each observed tool kind the trail holds, newest-first
// within limit.
//
// It exists for one question a fleet cannot answer any other way: is the observer actually
// RECORDING? An absent observer is silent by design (a per-read interruption would be worse),
// so a hook that is wired but writing nothing looks exactly like an agent that read nothing,
// and both look like a human wrote the file. This repository has already paid for that: 3252
// events, not one read, correct wiring, and a green doctor, because the hook resolved a PATH
// magus too old to know --observe.
//
// Counts rather than a verdict: what a healthy ratio looks like depends on the host, so the
// caller decides. Reporting reads==0 beside commands>0 is the fact that matters.
func ObservedCounts(base string, limit int) (reads, writes, shell int) {
	events, err := ReadRecent(base, limit)
	if err != nil {
		return 0, 0, 0
	}
	for _, e := range events {
		if e.Kind != KindAgentCommand || e.RequestRef == "" {
			continue
		}
		raw, rerr := ReadBlob(base, e.RequestRef)
		if rerr != nil {
			continue
		}
		var req agentCommandRequest
		if json.Unmarshal(raw, &req) != nil {
			continue
		}
		switch req.Tool {
		case toolRead:
			reads++
		case toolWrite:
			writes++
		case toolShell:
			shell++
		}
	}
	return reads, writes, shell
}

// commandProgram reduces a recorded command line to the program it invoked, dropping every
// argument. See Touch.Ran for why the arguments cannot be kept.
//
// Leading VAR=value assignments are skipped rather than reported, because they are the single
// likeliest place for a credential to sit; the observed leak was literally `T=<token> curl -H
// "Authorization: Bearer $T"`, whose first token IS the secret. Only the shape a shell would
// treat as an assignment counts, so a path that happens to contain "=" is still a program.
//
// A command this cannot read reduces to the empty string and is dropped. Reporting a
// best-guess program for an unparsable line would put an invented fact in a provenance
// record, and an admitted gap beats a low-confidence match here for the same reason it does
// in the notes store.
func commandProgram(cmd string) string {
	for _, tok := range strings.Fields(cmd) {
		if isEnvAssignment(tok) {
			continue
		}
		if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
			tok = tok[i+1:]
		}
		if tok == "" {
			continue
		}
		// A program name is short. Anything longer is not one, and truncating bounds what an
		// odd invocation can push into the payload.
		if len(tok) > commandProgramMax {
			tok = tok[:commandProgramMax]
		}
		return tok
	}
	return ""
}

// commandProgramMax bounds a reported program name.
const commandProgramMax = 32

// isEnvAssignment reports the NAME=value shape a shell treats as an environment assignment
// preceding the command, rather than any token containing "=".
func isEnvAssignment(tok string) bool {
	i := strings.IndexByte(tok, '=')
	if i <= 0 {
		return false
	}
	for j := 0; j < i; j++ {
		c := tok[j]
		switch {
		case c == '_', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case j > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
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
func upsertLatest(xs []types.DiffTouch, t types.DiffTouch) []types.DiffTouch {
	for i := range xs {
		if xs[i].Session == t.Session {
			xs[i] = t
			return xs
		}
	}
	return append(xs, t)
}
