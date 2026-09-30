package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/hint"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// servedIndexPath is the tracked release index, relative to a workspace root. Only
// magus's own repository has one; everywhere else this check has nothing to say.
const servedIndexPath = "docs/gen/public/release/index.json"

// releaseIndexWarnWindow is how long before expires_at the check starts asking for a
// re-sign. Wide, because the remedy is a human running a workflow and merging its pull
// request, not a cron: the warning has to survive a month of not being looked at.
const releaseIndexWarnWindow = 45 * 24 * time.Hour

// checkReleaseIndexExpiry is the other half of putting expires_at in the signed index.
//
// The field bounds how long an attacker can replay an old index that names no
// revocation, which means it also bounds how long a LEGITIMATE index stays usable.
// Nothing republishes it on a timer (a release does, and the Release index workflow
// does on demand), so without something watching the clock the first sign of trouble
// would be `magus self update` refusing to run for everyone at once. That is the
// outage this whole area exists to have fixed, arriving by a different door.
//
// It carries NO Fix: re-signing is a human running a workflow, not a repair
// `doctor --fix` may perform on someone's behalf. The Fix arms it used to carry named
// `run release-index`, a target no workspace declares, so the first finding would have
// failed `doctor --fix` outright.
func (r *runner) checkReleaseIndexExpiry() types.Check {
	const name = "release-index"

	data, err := os.ReadFile(filepath.Join(r.root, servedIndexPath))
	if os.IsNotExist(err) {
		return types.Check{Name: name, Status: types.CheckOK, Message: "not served from this workspace"}
	}
	if err != nil {
		return types.Check{Name: name, Status: types.CheckFail, Message: fmt.Sprintf("read %s: %v", servedIndexPath, err)}
	}

	var idx struct {
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return types.Check{
			Name: name, Status: types.CheckFail,
			Message: fmt.Sprintf("%s does not parse: %v", servedIndexPath, err),
		}
	}
	if idx.ExpiresAt == "" {
		return types.Check{
			Name: name, Status: types.CheckFail,
			Message: "the served index declares no expires_at, so a stale copy can be replayed indefinitely",
			Details: []string{"re-sign it with the Release index workflow, then merge the pull request it opens"},
		}
	}
	deadline, err := time.Parse(time.RFC3339, idx.ExpiresAt)
	if err != nil {
		return types.Check{
			Name: name, Status: types.CheckFail,
			Message: fmt.Sprintf("expires_at %q is not RFC3339, so no client can read the bound it sets", idx.ExpiresAt),
		}
	}

	left := time.Until(deadline)
	switch {
	case left <= 0:
		return types.Check{
			Name: name, Status: types.CheckFail,
			Message: fmt.Sprintf("the served release index expired %s ago; every `%s` is refusing it", roughly(-left), hint.SelfUpdate),
			Details: []string{"re-sign it with the Release index workflow, then merge the pull request it opens"},
		}
	case left <= releaseIndexWarnWindow:
		return types.Check{
			Name: name, Status: types.CheckAdvice,
			Message: fmt.Sprintf("the served release index expires in %s (%s)", roughly(left), idx.ExpiresAt),
			Details: []string{"a release re-signs it; the Release index workflow does too, if none is due"},
		}
	default:
		return types.Check{
			Name: name, Status: types.CheckOK,
			Message: fmt.Sprintf("signed and good for another %s", roughly(left)),
		}
	}
}

// roughly renders a duration in the unit a human would use for it. time.Duration's own
// String gives "4319h0m0s" at this scale, which nobody reads as six months.
func roughly(d time.Duration) string {
	switch days := int(d.Hours() / 24); {
	case days >= 2:
		return fmt.Sprintf("%d days", days)
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return "under an hour"
	}
}
