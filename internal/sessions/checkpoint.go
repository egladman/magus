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

// KindCheckpoint is the fact a session writes when it stops with work unfinished.
const KindCheckpoint = "checkpoint"

// Checkpoint is where work was left, and what the next person needs to pick it up.
//
// It exists because work does not always stop because it finished. A day ends, a branch
// gets parked, an agent host hits a usage limit: in each case the work sits exactly
// where it was and nothing records that. Measured 2026-09-08, recovering one such stop
// meant a session id passed by hand, a guessed transcript location, and three failed
// commands before it emerged that the commits were unreachable.
//
// It is the same noun [types.VCSCheckpoint] names, kept rather than computed: `magus vcs
// checkpoint` works one out and prints it, this records one and keeps it. What makes a
// checkpoint worth keeping is Note, so the position arrives with the reason someone
// stopped at it.
//
// Tree is where the work sits, and its Revision is the field that earns the record. A
// reader whose checkout cannot resolve it is looking at work that was never pushed,
// which is a different problem from a stale branch and wants a different fix; no branch
// name reveals it.
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
type Checkpoint struct {
	Host        string              `json:"host,omitempty"`
	HostSession string              `json:"host_session,omitempty"`
	Transcript  string              `json:"transcript,omitempty"`
	Workspace   string              `json:"workspace,omitempty"`
	Tree        types.VCSCheckpoint `json:"tree"`
	Note        string              `json:"note,omitempty"`
}

// CheckpointRecord is one checkpoint with the envelope's timestamp resolved, mirroring
// [GateRecord]. The time is what makes a listing orderable and a stale checkpoint
// visible, and it lives on the envelope rather than in the payload.
//
// The payload's own position field is [Checkpoint.Tree] rather than At, so this At means
// one thing: when it was recorded.
type CheckpointRecord struct {
	Checkpoint
	At time.Time
}

// RecordCheckpoint files c under dir, returning it as stored and whether anything was
// written.
//
// A record identical to the newest one already filed for the same line of work is
// skipped, so a host hook firing every turn costs a record only when the work moved.
// The history stays append-only: a superseded checkpoint is never rewritten, it is
// followed by a newer one.
//
// The returned Checkpoint is what went to disk, Note already clamped, so a caller
// reporting what it recorded cannot print something the store does not hold.
//
// start describes the writing invocation. Its Command and session id are set here.
func RecordCheckpoint(dir string, c Checkpoint, start SessionStart) (stored Checkpoint, recorded bool, err error) {
	c.Note = boundMessage(c.Note)
	session := checkpointSessionID(checkpointKey(c))
	// One file, not the whole store. Every checkpoint for this line of work is in the
	// file checkpointSessionID names and nowhere else, so folding the store would
	// decode a 30-day history to answer a question one file holds, on every turn of
	// every session, which is when this runs.
	records, _, _ := readFile(filepath.Join(dir, session+fileExt))
	if prev, ok := latestCheckpoint(records); ok && prev == c {
		return c, false, nil
	}
	start.Command = "session checkpoint"
	w, err := Open(dir, session, start)
	if err != nil {
		return c, false, err
	}
	if err := w.Append(KindCheckpoint, c); err != nil {
		return c, false, err
	}
	return c, true, nil
}

// LatestCheckpoints returns the newest checkpoint per line of work, most recent first:
// the unfinished work a person arriving at this repository could pick up.
func LatestCheckpoints(fold Fold) []CheckpointRecord {
	newest := map[string]CheckpointRecord{}
	for _, rec := range fold.Records {
		c, ok := decodeCheckpoint(rec)
		if !ok {
			continue
		}
		key := checkpointKey(c)
		at := time.UnixMilli(rec.Ts)
		if cur, seen := newest[key]; !seen || !at.Before(cur.At) {
			newest[key] = CheckpointRecord{Checkpoint: c, At: at}
		}
	}
	out := make([]CheckpointRecord, 0, len(newest))
	for _, r := range newest {
		out = append(out, r)
	}
	// The key breaks the tie, and it is unique per element, so the order is total: `out`
	// is built by ranging a map and SortFunc is not stable, so a comparator that can
	// return 0 renders two checkpoints in a different order on each read.
	slices.SortFunc(out, func(a, b CheckpointRecord) int {
		if c := b.At.Compare(a.At); c != 0 {
			return c
		}
		return cmp.Compare(checkpointKey(a.Checkpoint), checkpointKey(b.Checkpoint))
	})
	return out
}

// checkpointKey identifies one line of work.
//
// A host session is the identity when there is one, so refiring a hook supersedes
// rather than accumulates. With no host (a person who ran the command), the identity
// is the branch IN a workspace. Keying everything on the host session would fold every
// checkpoint a person ever took into a single record, which is the shape that makes a
// feature work only when a tool drives it; keying on the branch alone would then
// collapse two worktrees parked on the same branch, which since the store went
// repo-wide is an ordinary Tuesday.
//
// A branchless checkout (a detached HEAD, jj's anonymous change) falls back to the
// workspace, which is coarser but never merges two of them.
func checkpointKey(c Checkpoint) string {
	switch {
	case c.HostSession != "":
		return "session\x00" + c.Host + "\x00" + c.HostSession
	case c.Tree.Branch != "":
		return "branch\x00" + c.Workspace + "\x00" + c.Tree.Branch
	default:
		return "workspace\x00" + c.Workspace
	}
}

// checkpointSessionID is the session file one line of work appends its checkpoints to.
//
// Derived from the key rather than minted per checkpoint: a hook that fires every turn
// would otherwise write one session file per turn, and `magus session` would list forty
// rows of checkpoint ahead of the runs a reader opened it for.
//
// Two processes checkpointing one line of work concurrently therefore open the same
// file. Appends stay whole (the store's package doc has the POSIX rule), but both can
// stamp one sequence number, which leaves the pair ordering those two records
// arbitrarily. Both are still read, and the newest by timestamp still wins, so the cost
// is an ambiguous order between two records written in the same instant.
func checkpointSessionID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "checkpoint-" + hex.EncodeToString(sum[:])[:12]
}

// latestCheckpoint returns the newest checkpoint among records, which the caller has
// already narrowed to one line of work.
func latestCheckpoint(records []Record) (Checkpoint, bool) {
	var found Checkpoint
	var ok bool
	for _, rec := range records {
		if c, decoded := decodeCheckpoint(rec); decoded {
			found, ok = c, true
		}
	}
	return found, ok
}

// decodeCheckpoint reports the checkpoint a record carries, and whether it carried one.
func decodeCheckpoint(rec Record) (Checkpoint, bool) {
	if rec.Kind != KindCheckpoint {
		return Checkpoint{}, false
	}
	var c Checkpoint
	if json.Unmarshal(rec.Payload, &c) != nil {
		return Checkpoint{}, false
	}
	return c, true
}
