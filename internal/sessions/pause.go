package sessions

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"unicode/utf8"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// KindSessionPause is the fact a session writes when it stops with work unfinished.
const KindSessionPause = "session_pause"

// Pause is where work was left, and what the next person needs to pick it up.
//
// It exists because work does not always stop because it finished. A day ends, a branch
// gets parked, an agent host hits a usage limit: in each case the work sits exactly
// where it was and nothing records that. Measured 2026-09-08, recovering one such stop
// meant a session id passed by hand, a guessed transcript location, and three failed
// commands before it emerged that the commits were unreachable.
//
// At is where the work sits, and Revision is the field that earns the record. A reader
// whose checkout cannot resolve it is looking at work that was never pushed, which is a
// different problem from a stale branch and wants a different fix; no branch name
// reveals it.
//
// Host and Session are attribution when a tool was driving, and empty when a person
// was. Session is the HOST's own session id, not this magus invocation's; Transcript is
// a pointer magus records and never opens, on the same terms as the guard's
// --transcript. They are repeated here rather than read off the enclosing
// [SessionStart] because a pause is routinely read on its own, the way [TargetResult]
// repeats Lease.
//
// Note is prose, clamped to [MaxMessageBytes] and otherwise untouched. magus does not
// fill it from a host's payload: a sentence about where the work stands is written by
// whoever stopped working, or by a wrapper that chose to pass one.
type Pause struct {
	Host       string              `json:"host,omitempty"`
	Session    string              `json:"session,omitempty"`
	Transcript string              `json:"transcript,omitempty"`
	Workspace  string              `json:"workspace,omitempty"`
	At         types.VCSCheckpoint `json:"at"`
	Note       string              `json:"note,omitempty"`
}

// RecordPause files p under dir, reporting whether anything was written.
//
// A record identical to the newest one already filed for the same line of work is
// skipped, so a host hook firing every turn costs a record only when the work moved.
// The history stays append-only: a superseded pause is never rewritten, it is followed
// by a newer one.
//
// start describes the writing invocation. Its Command and session id are set here.
func RecordPause(dir string, p Pause, start SessionStart) (recorded bool, err error) {
	p.Note = clampText(p.Note)
	fold, err := ReadAll(dir)
	if err != nil {
		return false, err
	}
	key := pauseKey(p)
	if prev, ok := latestPause(fold, key); ok && prev == p {
		return false, nil
	}
	start.Command = "session pause"
	w, err := Open(dir, pauseSessionID(key), start)
	if err != nil {
		return false, err
	}
	if err := w.Append(KindSessionPause, p); err != nil {
		return false, err
	}
	return true, nil
}

// Pauses returns the newest pause per line of work, most recent first: the unfinished
// work a person arriving at this repository could pick up.
func Pauses(fold Fold) []Pause {
	type stamped struct {
		p  Pause
		ts int64
	}
	newest := map[string]stamped{}
	for _, rec := range fold.Records {
		p, ok := decodePause(rec)
		if !ok {
			continue
		}
		key := pauseKey(p)
		if cur, seen := newest[key]; !seen || rec.Ts >= cur.ts {
			newest[key] = stamped{p: p, ts: rec.Ts}
		}
	}
	out := make([]stamped, 0, len(newest))
	for _, s := range newest {
		out = append(out, s)
	}
	// Ties break on the revision so two pauses filed in one millisecond order the same
	// way on every read, matching Fold's rule.
	slices.SortFunc(out, func(a, b stamped) int {
		if c := cmp.Compare(b.ts, a.ts); c != 0 {
			return c
		}
		return cmp.Compare(a.p.At.Revision, b.p.At.Revision)
	})
	ps := make([]Pause, len(out))
	for i, s := range out {
		ps[i] = s.p
	}
	return ps
}

// pauseKey identifies one line of paused work.
//
// A host session is the identity when there is one, so refiring a hook supersedes
// rather than accumulates. With no host - a person who ran the command - the identity
// is the branch, because a branch is already what a person's unfinished work is called.
// Keying everything on the host session would fold every pause a person ever took into
// a single record, which is the shape that makes a feature work only when a tool drives
// it.
//
// A branchless checkout (a detached HEAD, jj's anonymous change) falls back to the
// workspace, which is coarser but never collapses two repositories together.
func pauseKey(p Pause) string {
	switch {
	case p.Session != "":
		return "session\x00" + p.Host + "\x00" + p.Session
	case p.At.Branch != "":
		return "branch\x00" + p.At.Branch
	default:
		return "workspace\x00" + p.Workspace
	}
}

// pauseSessionID is the session file one line of work appends its pauses to.
//
// Derived from the key rather than minted per pause: a hook that fires every turn would
// otherwise write one session file per turn, and `magus session` would list forty rows
// of pause ahead of the runs a reader opened it for.
func pauseSessionID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "pause-" + hex.EncodeToString(sum[:])[:12]
}

// latestPause returns the newest pause already filed for one line of work.
func latestPause(fold Fold, key string) (Pause, bool) {
	var found Pause
	var ok bool
	for _, rec := range fold.Records {
		p, decoded := decodePause(rec)
		if decoded && pauseKey(p) == key {
			found, ok = p, true
		}
	}
	return found, ok
}

// decodePause reports the pause a record carries, and whether it carried one.
func decodePause(rec Record) (Pause, bool) {
	if rec.Kind != KindSessionPause {
		return Pause{}, false
	}
	var p Pause
	if json.Unmarshal(rec.Payload, &p) != nil {
		return Pause{}, false
	}
	return p, true
}

// clampText bounds a note to [MaxMessageBytes], cutting on a rune boundary so the
// stored line stays valid UTF-8. Same bound and same reason as an attention message:
// the store is grow-only and every reader folds all of it into memory.
func clampText(s string) string {
	if len(s) <= MaxMessageBytes {
		return s
	}
	cut := MaxMessageBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + messageTruncated
}
