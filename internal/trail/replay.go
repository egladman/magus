package trail

import (
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
)

// Touch is one agent session's contact with one file: that it wrote the file, what it had
// READ beforehand in the same session, and where the host's own transcript of that session
// lives.
//
// The read list is the part no other review tool has. A guard hook sees every path an agent
// reaches, so magus can say what an agent was LOOKING AT immediately before it wrote
// something - which is the closest thing to "why is this change shaped like this" that any
// tool can produce without asking the author. "It changed this because it had just read that"
// is a sentence a forge cannot say.
type Touch struct {
	// Host is the agent host's own label for itself, empty when its wrapper passed none.
	Host string `json:"host,omitempty" yaml:"host,omitempty"`
	// Session is the host's own session id - the thing that groups these events.
	Session string `json:"session,omitempty" yaml:"session,omitempty"`
	// Transcript points at the host's record of the session. A POINTER, never content: magus
	// never opens it, so the trail stays a record of paths and timings while the expensive and
	// sensitive detail stays where the host already put it.
	Transcript string `json:"transcript,omitempty" yaml:"transcript,omitempty"`
	// At is when the session last wrote this file.
	At time.Time `json:"at" yaml:"at"`
	// Read are the paths the session reached BEFORE that write, most recent first. Capped: the
	// last handful is the context that explains the edit, and the whole session's reach is a
	// different question with a different surface.
	Read []string `json:"read,omitempty" yaml:"read,omitempty"`
	// Ran are shell commands the session ran before the write, most recent first and capped.
	Ran []string `json:"ran,omitempty" yaml:"ran,omitempty"`
}

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
// this", and the answer is the last few things - a hundred paths is a session transcript, not
// an explanation, and the transcript pointer is right there for anyone who wants that.
const (
	replayReadCap = 6
	replayRanCap  = 3
)

// Replay reconstructs, for each of paths, which agent sessions wrote it and what they had read
// first. It reads at most limit recent events.
//
// Best-effort throughout: an unreadable blob, a missing trail, or a host that supplied no
// session id all just contribute less. A review must still open when nothing was recorded,
// which is the normal case for a workspace whose agents have no guard hook wired.
func Replay(root, base string, paths []string, limit int) map[string][]Touch {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	events, err := ReadRecent(base, limit)
	if err != nil || len(events) == 0 {
		return nil
	}

	// One pass, OLDEST first, accumulating each session's reads and commands as they happen so
	// that a write can take a snapshot of what came before it. Walking newest-first would mean
	// knowing the writes before the reads that explain them, which is the wrong direction for
	// the only question being asked.
	//
	// REVERSED rather than sorted by Ts. The events file is append-only, so its order IS the
	// chronology; Ts is a lossy shadow of it, stamped in whole milliseconds. A burst of hook
	// observations - which is the normal shape, since an agent reads several files and then
	// writes one, all inside a millisecond - lands on identical timestamps, and a stable sort
	// over ties preserves whatever order it was handed. ReadRecent hands back newest-first, so
	// sorting by Ts silently walked the whole thing backwards and every read looked like it
	// happened after the write it explained.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	type sessionState struct {
		read []string
		ran  []string
	}
	sessions := map[string]*sessionState{}
	out := map[string][]Touch{}

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
		// Session is the grouping key. A host that supplies none still produces attributable
		// events, but they cannot be threaded into a story, so they are skipped rather than
		// all collapsed into one fictional session.
		key := req.Session
		if key == "" {
			continue
		}
		st := sessions[key]
		if st == nil {
			st = &sessionState{}
			sessions[key] = st
		}

		// A hook records the path exactly as its host supplied it, which is an ABSOLUTE path
		// for every host observed so far, while a review speaks workspace-relative. Without
		// this the two vocabularies never meet and every file reports no history at all -
		// which looks identical to "no hook is wired" and is why it was worth a helper rather
		// than a comparison at each site.
		reqPath := relativize(root, req.Path)

		switch req.Tool {
		case toolRead:
			if reqPath != "" {
				st.read = prependCapped(st.read, reqPath, replayReadCap)
			}
		case toolShell:
			if req.Command != "" {
				st.ran = prependCapped(st.ran, req.Command, replayRanCap)
			}
		case toolWrite:
			if reqPath == "" || !want[reqPath] {
				continue
			}
			// The read list is snapshotted at the moment of the write, so a path the session
			// reached AFTER this edit does not retroactively become its explanation.
			t := Touch{
				Host:       req.Host,
				Session:    req.Session,
				Transcript: req.Transcript,
				At:         time.UnixMilli(e.Ts),
				Read:       withoutSelf(st.read, reqPath),
				Ran:        append([]string(nil), st.ran...),
			}
			out[reqPath] = upsertLatest(out[reqPath], t)
		}
	}
	return out
}

// relativize turns a recorded path into the workspace-relative form a review speaks. A path
// already relative, or one outside the workspace entirely, is returned unchanged - the latter
// then simply matches nothing, which is the honest outcome for a file this review is not about.
func relativize(root, p string) string {
	if p == "" || root == "" || !strings.HasPrefix(p, root) {
		return p
	}
	return strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
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
func upsertLatest(xs []Touch, t Touch) []Touch {
	for i := range xs {
		if xs[i].Session == t.Session {
			xs[i] = t
			return xs
		}
	}
	return append(xs, t)
}
