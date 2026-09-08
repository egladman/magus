package sessions

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// KindPause is the fact a session writes when it stops with work unfinished.
const KindPause = "pause"

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
// Host and HostSession are attribution when a tool was driving, and empty when a person
// was. HostSession is spelled in full because [Record.Session] and
// [AttentionRequest.Session] are magus's own session and this is not: one store holding
// two things called "session" is a bug report waiting for a quiet week. Transcript is a
// pointer magus records and never opens, on the same terms as the guard's --transcript.
//
// Note is prose, clamped to [MaxMessageBytes] and otherwise untouched. magus does not
// fill it from a host's payload: a sentence about where the work stands is written by
// whoever stopped working, or by a wrapper that chose to pass one.
type Pause struct {
	Host        string              `json:"host,omitempty"`
	HostSession string              `json:"host_session,omitempty"`
	Transcript  string              `json:"transcript,omitempty"`
	Workspace   string              `json:"workspace,omitempty"`
	At          types.VCSCheckpoint `json:"at"`
	Note        string              `json:"note,omitempty"`
}

// PauseRecord is one pause with the envelope's timestamp resolved, mirroring
// [GateRecord]. The time is what makes a listing orderable and a stale pause visible,
// and it lives on the envelope rather than in the payload.
type PauseRecord struct {
	Pause
	At time.Time
}

// RecordPause files p under dir, returning it as stored and whether anything was
// written.
//
// A record identical to the newest one already filed for the same line of work is
// skipped, so a host hook firing every turn costs a record only when the work moved.
// The history stays append-only: a superseded pause is never rewritten, it is followed
// by a newer one.
//
// The returned Pause is what went to disk, Note already clamped, so a caller reporting
// what it recorded cannot print something the store does not hold.
//
// start describes the writing invocation. Its Command and session id are set here.
func RecordPause(dir string, p Pause, start SessionStart) (stored Pause, recorded bool, err error) {
	p.Note = boundMessage(p.Note)
	session := pauseSessionID(pauseKey(p))
	// One file, not the whole store. Every pause for this line of work is in the file
	// pauseSessionID names and nowhere else, so folding the store would decode a
	// 30-day history to answer a question one file holds - on every turn of every
	// session, which is when this runs.
	records, _, _ := readFile(filepath.Join(dir, session+fileExt))
	if prev, ok := latestPause(records); ok && prev == p {
		return p, false, nil
	}
	start.Command = "session pause"
	w, err := Open(dir, session, start)
	if err != nil {
		return p, false, err
	}
	if err := w.Append(KindPause, p); err != nil {
		return p, false, err
	}
	return p, true, nil
}

// LatestPauses returns the newest pause per line of work, most recent first: the
// unfinished work a person arriving at this repository could pick up.
func LatestPauses(fold Fold) []PauseRecord {
	newest := map[string]PauseRecord{}
	for _, rec := range fold.Records {
		p, ok := decodePause(rec)
		if !ok {
			continue
		}
		key := pauseKey(p)
		at := time.UnixMilli(rec.Ts)
		if cur, seen := newest[key]; !seen || !at.Before(cur.At) {
			newest[key] = PauseRecord{Pause: p, At: at}
		}
	}
	out := make([]PauseRecord, 0, len(newest))
	for _, r := range newest {
		out = append(out, r)
	}
	// The key breaks the tie, and it is unique per element, so the order is total: `out`
	// is built by ranging a map and SortFunc is not stable, so a comparator that can
	// return 0 renders two pauses in a different order on each read.
	slices.SortFunc(out, func(a, b PauseRecord) int {
		if c := b.At.Compare(a.At); c != 0 {
			return c
		}
		return cmp.Compare(pauseKey(a.Pause), pauseKey(b.Pause))
	})
	return out
}

// pauseKey identifies one line of paused work.
//
// A host session is the identity when there is one, so refiring a hook supersedes
// rather than accumulates. With no host - a person who ran the command - the identity
// is the branch IN a workspace. Keying everything on the host session would fold every
// pause a person ever took into a single record, which is the shape that makes a
// feature work only when a tool drives it; keying on the branch alone would then
// collapse two worktrees parked on the same branch, which since the store went
// repo-wide is an ordinary Tuesday.
//
// A branchless checkout (a detached HEAD, jj's anonymous change) falls back to the
// workspace, which is coarser but never merges two of them.
func pauseKey(p Pause) string {
	switch {
	case p.HostSession != "":
		return "session\x00" + p.Host + "\x00" + p.HostSession
	case p.At.Branch != "":
		return "branch\x00" + p.Workspace + "\x00" + p.At.Branch
	default:
		return "workspace\x00" + p.Workspace
	}
}

// pauseSessionID is the session file one line of work appends its pauses to.
//
// Derived from the key rather than minted per pause: a hook that fires every turn would
// otherwise write one session file per turn, and `magus session` would list forty rows
// of pause ahead of the runs a reader opened it for.
//
// Two processes pausing one line of work concurrently therefore open the same file.
// Appends stay whole (the store's package doc has the POSIX rule), but both can stamp
// one sequence number, which leaves the pair ordering those two records arbitrarily.
// Both are still read, and the newest by timestamp still wins, so the cost is an
// ambiguous order between two records written in the same instant.
func pauseSessionID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "pause-" + hex.EncodeToString(sum[:])[:12]
}

// latestPause returns the newest pause among records, which the caller has already
// narrowed to one line of work.
func latestPause(records []Record) (Pause, bool) {
	var found Pause
	var ok bool
	for _, rec := range records {
		if p, decoded := decodePause(rec); decoded {
			found, ok = p, true
		}
	}
	return found, ok
}

// decodePause reports the pause a record carries, and whether it carried one.
func decodePause(rec Record) (Pause, bool) {
	if rec.Kind != KindPause {
		return Pause{}, false
	}
	var p Pause
	if json.Unmarshal(rec.Payload, &p) != nil {
		return Pause{}, false
	}
	return p, true
}
