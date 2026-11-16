package settle

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/risk"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// ByClaims settles the content conflicts that jobs on the two sides of a merge of
// against into ours reached through disjoint declaration claims, and returns the conflicts
// still standing. A path settles only when every condition holds:
//   - a project's merge_low_risk covers it, and a diff driver places its declarations;
//   - the VCS reports one merge base for the two sides;
//   - each side carries the work of a job that writes the path, and every such job is
//     recorded pass by `magus job wait`;
//   - each of those jobs writes it by declaration claims alone, and the sides' claims are
//     disjoint;
//   - risk.MergeByClaims settles every region the two sides' edits meet in.
//
// A path some job claims by declaration gets one line saying it settled or which condition
// failed. A path no job claims is passed over in silence, since claims say nothing about it.
func ByClaims(ctx context.Context, out io.Writer, m *magus.Magus, drv types.VCSDriver, jobs func() ([]job.Job, error), ours, against string, conflicts []types.Conflict) ([]types.Conflict, error) {
	root := m.Root()
	rows, err := jobs()
	if err != nil {
		fmt.Fprintf(out, "vcs resolve: no conflict settles by job claims: the job store: %v\n", err)
		return conflicts, nil
	}
	s := claimSettle{m: m, drv: drv, ours: ours, theirs: against}
	var left []types.Conflict
	for _, c := range conflicts {
		writers, claimed := jobsWriting(rows, c.Path)
		if c.Kind != types.ConflictKindContent || !claimed || m.FindOutputProducer(filepath.Join(root, filepath.FromSlash(c.Path))) != nil {
			left = append(left, c)
			continue
		}
		merged, how, err := s.settle(ctx, c.Path, writers)
		var why unsettledError
		if errors.As(err, &why) {
			fmt.Fprintf(out, "vcs resolve: %s not settled by job claims: %s\n", c.Path, why)
			left = append(left, c)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("vcs resolve: %s: %w", c.Path, err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(c.Path)), merged, 0o644); err != nil {
			return nil, fmt.Errorf("vcs resolve: %w", err)
		}
		if err := drv.MarkResolved(ctx, root, []string{c.Path}); err != nil {
			return nil, fmt.Errorf("vcs resolve: %w", err)
		}
		fmt.Fprintf(out, "vcs resolve: settled %s by job claims: %s\n", c.Path, how)
	}
	if len(left) == 0 {
		fmt.Fprintln(out, "vcs resolve: every conflict is resolved")
	}
	return left, nil
}

// jobsWriting returns the rows with a write path on p, and whether any of them claims p by
// declaration.
func jobsWriting(rows []job.Job, p string) (writers []job.Job, claimed bool) {
	for _, row := range rows {
		writes := false
		for _, e := range row.Paths.Write {
			if job.PathsIntersect(e, p) {
				writes = true
				claimed = claimed || job.HasClaim(e)
			}
		}
		if writes {
			writers = append(writers, row)
		}
	}
	return writers, claimed
}

// claimSettle decides paths for ByClaims. ours and theirs are the two revisions the
// merge joins; the merge base between them is looked up once.
type claimSettle struct {
	m            *magus.Magus
	drv          types.VCSDriver
	ours, theirs string

	looked  bool
	base    string
	baseErr error
}

// unsettledError is the first condition that kept a path from settling by job claims,
// as opposed to a failure to evaluate them.
type unsettledError string

func (e unsettledError) Error() string { return string(e) }

func unsettledf(format string, args ...any) error {
	return unsettledError(fmt.Sprintf(format, args...))
}

// mergeSide is one of the two revisions a merge joins; it indexes the per-side arrays of
// settle.
type mergeSide uint8

const (
	oursSide mergeSide = iota
	theirsSide
)

