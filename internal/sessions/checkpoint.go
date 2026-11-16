package sessions

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/vcs"
	driver "github.com/egladman/magus/vcs"
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
// It is the same noun [vcs.Checkpoint] names, kept rather than computed.
// `magus vcs checkpoint` works one out and prints it, this records one and
// keeps it. What makes a checkpoint worth keeping is Note, so the position
// arrives with the reason someone stopped at it.
//
// Tree is where the work sits, and its Revision is the field that earns the record. A
// reader whose checkout cannot resolve it is looking at work that was never pushed,
// which is a different problem from a stale branch and wants a different fix; no branch
// name reveals it.
//
// Host and HostSession are attribution when a host was driving, and empty when none
// delivered them. Transcript is a pointer magus records and never opens, on the same
// terms as the guard's --transcript.
//
// Note is prose, clamped to [MaxMessageBytes] and otherwise untouched. magus does not
// fill it from a host's payload: a sentence about where the work stands is written by
// whoever stopped working, or by a wrapper that chose to pass one.
type Checkpoint struct {
	Host        string         `json:"host,omitempty"`
	HostSession string         `json:"host_session,omitempty"`
	Transcript  string         `json:"transcript,omitempty"`
	Workspace   string         `json:"workspace,omitempty"`
	Tree        vcs.Checkpoint `json:"tree"`
	Note        string         `json:"note,omitempty"`
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
// start describes the writing invocation. Its Command and invocation id are set here.
func RecordCheckpoint(dir string, c Checkpoint, start session.InvocationStart) (stored Checkpoint, recorded bool, err error) {
	c.Note = boundMessage(c.Note)
	id := checkpointInvocationID(checkpointKey(c))
	// One file, not the whole store. Every checkpoint for this line of work is in the
	// file checkpointInvocationID names and nowhere else, so folding the store would
	// decode a 30-day history to answer a question one file holds, on every turn of
	// every session, which is when this runs.
	records := session.ReadInvocation(dir, id)
	if prev, ok := latestCheckpoint(records); ok && prev == c {
		return c, false, nil
	}
	start.Command = "session checkpoint"
	w, err := openWriter(dir, id, start)
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
func LatestCheckpoints(fold session.Fold) []CheckpointRecord {
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
		return fmt.Sprintf("session\x00%s\x00%s", c.Host, c.HostSession)
	case c.Tree.Branch != "":
		return fmt.Sprintf("branch\x00%s\x00%s", c.Workspace, c.Tree.Branch)
	default:
		return "workspace\x00" + c.Workspace
	}
}

// checkpointInvocationID is the file one line of work appends its checkpoints to.
//
// Derived from the key rather than minted per checkpoint: a hook that fires every turn
// would otherwise write one file per turn, and `magus session` would list forty
// rows of checkpoint ahead of the runs a reader opened it for.
//
// Two processes checkpointing one line of work concurrently therefore open the same
// file. Appends stay whole (the store's package doc has the POSIX rule), but both can
// stamp one sequence number, which leaves the pair ordering those two records
// arbitrarily. Both are still read, and the newest by timestamp still wins, so the cost
// is an ambiguous order between two records written in the same instant.
func checkpointInvocationID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "checkpoint-" + hex.EncodeToString(sum[:])[:12]
}

