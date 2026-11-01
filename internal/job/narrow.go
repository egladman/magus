package job

import (
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

// releasedFile reports whether row released file whole, which ends the hold any pattern
// of its write paths had on it (see [NarrowConfigHold]).
func releasedFile(row types.Job, file string) bool {
	file = path.Clean(file)
	return slices.ContainsFunc(row.Releases, func(r types.JobRelease) bool {
		p, decl := types.SplitClaim(r.Path)
		return decl == "" && path.Clean(p) == file
	})
}

// releasedReach reports whether entry, a pattern among row's write paths, reaches the file
// other names only after row released that file.
func releasedReach(row types.Job, entry, other string) bool {
	if types.HasClaim(entry) || !strings.ContainsAny(entry, globMeta) {
		return false
	}
	p, _ := types.SplitClaim(other)
	p = path.Clean(strings.TrimSpace(p))
	return !strings.ContainsAny(p, globMeta) && releasedFile(row, p)
}

// heldByPattern reports whether a write path of row with glob metacharacters covers file.
func heldByPattern(row types.Job, file string) bool {
	return slices.ContainsFunc(row.WritePaths, func(entry string) bool {
		p, decl := types.SplitClaim(entry)
		return decl == "" && strings.ContainsAny(p, globMeta) && coversPath(p, file)
	})
}

// claimsOnFile are the declarations row's write paths claim in file.
func claimsOnFile(row types.Job, file string) []string {
	var out []string
	for _, entry := range row.WritePaths {
		if p, decl := types.SplitClaim(entry); decl != "" && path.Clean(p) == file {
			out = append(out, decl)
		}
	}
	return out
}

// NarrowConfigHold rewrites live job id, which holds file (a project's magusfile or
// magus.yaml) through a pattern such as `**/*.buzz`, so it holds `file#<claim>` for each declaration in touched
// it does not claim yet, and records file itself as released with its digest. From then
// on the pattern no longer holds file for id ([ClaimedDeclarations], WriteProof): another
// job may claim the declarations id never touched, and `magus job wait` grades id's
// changes to file against its claims.
//
// touched are declarations as the footprint places them (a declaration line, or a key path
// such as `project.targets.test` or `jobs.stale_after`); each is claimed by the shortest word naming it alone in
// the file. The preamble is never claimed. Calling it again adds the declarations not yet
// claimed and refreshes the release's digest.
//
// The write is the store's own, so it is not held to the holder's boundary: the claims sit
// inside what the pattern already covered. Refused when file is not a project's
// configuration, when version control cannot place its lines, when id is not live, or when
// no pattern of id covers file.
func NarrowConfigHold(ctx context.Context, store *Store, id, file string, touched []string) (types.Job, error) {
	if store == nil || store.root == "" {
		return types.Job{}, errors.New("job: no workspace to narrow a hold in")
	}
	file = path.Clean(strings.TrimSpace(file))
	if !projectConfig(store.root, file) {
		return types.Job{}, fmt.Errorf("job: %s is not a project's configuration, so no hold on it narrows", file)
	}
	driver, drivers, err := claimDrivers(ctx, store.root, []string{file})
	if err != nil {
		return types.Job{}, fmt.Errorf("no claim on %s can be graded, so its hold stays whole: %w", file, err)
	}
	var all []string
	if drivers[file] != "" {
		all, _ = readDeclarations(ctx, driver, store.root, file)
	}
	return store.mutate(ctx, id, asNarrowing, func(cur *types.Job, exists bool, now int64) error {
		switch {
		case !exists:
			return fmt.Errorf("%q: %w", id, ErrUnknownJob)
		case !Editing(*cur):
			return fmt.Errorf("job: %s is %s and holds nothing to narrow", id, cur.State)
		case !heldByPattern(*cur, file):
			return fmt.Errorf("job: no pattern of %s covers %s, so there is no hold on it to narrow", id, file)
		}
		held := claimsOnFile(*cur, file)
		for _, d := range touched {
			d = strings.TrimSpace(d)
			if d == "" || d == types.Preamble || slices.ContainsFunc(held, func(c string) bool { return types.NamesDeclaration(c, d) }) {
				continue
			}
			word := claimWord(d, all)
			held = append(held, word)
			cur.WritePaths = append(cur.WritePaths, fmt.Sprintf("%s#%s", file, word))
		}
		// Store.releases records what a write gave up; here that is file itself, which the
		// pattern held whole until now.
		gaveUp := types.Job{WritePaths: append(slices.Clone(cur.WritePaths), file), Releases: cur.Releases}
		cur.Releases = store.releases(ctx, gaveUp, *cur, now, false)
		return nil
	})
}

// liveHolders are the live rows other than id whose write paths cover file, a pattern that
// released it excepted.
func liveHolders(rows []types.Job, id, file string) []types.Job {
	var out []types.Job
	for _, r := range rows {
		if r.ID == id || !Editing(r) {
			continue
		}
		if slices.ContainsFunc(r.HardPaths(), func(entry string) bool {
			return covers(entry, file) && !releasedReach(r, entry, file)
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
	filed, err := s.FileReleaseRequest(ctx, holder, types.JobReleaseRequest{
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
	_ = sessions.AppendRequest(dir, holder, open, sessions.InvocationStart{Origin: trail.LocalOrigin(ctx), Workspace: s.root})
}

// FileReleaseRequest puts req on holder's row and stamps its time. It answers
// false when the row already carries req.ID, which leaves the row unchanged.
func (s *Store) FileReleaseRequest(ctx context.Context, holder string, req types.JobReleaseRequest) (bool, error) {
	filed := false
	_, err := s.mutate(ctx, holder, asObservation, func(cur *types.Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("%q: %w", holder, ErrUnknownJob)
		}
		if slices.ContainsFunc(cur.ReleaseRequests, func(r types.JobReleaseRequest) bool { return r.ID == req.ID }) {
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
	f, err := s.read()
	if err != nil {
		return err
	}
	matches := func(r types.JobReleaseRequest) bool { return r.ID == id }
	for _, row := range f.Jobs {
		if !slices.ContainsFunc(row.ReleaseRequests, matches) {
			continue
		}
		_, err := s.mutate(ctx, row.ID, asObservation, func(cur *types.Job, _ bool, _ int64) error {
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

// heldRequests are row's release requests for files it still holds. Narrowing
// past a file answers the request for it.
func heldRequests(row types.Job) []types.JobReleaseRequest {
	return slices.DeleteFunc(row.ReleaseRequests, func(r types.JobReleaseRequest) bool {
		return !slices.ContainsFunc(row.HardPaths(), func(entry string) bool {
			return covers(entry, r.File) && !releasedReach(row, entry, r.File)
		})
	})
}
