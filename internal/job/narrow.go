package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
)

// releaseRequestSource is the attention source of a request asking a job to release write
// paths a refused fork needed.
const releaseRequestSource = "magus/job-release"

// projectConfig reports whether rel is a project's configuration this workspace loads: a
// magusfile source or a magus.yaml among describe.WorkspaceLoadFiles, the set
// sharedCheckoutProblems guards. The spell sources a magusfile imports are left out: they
// are code a job owns, not a table every job edits.
func projectConfig(root, rel string) bool {
	if !configName(rel) {
		return false
	}
	return slices.Contains(describe.WorkspaceLoadFiles(root), rel)
}

// configName reports whether rel is named as a project's configuration is: magusfile.buzz,
// a file under magusfiles/, or magus.yaml. projectConfig asks the tree whether it is one.
func configName(rel string) bool {
	switch {
	case path.Base(rel) == "magusfile.buzz", path.Base(rel) == "magus.yaml":
		return true
	}
	return path.Base(path.Dir(rel)) == "magusfiles"
}

// stored is the ledger's row for id.
func (s *Store) stored(ctx context.Context, id string) (job.Job, error) {
	rows, err := s.List(ctx)
	if err != nil {
		return job.Job{}, err
	}
	at := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == id })
	if at < 0 {
		return job.Job{}, fmt.Errorf("%q: %w", id, job.ErrUnknownJob)
	}
	return rows[at], nil
}

// errNarrowedAlready is apply's sentinel for a call that adds nothing.
var errNarrowedAlready = errors.New("job: hold already narrowed")

// NarrowHold narrows live job id's whole-file hold on file to claims.
// It adds `file#<word>` for each declaration in touched id does not claim
// and answers them. The release records takenBy, the fork that asked.
//
// It refuses a pattern, an unreadable footprint, a file no diff driver
// places, a non-live row, and a declaration another live row claims.
func NarrowHold(ctx context.Context, store *Store, id, file string, touched []string, takenBy string) (job.Job, []string, error) {
	if store == nil || store.root == "" {
		return job.Job{}, nil, errors.New("job: no workspace to narrow a hold in")
	}
	file = path.Clean(strings.TrimSpace(file))
	if strings.ContainsAny(file, globMeta) {
		return job.Job{}, nil, fmt.Errorf("job: %s is a pattern, and a declaration lives in one file", file)
	}
	driver, drivers, err := claimDrivers(ctx, store.root, []string{file})
	if err != nil {
		return job.Job{}, nil, fmt.Errorf("no claim on %s can be graded, so its hold stays whole: %w", file, err)
	}
	if drivers[file] == "" {
		return job.Job{}, nil, fmt.Errorf("no diff driver places the declarations of %s, so its hold stays whole", file)
	}
	all, ok := readDeclarations(ctx, driver, store.root, file)
	if !ok {
		return job.Job{}, nil, fmt.Errorf("the footprint of %s cannot be read, so its hold stays whole", file)
	}
	var added []string
	row, err := store.MutateSeeing(ctx, id, job.AsNarrowing, func(cur *job.Job, exists bool, now int64, rows []job.Job) error {
		added = nil
		switch {
		case !exists:
			return fmt.Errorf("%q: %w", id, job.ErrUnknownJob)
		case !Editing(*cur):
			return fmt.Errorf("job: %s is %s and holds nothing to narrow", id, cur.State)
		case !wholeFileHeld(*cur, file):
			return fmt.Errorf("job: no whole-file write path of %s covers %s, so there is no hold on it to narrow", id, file)
		}
		var err error
		added, err = claimTouched(*cur, rows, id, file, touched, all)
		if err != nil {
			return err
		}
		if len(added) == 0 && job.Narrowed(*cur, file) {
			return errNarrowedAlready
		}
		cur.Paths.Write = append(cur.Paths.Write, added...)
		recordNarrowing(cur, file, takenBy, now, store.Digest(ctx, file))
		return nil
	})
	if errors.Is(err, errNarrowedAlready) {
		row, err = store.stored(ctx, id)
		return row, nil, err
	}
	if err != nil {
		return job.Job{}, nil, err
	}
	return row, added, nil
}

// wholeFileHeld reports whether a write path of row covers file whole.
func wholeFileHeld(row job.Job, file string) bool {
	return slices.ContainsFunc(row.Paths.Write, func(entry string) bool {
		p, decl := job.SplitClaim(entry)
		return decl == "" && job.CoversPath(p, file)
	})
}

// claimedByOther finds a claim on declaration d of file in a live row but id.
func claimedByOther(rows []job.Job, id, file, d string) (owner, claim string, taken bool) {
	for _, r := range rows {
		if r.ID == id || !r.State.Live() {
			continue
		}
		for _, entry := range r.HardPaths() {
			if p, c := job.SplitClaim(entry); c != "" && path.Clean(p) == file && job.NamesDeclaration(c, d) {
				return r.ID, c, true
			}
		}
	}
	return "", "", false
}

// liveHolders are the live rows other than id whose write paths cover file, a pattern that
// released it excepted.
func liveHolders(rows []job.Job, id, file string) []job.Job {
	var out []job.Job
	for _, r := range rows {
		if r.ID == id || !Editing(r) {
			continue
		}
		if slices.ContainsFunc(r.HardPaths(), func(entry string) bool {
			return job.Covers(entry, file) && !job.ReleasedReach(r, entry, file)
		}) {
			out = append(out, r)
		}
	}
	return out
}