// latestCheckpoint returns the newest checkpoint among records, which the caller has
// already narrowed to one line of work.
func latestCheckpoint(records []session.Record) (Checkpoint, bool) {
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
func decodeCheckpoint(rec session.Record) (Checkpoint, bool) {
	if rec.Kind != KindCheckpoint {
		return Checkpoint{}, false
	}
	var c Checkpoint
	if codec.Unmarshal(rec.Payload, &c) != nil {
		return Checkpoint{}, false
	}
	return c, true
}

// checkpointsShown bounds the block. A checkpoint from three weeks ago is still where that
// work sits, so the list is bounded by count rather than by age, and the tail is pointed at
// rather than dropped silently.
//
// It is NOT unbounded in time, whatever this comment used to say: Prune drops a
// whole session file 30 days after its newest record, with an exemption for open attention
// requests and none for checkpoints. A line of work parked for a month loses every
// checkpoint it had, silently. Either that exemption is owed or this block should say so.
const checkpointsShown = 3

// renderCheckpoints prints unfinished work above the history, because "where was I" is
// the question a person opens this listing with, and the history answers a different one.
//
// It is deliberately not filtered by --since. That flag bounds what RAN recently; a
// checkpoint records what has not finished, and hiding an old one would hide the case
// this exists for.
func renderCheckpoints(ctx context.Context, root string, w io.Writer, checkpoints []CheckpointRecord) {
	if len(checkpoints) == 0 {
		return
	}
	fmt.Fprintln(w, "Checkpoints, most recent first:")
	for _, r := range checkpoints[:min(len(checkpoints), checkpointsShown)] {
		fmt.Fprintf(w, "  %s  %s\n", r.At.Format("2006-01-02 15:04:05"), checkpointWhere(r.Checkpoint))
		if r.Note != "" {
			fmt.Fprintf(w, "    %s\n", r.Note)
		}
		if r.HostSession != "" {
			fmt.Fprintf(w, "    session %s\n", r.HostSession)
		}
		if r.Transcript != "" {
			fmt.Fprintf(w, "    transcript %s\n", r.Transcript)
		}
	}
	if extra := len(checkpoints) - checkpointsShown; extra > 0 {
		fmt.Fprintf(w, "  and %d more; `%s` lists them all\n", extra, hint.Session.With("-o", "json"))
	}
	printCheckpointInspect(ctx, root, w, checkpoints[0].Checkpoint)
	fmt.Fprintln(w)
}

// printCheckpointInspect prints the command that shows what changed since the newest
// checkpoint, already substituted, for THIS repository's backend.
//
// Composed by the DRIVER, never here. magus drives git, Mercurial, Sapling and Jujutsu,
// and each spells this differently; a reader who assembles one from memory is guessing
// which of the four they are in, and the same guess is what a skill hardcoding `git
// diff` teaches them to make. DiffCommands is the one method that already knows, and
// until now it had a single caller.
//
// Best-effort and silent on failure: a revisionless checkpoint has no base to diff from,
// and a listing that refuses to print because a VCS probe failed would be worse than one
// that prints what it has. Only for the newest, because that is the one a reader acts on
// and each call costs a subprocess.
func printCheckpointInspect(ctx context.Context, root string, w io.Writer, c Checkpoint) {
	if root == "" || c.Tree.Revision == "" {
		return
	}
	res, err := driver.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return
	}
	hints, err := res.VCS.DiffCommands(ctx, root, c.Tree.Revision)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "  what changed since the newest:\n    %s\n", hints.CLI)
	if hints.GUI != "" {
		fmt.Fprintf(w, "    %s\n", hints.GUI)
	}
}

// checkpointWhere reads one back as a phrase. The revision leads because it is the fact
// a reader acts on: a checkout that cannot resolve it is looking at work that was never
// pushed, which no branch name would have revealed.
//
// A checkpoint with no revision is not a broken record. A tree before its first commit,
// or under no VCS at all, still has a location worth naming, and the workspace is it.
func checkpointWhere(c Checkpoint) string {
	var b strings.Builder
	if c.Host != "" {
		fmt.Fprintf(&b, "%s ", c.Host)
	}
	if c.Tree.Revision == "" {
		b.WriteString(c.Workspace)
		return b.String()
	}
	b.WriteString(c.Tree.Revision[:min(len(c.Tree.Revision), 12)])
	if c.Tree.Branch != "" {
		fmt.Fprintf(&b, " on %s", c.Tree.Branch)
	}
	if c.Tree.Dirty {
		b.WriteString(", uncommitted changes")
	}
	return b.String()
}

// CheckpointRecordedLine is the terminal reading of one write: where the work sits, and
// whether this call changed anything. The location is rendered by the same function the
// listing uses, so recording a checkpoint and reading one back describe it identically.
//
// Distinct from vcs.go's checkpointLine, which renders the VCS triple alone in fixed
// columns for `magus vcs checkpoint`. Same noun, two audiences.
func CheckpointRecordedLine(c Checkpoint, recorded bool) string {
	verb := "checkpoint at"
	if !recorded {
		verb = "unchanged since the last checkpoint at"
	}
	line := fmt.Sprintf("%s %s", verb, checkpointWhere(c))
	if c.Note != "" {
		line += ": " + c.Note
	}
	return line
}
