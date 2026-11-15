package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
	"github.com/egladman/magus/vcs"
)

// ComposeModelTierMatrix asks the workspace matrix, then the person's on a nil.
// An error from either stands. A nil user matrix is the workspace's alone.
func ComposeModelTierMatrix(workspace, user types.ModelTierMatrix) types.ModelTierMatrix {
	if user == nil {
		return workspace
	}
	return func(ctx context.Context, req types.ModelTierRequest) (*typesagent.JobModelTier, error) {
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

// SplitSeq rather than Split: the intermediate slice is never used for anything but this
// loop, and this file already had three copies of the same split before they were folded
// back into this one.
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
func FlagDeclaration(id string, f flaggen.JobForkFlags, models func(string) typesagent.Model) (types.Declaration, error) {
	var d types.Declaration
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
			return types.Declaration{}, err
		}
	}
	d.Schema = types.Schema{Version: types.JobAPIVersion}
	d.ID = id
	d.Parent, d.Checkpoint, d.Checkout = f.Parent, f.Checkpoint, f.Checkout
	d.BriefFormat, d.Delivery, d.ReadOnly = types.BriefFormat(f.BriefFormat), f.Delivery, f.ReadOnly
	if f.Timeout != 0 {
		d.Timeout = f.Timeout.String()
	}
	if f.Model != "" || f.Instructions != "" || f.NewWorker != "" {
		d.Agent = &typesagent.Spec{Instructions: f.Instructions, Worker: typesagent.Worker{Type: typesagent.WorkerTypeResumed}}
		if f.Model != "" {
			d.Agent.Model = models(f.Model)
		}
		if f.NewWorker != "" {
			d.Agent.Worker = typesagent.Worker{Type: typesagent.WorkerTypeNew, Reason: f.NewWorker}
		}
	}
	if strings.TrimSpace(f.Check) == "" {
		return d, nil
	}
	parsed, err := types.ParseLeaseCheck(f.Check)
	if err != nil {
		parsed = types.LeaseCheck{Target: f.Check}
	}
	d.Check = &parsed
	return d, nil
}

// CheckpointNames reports whether checkpoint's revision is revision, a full
// id, or an abbreviation of it.
func CheckpointNames(checkpoint, revision string) bool {
	rev, _ := types.ParseCheckpointToken(checkpoint)
	return rev != "" && strings.HasPrefix(revision, rev)
}

// StackedHead fills a stacked row's parent_head with this checkout's
// branch. A checkout on no branch has no head to stack on.
func StackedHead(ctx context.Context, root string, row types.Declaration, opts types.VCSOptions) (types.Declaration, error) {
	if row.Delivery != types.DeliveryStacked || row.ParentHead != "" {
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
func RetriedDeclaration(ctx context.Context, store *Store, row types.Declaration) (types.Declaration, error) {
	rows, err := store.List()
	if err != nil {
		return row, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == row.ID })
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
func WidenedDeclaration(store *Store, id string, add []string) (types.Declaration, error) {
	rows, err := store.List()
	if err != nil {
		return types.Declaration{}, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return types.Declaration{}, exit.Usagef("no job %s to widen; `%s` lists them", id, hint.LsJobs)
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

// NarrowedDeclaration is rec without the paths in release; goals stay.
// A path the job does not hold is refused, so a typo is never a no-op.
func NarrowedDeclaration(rec types.Declaration, id string, release []string) (types.Declaration, error) {
	rec.Paths.Write = slices.Clone(rec.Paths.Write)
	for _, p := range release {
		i := slices.Index(rec.Paths.Write, p)
		if i < 0 {
			return types.Declaration{}, exit.Usagef("%s holds no write path %q to release", id, p)
		}
		rec.Paths.Write = slices.Delete(rec.Paths.Write, i, i+1)
	}
	return rec, nil
}
