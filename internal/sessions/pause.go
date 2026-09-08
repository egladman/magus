package sessions

import (
	"cmp"
	"slices"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// KindHandoff is the fact a session writes when its host reaches a stopping point.
const KindHandoff = "handoff"

// Handoff is what the NEXT session needs to pick this work up, on any host.
//
// It exists because a session does not always end by finishing. An agent host that
// hits a usage limit, crashes, or is closed mid-task leaves the work exactly where it
// was and says so nowhere: measured 2026-09-08, recovering one such session meant a
// UUID passed by hand, a guessed transcript location, and three git commands that
// failed before revealing the commits were unreachable. Every fact below is one that
// recovery needed and had to be reconstructed.
//
// Session is the HOST's session id, not this magus invocation's - it is the handle the
// host's own resume command takes, and the reason a person can be handed one string.
// Transcript is recorded as a pointer and never opened, on the same terms as the
// guard's --transcript.
//
// At is where the work sits. Revision is what answers the question a path cannot:
// a reader whose checkout cannot resolve it is looking at work that was never pushed,
// which is a different problem from a stale branch and wants a different fix.
//
// Note is the host's last word - whatever the harness passes as its final assistant
// message. It is prose magus neither parses nor trusts; it is here because a sentence
// of intent outperforms any field magus could invent for it.
type Handoff struct {
	Host       string              `json:"host,omitempty"`
	Session    string              `json:"session,omitempty"`
	Event      string              `json:"event,omitempty"`
	Transcript string              `json:"transcript,omitempty"`
	Workspace  string              `json:"workspace,omitempty"`
	At         types.VCSCheckpoint `json:"at"`
	Note       string              `json:"note,omitempty"`
}

// RecordHandoff appends h to the repository's session store under dir, reporting
// whether anything was written.
//
// A host hook fires on every turn, and most turns move nothing a handoff describes, so
// a record identical to the newest one already filed for the same host session is
// skipped. That keeps the store proportional to progress rather than to turn count
// while leaving the history append-only: a superseded handoff is never rewritten, it is
// simply followed by a newer one.
//
// start describes the writing invocation; its Command is set here, and the magus
// session id is minted by [NewID].
func RecordHandoff(dir string, h Handoff, start SessionStart) (recorded bool, err error) {
	fold, err := ReadAll(dir)
	if err != nil {
		return false, err
	}
	if prev, ok := latestHandoff(fold, h.Host, h.Session); ok && prev == h {
		return false, nil
	}
	start.Command = "session handoff"
	w, err := Open(dir, NewID(), start)
	if err != nil {
		return false, err
	}
	if err := w.Append(KindHandoff, h); err != nil {
		return false, err
	}
	return true, nil
}

// Handoffs returns the newest handoff per host session, most recent first: the open
// threads a person arriving at this repository could resume.
func Handoffs(fold Fold) []Handoff {
	type stamped struct {
		h  Handoff
		ts int64
	}
	newest := map[string]stamped{}
	for _, rec := range fold.Records {
		if rec.Kind != KindHandoff {
			continue
		}
		var h Handoff
		if json.Unmarshal(rec.Payload, &h) != nil {
			continue
		}
		key := h.Host + "\x00" + h.Session
		if cur, ok := newest[key]; !ok || rec.Ts >= cur.ts {
			newest[key] = stamped{h: h, ts: rec.Ts}
		}
	}
	out := make([]stamped, 0, len(newest))
	for _, s := range newest {
		out = append(out, s)
	}
	// Ties break on the host session id so two handoffs filed in one millisecond
	// order the same way on every read, matching Fold's rule.
	slices.SortFunc(out, func(a, b stamped) int {
		if c := cmp.Compare(b.ts, a.ts); c != 0 {
			return c
		}
		return cmp.Compare(a.h.Session, b.h.Session)
	})
	hs := make([]Handoff, len(out))
	for i, s := range out {
		hs[i] = s.h
	}
	return hs
}

// latestHandoff returns the newest handoff already filed for one host session.
func latestHandoff(fold Fold, host, session string) (Handoff, bool) {
	var found Handoff
	var ok bool
	for _, rec := range fold.Records {
		if rec.Kind != KindHandoff {
			continue
		}
		var h Handoff
		if json.Unmarshal(rec.Payload, &h) != nil {
			continue
		}
		if h.Host == host && h.Session == session {
			found, ok = h, true
		}
	}
	return found, ok
}
