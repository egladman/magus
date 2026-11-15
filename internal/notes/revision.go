package notes

import (
	"context"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Revision is the commit the re-attestation is recorded AT, so a later drift report can
// say what to diff rather than only that something changed.
//
// It reports HEAD even when the tree is dirty, deliberately. A note is normally written
// alongside the very work it describes, so demanding a clean tree would leave the provenance
// empty in the common case. HEAD-at-review is still the right base: it is the parent of
// the commit that will carry both the note and the change, so diffing from it shows the
// author exactly the work they were looking at. Empty when there is no resolvable VCS, which
// omits the provenance rather than blocking the write.
func Revision(ctx context.Context, root string) string {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return ""
	}
	meta, err := res.VCS.Metadata(ctx, root)
	if err != nil {
		return ""
	}
	return job.ShortRevision(meta.ID)
}
