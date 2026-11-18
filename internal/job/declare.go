package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// ComposeModelTierMatrix asks the workspace matrix, then the person's on a nil.
// An error from either stands. A nil user matrix is the workspace's alone.
func ComposeModelTierMatrix(workspace, user types.ModelTierMatrix) types.ModelTierMatrix {
	if user == nil {
		return workspace
	}
	return func(ctx context.Context, req types.ModelTierRequest) (*tier.Assignment, error) {
		if workspace != nil {
			choice, err := workspace(ctx, req)
			if err != nil || choice != nil {
				return choice, err
			}
		}
		return user(ctx, req)
	}
}

// ListFlag accumulates one repeatable, comma-separated flag, on the same rule --skip
// follows (cmd/magus/run.go): an empty segment is refused rather than dropped, so a
// trailing comma cannot silently shrink a lease's write paths.
//
// Not named for paths: --depends-on holds job ids and the gate flags hold symbols, and a
// type called pathList holding neither is a name that has to be read past.
type ListFlag []string

func (l *ListFlag) String() string { return strings.Join(*l, ",") }

// Set appends each comma-separated entry of value and refuses an empty one. SplitSeq
// rather than Split: the intermediate slice is never used for anything but this loop.
func (l *ListFlag) Set(value string) error {
	for part := range strings.SplitSeq(value, ",") {
		if part = strings.TrimSpace(part); part == "" {
			return errors.New("empty entry")
		}
		*l = append(*l, part)
	}
	return nil
}

// claimListFlag is a path flag whose value may be a declaration claim.
// A value holding # is one path: a claim runs to the end of its value.
type claimListFlag []string

func (l *claimListFlag) String() string { return strings.Join(*l, ",") }

func (l *claimListFlag) Set(value string) error {
	if !strings.Contains(value, "#") {
		return (*ListFlag)(l).Set(value)
	}
	if value = strings.TrimSpace(value); value == "" {
		return errors.New("empty entry")
	}
	*l = append(*l, value)
	return nil
}

// ClaimPaths splits each value of the path flag name as [claimListFlag.Set]
// does, so a declaration claim stays one path.
func ClaimPaths(name string, values []string) ([]string, error) {
	var paths claimListFlag
	for _, v := range values {
		if err := paths.Set(v); err != nil {
			return nil, exit.Usagef("magus job apply: --%s %q: %s", name, v, err)
		}
	}
	return paths, nil
}

// listValues splits each value of the list flag name as [ListFlag.Set] does.
func listValues(command, name string, values []string) ([]string, error) {
	var l ListFlag
	for _, v := range values {
		if err := l.Set(v); err != nil {
			return nil, exit.Usagef("%s: --%s %q: %s", command, name, v, err)
		}
	}
	return l, nil
}

// FlagDeclaration is the record for id from the bound `job fork` flags.
// An unparsable check is carried through so Validate names the rule,
// rather than the row going silently checkless. An agent flag writes the
// agent section, and models reads the --model word.
func FlagDeclaration(id string, f flaggen.JobForkFlags, models func(string) job.Model) (job.Declaration, error) {
	var d job.Declaration
	for _, l := range []struct {
		name string
		from []string
		to   *[]string
	}{
		{flaggen.FlagJobForkWritePaths, f.WritePaths, &d.Paths.Write},
		{flaggen.FlagJobForkDenyPaths, f.DenyPaths, &d.Paths.Deny},
		{flaggen.FlagJobForkReadPaths, f.ReadPaths, &d.Paths.Read},
		{flaggen.FlagJobForkDependsOn, f.DependsOn, &d.DependsOn},
	} {
		var err error
		if *l.to, err = listValues("magus job fork", l.name, l.from); err != nil {
			return job.Declaration{}, err
		}
	}
	d.Schema = job.Schema{Version: job.APIVersion}
	d.ID = id
	d.Parent, d.Checkpoint, d.Checkout = f.Parent, f.Checkpoint, f.Checkout
	d.BriefFormat, d.Delivery, d.ReadOnly = job.BriefFormat(f.BriefFormat), f.Delivery, f.ReadOnly
	if f.Timeout != 0 {
		d.Timeout = f.Timeout.String()
	}
	if f.Model != "" || f.Instructions != "" || f.NewWorker != "" {
		d.Agent = &job.Agent{Instructions: f.Instructions, Worker: job.Worker{Type: job.WorkerTypeResumed}}
		if f.Model != "" {
			d.Agent.Model = models(f.Model)
		}
		if f.NewWorker != "" {
			d.Agent.Worker = job.Worker{Type: job.WorkerTypeNew, Reason: f.NewWorker}
		}
	}
	if strings.TrimSpace(f.Check) == "" {
		return d, nil
	}
	parsed, err := parseLeaseCheck(f.Check)
	if err != nil {
		parsed = types.LeaseCheck{Target: f.Check}
	}
	raw, err := types.EncodeLeaseCheck(parsed)
	if err != nil {
		return job.Declaration{}, err
	}
	d.Check = raw
	return d, nil
}

// resolveAccept is postconditions with each check postcondition that names no basis accepting a witnessed
// run, the run magus's own `magus run` of the check records, and the ids of the postconditions it
// defaulted so. The module refuses a check postcondition accepting nothing, so a declaration magus
// reads resolves it before it is validated, as the module resolves a postcondition's expect; a
// postcondition naming its bases keeps them.
func resolveAccept(postconditions []job.Postcondition) ([]job.Postcondition, []string) {
	if postconditions == nil {
		return nil, nil
	}
	out := slices.Clone(postconditions)
	var defaulted []string
	for i, g := range out {
		if len(g.Accept) == 0 && g.Resolve().Kind == job.PostconditionKindCheck {
			out[i].Accept = []job.Basis{job.BasisWitnessed}
			defaulted = append(defaulted, g.ID)
		}
	}
	return out, defaulted
}