// dry is s as a dry run reads it: the same file and seams, with the writes a dry run must
// not make (the sweep's, and a refusal's release request) skipped.
func (s *Store) dry() *Store {
	v := s.view()
	v.dryRun = true
	return v
}

// requestRelease asks holder to release what a refused fork wanted of file.
// It lands on the holder's row, which the guard reads without a store fold,
// and in the attention queue for a person. A dry-run store files nothing.
func (s *Store) requestRelease(ctx context.Context, holder, file string, wanted, unused []string) {
	if s.dryRun {
		return
	}
	files := make([]types.FileRef, len(wanted))
	for i, w := range wanted {
		files[i] = types.FileRef{Value: w}
	}
	open := sessions.AttentionOpen{
		Outcome:  string(types.OutcomeWaiting),
		Severity: string(types.SeverityWarning),
		Source:   releaseRequestSource,
		Where:    file,
		Lease:    s.Actor().Lease,
		Files:    files,
		Message: fmt.Sprintf("job %s holds %s, and a fork that needs part of it was refused (its claims are this request's files)."+
			" %s. %s narrows to what it edits (`%s#<declaration>`, or `%s` without the unused paths) or exits; then `%s` closes this request",
			holder, file, describeUnused(holder, unused), holder, file, hint.JobApply.With("-f", "<record>"), hint.SessionDispose.With("<request>")),
	}
	filed, err := s.FileReleaseRequest(ctx, holder, job.ReleaseRequest{
		ID: sessions.RequestID(holder, open), File: file, Wanted: wanted, By: open.Lease, Message: open.Message,
	})
	if err != nil || !filed {
		return
	}
	dir, err := sessions.Dir(s.root)
	if err != nil {
		return
	}
	// The row just answered that this request is new.
	_ = sessions.AppendRequest(ctx, dir, holder, open, session.InvocationStart{Origin: trail.LocalOrigin(ctx), Workspace: s.root})
}

// FileReleaseRequest puts req on holder's row and stamps its time. It answers
// false when the row already carries req.ID, which leaves the row unchanged.
func (s *Store) FileReleaseRequest(ctx context.Context, holder string, req job.ReleaseRequest) (bool, error) {
	filed := false
	_, err := s.Mutate(ctx, holder, job.AsObservation, func(cur *job.Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("%q: %w", holder, job.ErrUnknownJob)
		}
		if slices.ContainsFunc(cur.ReleaseRequests, func(r job.ReleaseRequest) bool { return r.ID == req.ID }) {
			return nil
		}
		req.At = now
		cur.ReleaseRequests = append(cur.ReleaseRequests, req)
		filed = true
		return nil
	})
	return filed, err
}

// CloseReleaseRequest drops request id from the row carrying it, if any does.
func (s *Store) CloseReleaseRequest(ctx context.Context, id string) error {
	f, err := s.Read(ctx)
	if err != nil {
		return err
	}
	matches := func(r job.ReleaseRequest) bool { return r.ID == id }
	for _, row := range f.Jobs {
		if !slices.ContainsFunc(row.ReleaseRequests, matches) {
			continue
		}
		_, err := s.Mutate(ctx, row.ID, job.AsObservation, func(cur *job.Job, _ bool, _ int64) error {
			cur.ReleaseRequests = slices.DeleteFunc(cur.ReleaseRequests, matches)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ReleaseRequestDisposed clears req off its holder's row in the repository at
// root once a person disposed it. Any other kind of request is left alone.
func ReleaseRequestDisposed(ctx context.Context, root string, req sessions.AttentionRequest) error {
	if req.Source != releaseRequestSource {
		return nil
	}
	base, err := xdg.StateDir()
	if err != nil {
		return fmt.Errorf("job: resolve the user state dir: %w", err)
	}
	return NewStore(Location{Root: root, StateBase: base}).CloseReleaseRequest(ctx, req.ID)
}

// claimTouched answers the `file#<word>` claims cur lacks for the declarations
// in touched, refusing one that another live row claims.
func claimTouched(cur job.Job, rows []job.Job, id, file string, touched, all []string) ([]string, error) {
	var added []string
	held := job.ClaimsOnFile(cur, file)
	for _, d := range touched {
		d = strings.TrimSpace(d)
		if d == "" || d == job.Preamble || slices.ContainsFunc(held, func(c string) bool { return job.NamesDeclaration(c, d) }) {
			continue
		}
		if owner, claim, taken := claimedByOther(rows, id, file, d); taken {
			return nil, fmt.Errorf("job: %s#%s is lease %s's claim, so %s cannot add %s", file, claim, owner, id, d)
		}
		word := claimWord(d, all)
		held = append(held, word)
		added = append(added, fmt.Sprintf("%s#%s", file, word))
	}
	return added, nil
}

// recordNarrowing stores the narrowing release of file on cur, keeping who took it.
func recordNarrowing(cur *job.Job, file, takenBy string, now int64, digest string) {
	rel := job.Release{Path: file, Digest: digest, ReleasedAt: now, Narrowed: true, TakenBy: takenBy}
	if at := job.FileRelease(*cur, file); at >= 0 {
		rel.TakenBy = cmp.Or(takenBy, cur.Releases[at].TakenBy)
		cur.Releases[at] = rel
		return
	}
	cur.Releases = append(cur.Releases, rel)
}