// settle is the merge of p, or an unsettledError naming the first condition that failed.
// how names the claims that settled it.
func (s *claimSettle) settle(ctx context.Context, p string, writers []job.Job) (merged []byte, how string, err error) {
	if _, _, ok := risk.MatchScopes(risk.MergeScopes(s.m.All()), p); !ok {
		return nil, "", unsettledf("no project's merge_low_risk covers it")
	}
	if _, ok := types.MatchDiffDriver(p); !ok {
		return nil, "", unsettledf("no diff driver places its declarations")
	}
	base, err := s.mergeBase(ctx)
	if err != nil {
		return nil, "", err
	}
	var jobs [2][]job.Job
	for _, row := range writers {
		if side, ok := s.resolveSide(ctx, row, base); ok {
			jobs[side] = append(jobs[side], row)
		}
	}
	names := [2]string{oursSide: "ours", theirsSide: s.theirs}
	var claims, cited [2][]string
	for side, rows := range jobs {
		if len(rows) == 0 {
			return nil, "", unsettledf("no job whose work %s carries writes it", names[side])
		}
		for _, row := range rows {
			if row.State != job.StatePass {
				return nil, "", unsettledf("job %s is %s, not recorded pass by `magus job wait`", row.ID, cmp.Or(row.State, job.StateDeclared))
			}
			for _, e := range row.Paths.Write {
				if !job.PathsIntersect(e, p) {
					continue
				}
				_, decl := job.SplitClaim(e)
				if decl == "" {
					return nil, "", unsettledf("job %s writes it whole (%s), not by declaration", row.ID, e)
				}
				claims[side] = append(claims[side], decl)
				cited[side] = append(cited[side], fmt.Sprintf("%s (job %s)", e, row.ID))
			}
		}
	}
	if a, b, ok := risk.OverlappingClaims(claims[0], claims[1]); ok {
		return nil, "", unsettledf("the claims overlap: %s#%s on %s, %s#%s on %s", p, a, names[oursSide], p, b, names[theirsSide])
	}
	var content [3][]byte
	for i, rev := range []string{base, s.ours, s.theirs} {
		text, err := s.drv.ReadFileAt(ctx, s.m.Root(), rev, p)
		if err != nil {
			return nil, "", unsettledf("it cannot be read at %s: %v", rev, err)
		}
		content[i] = []byte(text)
	}
	res, err := risk.MergeByClaims(p, content[0],
		risk.ClaimSide{Content: content[1], Claims: claims[oursSide]},
		risk.ClaimSide{Content: content[2], Claims: claims[theirsSide]})
	if err != nil {
		return nil, "", unsettledError(err.Error())
	}
	return res.Content, fmt.Sprintf("%s against %s; every job passed", strings.Join(cited[0], ", "), strings.Join(cited[1], ", ")), nil
}

// mergeBase is the one merge base of the two sides, an unsettledError when there is not one.
func (s *claimSettle) mergeBase(ctx context.Context) (string, error) {
	if !s.looked {
		s.looked = true
		var ok bool
		s.base, ok, s.baseErr = s.drv.MergeBase(ctx, s.m.Root(), s.ours, s.theirs)
		switch {
		case errors.Is(s.baseErr, types.ErrVCSUnsupported):
			s.baseErr = unsettledf("%s reports no merge base, and each side's edits are measured from one", s.drv.Name())
		case s.baseErr == nil && !ok:
			s.baseErr = unsettledf("the sides share no one merge base")
		}
	}
	return s.base, s.baseErr
}

// resolveSide is the side whose history carries row's work, or false for neither: the work is
// already in the merge base, or no revision here names it. The work is the revision row's
// checkout has checked out, else a revision named row.ID: the branch-per-job convention
// hack/dev/merge-job-branches.buzz also reads.
func (s *claimSettle) resolveSide(ctx context.Context, row job.Job, base string) (mergeSide, bool) {
	tip := row.ID
	if info, err := os.Stat(row.CheckoutRoot); err == nil && info.IsDir() {
		if ref, err := s.drv.Ref(ctx, row.CheckoutRoot); err == nil && ref != "" {
			tip = ref
		}
	}
	root := s.m.Root()
	if in, err := s.drv.IsAncestor(ctx, root, tip, base); err != nil || in {
		return 0, false
	}
	for side, rev := range []string{s.ours, s.theirs} {
		if in, err := s.drv.IsAncestor(ctx, root, tip, rev); err == nil && in {
			return mergeSide(side), true
		}
	}
	return 0, false
}
