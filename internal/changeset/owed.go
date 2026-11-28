package changeset

import (
	"context"
	"fmt"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// OwedVerdict is what a branch tip owes a regeneration, and the text a refusal at push
// or job exit prints for it.
type OwedVerdict struct {
	Owed vcs.RangeOwed
	// Notice names the owing projects and ends with Fold, "" when nothing is owed.
	Notice string
	// Fold is the step after regenerating, in the backend's own commands. It folds the
	// outputs into Owed.Last while that is unpushed, and prescribes a follow-up otherwise.
	Fold string
}

// RegenerationOwed judges the checkout at root's tip against base by vcs.RangeOwes.
// A range the backend cannot read is an error, so a caller refuses on unknown rather
// than pass the tip as owing nothing.
func RegenerationOwed(ctx context.Context, root string, driver types.VCSDriver, base string, classify func(context.Context, []string) ([]types.FileEntry, error), generates func(project string) bool) (OwedVerdict, error) {
	tip, err := driver.Metadata(ctx, root)
	if err != nil {
		return OwedVerdict{}, fmt.Errorf("read the tip of %s: %w", root, err)
	}
	owed, err := vcs.RangeOwes(ctx, driver, root, base, tip.ID, classify, generates)
	if err != nil || len(owed.Projects) == 0 {
		return OwedVerdict{Owed: owed}, err
	}
	kase := foldCase(ctx, root, driver, owed.Last, func() bool { return owed.Last == tip.ID })
	subject := fmt.Sprintf("the range %s..%s", base, tip.ID)
	return OwedVerdict{
		Owed:   owed,
		Notice: buildDriftNotice(driver, subject, owed.Last, owed.Projects, kase),
		Fold:   foldLine(driver, owed.Last, kase),
	}, nil
}
