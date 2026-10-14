package job

import (
	"context"
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// endedSource is the attention source of every row magus files when it ends work whose
// owner went away or whose deadline passed.
const endedSource = "magus/ownership"

// NoteEnded files one attention row in the repository at root saying magus ended work
// lease owned, for whoever reads the queue: the root session, which otherwise learns of
// it only by polling. The same message noted twice is one row (see
// [sessions.RequestID]), so two processes ending the same work raise it once.
//
// Best effort: a repository with no session store, or a write that fails, files nothing,
// since the ending itself already happened and is recorded where it happened.
func NoteEnded(ctx context.Context, root, lease, message string) {
	if root == "" {
		return
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return
	}
	_, _, _ = sessions.OpenRequest(dir, endedSource, sessions.AttentionOpen{
		Outcome:  string(types.OutcomeFailed),
		Severity: string(types.SeverityWarning),
		Source:   endedSource,
		Lease:    lease,
		Message:  message,
	}, sessions.InvocationStart{Origin: trail.LocalOrigin(ctx), Workspace: root})
}

// noteEnded is [NoteEnded] for a row this store ended.
func (s *Store) noteEnded(id, reason string) {
	NoteEnded(context.Background(), s.root, id, fmt.Sprintf("magus ended job %s: %s", id, reason))
}

// EndSession ends the job c's record binds it to, now that c's session is over, and
// turns the record into a tombstone so nothing is graded as c until a session binds
// again. It reports the row and true when it ended one.
//
// Only a row c took and is still editing ends: a declared row nobody took is the
// forker's, and an exited one already returned its result. The end_reason opens with
// [types.EndReasonSessionEnded], which is what lets a resumed session's next
// `magus job exec` take the row back.
//
// An identity-less caller ends nothing: its record is the checkout's, shared by every
// such caller there, so one caller's end says nothing about the rest.
func (s *Store) EndSession(ctx context.Context, c Caller) (types.Job, bool, error) {
	if !c.Identified() {
		return types.Job{}, false, nil
	}
	path := s.record(c)
	b := readBinding(path)
	if b.Job == "" || b.Gone {
		return types.Job{}, false, nil
	}
	now := s.now().Unix()
	var ended types.Job
	var outcomes endings
	err := s.update(ctx, func(f *jobsFile) error {
		outcomes = endings{}
		i := slices.IndexFunc(f.Jobs, func(r types.Job) bool { return r.ID == b.Job })
		if i < 0 || !Editing(f.Jobs[i]) || f.Jobs[i].Registered == 0 || f.Jobs[i].Holder.OrSession() == types.HolderServer {
			return file.ErrSkipWrite
		}
		if err := readOnly(f.Jobs[i]); err != nil {
			return err
		}
		f.Jobs[i].State = types.StateNoReturn
		f.Jobs[i].EndReason = fmt.Sprintf("%s (%s); `magus job exec %s` from a resumed session takes it back",
			types.EndReasonSessionEnded, c.Host, b.Job)
		f.Jobs[i].Updated = now
		ended = f.Jobs[i].Clone()
		return outcomes.hold(s, ended)
	})
	if err != nil {
		return types.Job{}, false, err
	}
	_ = file.ReplaceFile(path, fmt.Appendf(nil, "%s %s %d\n", tombstoneTag, b.Job, now), 0o600)
	if ended.ID == "" {
		return types.Job{}, false, nil
	}
	s.noteEnded(ended.ID, ended.EndReason)
	if err := outcomes.record(ctx, now*1000); err != nil {
		return ended, true, err
	}
	return ended, true, nil
}

// TreeRoot is the root of the job tree id belongs to: its topmost ancestor, or id itself
// when it has none or no row declares it. It reads the store without sweeping, so it
// writes nothing and runs no VCS probe.
func (s *Store) TreeRoot(id string) (string, error) {
	f, err := s.read()
	if err != nil {
		return "", err
	}
	if ancestors := types.JobAncestors(f.Jobs, id); len(ancestors) > 0 {
		return ancestors[len(ancestors)-1].ID, nil
	}
	return id, nil
}