// DefaultedAccept is a check postcondition a declaration named no bases for, which a fork resolves
// to accept a witnessed run.
type DefaultedAccept struct{ Job, Postcondition string }

func (d DefaultedAccept) String() string {
	return fmt.Sprintf("%s: check postcondition %q names no accept, so it defaulted to accept [%s]", d.Job, d.Postcondition, job.BasisWitnessed)
}

func defaultedAccepts(id string, postconditions []string) []DefaultedAccept {
	out := make([]DefaultedAccept, 0, len(postconditions))
	for _, g := range postconditions {
		out = append(out, DefaultedAccept{Job: id, Postcondition: g})
	}
	return out
}

// CheckpointNames reports whether checkpoint's revision is revision, a full
// id, or an abbreviation of it.
func CheckpointNames(checkpoint, revision string) bool {
	rev, _ := types.ParseCheckpointToken(checkpoint)
	return rev != "" && strings.HasPrefix(revision, rev)
}

// StackedHead fills a stacked row's parent_head with this checkout's
// branch. A checkout on no branch has no head to stack on.
func StackedHead(ctx context.Context, root string, row job.Declaration, opts types.VCSOptions) (job.Declaration, error) {
	if row.Delivery != job.DeliveryStacked || row.ParentHead != "" {
		return row, nil
	}
	refuse := func(why error) error {
		return fmt.Errorf("%s is stacked with no pull request base, so name parent_head in the record: %w", row.ID, why)
	}
	if root == "" {
		return row, refuse(errors.New("no workspace was found"))
	}
	res, err := vcs.Resolve(ctx, root, "", opts)
	if err != nil {
		return row, refuse(err)
	}
	meta, err := res.VCS.Metadata(ctx, root)
	switch {
	case err != nil:
		return row, refuse(err)
	case meta.Ref == "":
		return row, refuse(fmt.Errorf("%s is on no branch", root))
	}
	slog.InfoContext(ctx, fmt.Sprintf("%s is stacked on %s, the branch this checkout is on", row.ID, meta.Ref), attr.Notice("note"))
	row.ParentHead = meta.Ref
	return row, nil
}

// RetriedDeclaration is the stored spec of the job row names, for a fork naming its id and no
// spec flag, with row's checkout when --checkout gave one; row as it is when no job has that id.
func RetriedDeclaration(ctx context.Context, store *Store, row job.Declaration) (job.Declaration, error) {
	rows, err := store.List(ctx)
	if err != nil {
		return row, err
	}
	i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == row.ID })
	if i < 0 {
		return row, nil
	}
	retried := RetryDeclaration(rows[i])
	retried.Checkout = row.Checkout
	slog.InfoContext(ctx, fmt.Sprintf("the fork names %s and no spec flag, so it forks %s again with the spec it was declared with", row.ID, row.ID), attr.Notice("note"))
	return retried, nil
}

// WidenedDeclaration is the job's own spec plus the paths it lacks, so a
// widening is validated as a whole record is.
func WidenedDeclaration(ctx context.Context, store *Store, id string, add []string) (job.Declaration, error) {
	rows, err := store.List(ctx)
	if err != nil {
		return job.Declaration{}, err
	}
	i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == id })
	if i < 0 {
		return job.Declaration{}, exit.Usagef("no job %s to widen; `%s` lists them", id, hint.LsJobs)
	}
	rec := RetryDeclaration(rows[i])
	rec.Paths.Write = slices.Clone(rec.Paths.Write)
	for _, p := range add {
		if !slices.Contains(rec.Paths.Write, p) {
			rec.Paths.Write = append(rec.Paths.Write, p)
		}
	}
	return rec, nil
}

// NarrowedDeclaration is rec without the paths in release; postconditions stay.
// A path the job does not hold is refused, so a typo is never a no-op.
func NarrowedDeclaration(rec job.Declaration, id string, release []string) (job.Declaration, error) {
	rec.Paths.Write = slices.Clone(rec.Paths.Write)
	for _, p := range release {
		i := slices.Index(rec.Paths.Write, p)
		if i < 0 {
			return job.Declaration{}, exit.Usagef("%s holds no write path %q to release", id, p)
		}
		rec.Paths.Write = slices.Delete(rec.Paths.Write, i, i+1)
	}
	return rec, nil
}

// ParsePauseUntil reads a pause's --until as the unix second it lapses: a duration from now
// or an RFC3339 instant. Empty is zero, a pause with no bound.
func ParsePauseUntil(raw string, now time.Time) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0, &pauseUntilError{Until: raw, Reason: "is not ahead of now, as a positive duration (2h) or a later instant is"}
		}
		return now.Add(d).Unix(), nil
	}
	if at, err := time.Parse(time.RFC3339, raw); err == nil {
		return at.Unix(), nil
	}
	return 0, &pauseUntilError{Until: raw, Reason: "is neither a duration such as 2h nor an RFC3339 instant such as 2006-01-02T15:04:05Z"}
}

// pauseUntilError is a pause bound [ParsePauseUntil] refuses, and why.
type pauseUntilError struct {
	Until  string
	Reason string
}

func (e *pauseUntilError) Error() string { return fmt.Sprintf("--until %q %s", e.Until, e.Reason) }
