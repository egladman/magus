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
	"github.com/egladman/magus/types"
)

// releaseSource is the attention source of a request asking a job to release part of a
// project's configuration that a refused fork needed.
const releaseSource = "magus/job-release"

// projectConfig reports whether rel is a project's configuration this workspace loads: a
// magusfile source or a magus.yaml among describe.WorkspaceLoadFiles, the set
// RefuseSharedCheckout guards. The spell sources a magusfile imports are left out: they
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
		return types.Job{}, fmt.Errorf("job: no claim on %s can be graded, so its hold stays whole: %w", file, err)
	}
	var all []string
	if drivers[file] != "" {
		all, _ = declarationsOf(ctx, driver, store.root, file)
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
			cur.WritePaths = append(cur.WritePaths, file+"#"+word)
		}
		// Store.releases records what a write gave up; here that is file itself, which the
		// pattern held whole until now.
		gaveUp := types.Job{WritePaths: append(slices.Clone(cur.WritePaths), file), Releases: cur.Releases}
		cur.Releases = store.releases(ctx, gaveUp, *cur, now, false)
		return nil
	})
}

// holdersOf are the live rows other than id whose write paths cover file, a pattern that
// released it excepted.
func holdersOf(rows []types.Job, id, file string) []types.Job {
	var out []types.Job
	for _, r := range rows {
		if r.ID == id || !Editing(r) {
			continue
		}
		if slices.ContainsFunc(r.WritePaths, func(entry string) bool {
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
	return &Store{
		path: s.path, err: s.err, root: s.root, actor: s.actor, cacheDir: s.cacheDir,
		clock: s.clock, staleAfter: s.staleAfter, notices: s.notices, retain: s.retain, outputs: s.outputs,
		landed: s.landed, grader: s.grader, dryRun: true,
	}
}

// requestRelease files one attention request (outcome waiting) asking holder to release
// what it does not edit of file, which a refused fork wanted as the claims in wanted. The
// request is keyed on holder and file alone (see [sessions.RequestID]), so every refusal over
// the pair lands on one row until the holder or the root disposes of it; the claims ride
// along as its files. A dry-run store files nothing, and neither does one that will not
// take the request: the refusal is the answer either way.
func (s *Store) requestRelease(ctx context.Context, holder, file string, wanted []string) {
	if s.dryRun {
		return
	}
	dir, err := sessions.Dir(s.root)
	if err != nil {
		return
	}
	files := make([]types.FileRef, len(wanted))
	for i, w := range wanted {
		files[i] = types.FileRef{Value: w}
	}
	_, _, _ = sessions.OpenRequest(dir, holder, sessions.AttentionOpen{
		Outcome:  string(types.OutcomeWaiting),
		Severity: string(types.SeverityWarning),
		Source:   releaseSource,
		Where:    file,
		Lease:    s.Actor().Lease,
		Files:    files,
		Message: fmt.Sprintf("job %s holds %s, and a fork that needs part of it was refused (its claims are this request's files)."+
			" %s narrows to the declarations it edits (`%s#<declaration>`) or exits; then `%s` closes this request",
			holder, file, holder, file, hint.SessionDispose.With("<request>")),
	}, sessions.InvocationStart{Origin: trail.LocalOrigin(ctx), Workspace: s.root})
}
