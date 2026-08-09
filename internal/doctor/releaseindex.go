package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/registry"
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
// Nothing republishes it on a timer - a release does, and the Release index workflow
// does on demand - so without something watching the clock the first sign of trouble
// would be `magus self update` refusing to run for everyone at once. That is the
// outage this whole area exists to have fixed, arriving by a different door.
func (r *runner) checkReleaseIndexExpiry() types.DoctorCheck {
	const name = "release index"

	data, err := os.ReadFile(filepath.Join(r.root, servedIndexPath))
	if os.IsNotExist(err) {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "not served from this workspace"}
	}
	if err != nil {
		return types.DoctorCheck{Name: name, Status: types.DoctorFail, Message: fmt.Sprintf("read %s: %v", servedIndexPath, err)}
	}

	var idx struct {
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return types.DoctorCheck{
			Name: name, Status: types.DoctorFail,
			Message: fmt.Sprintf("%s does not parse: %v", servedIndexPath, err),
		}
	}
	if idx.ExpiresAt == "" {
		return types.DoctorCheck{
			Name: name, Status: types.DoctorFail,
			Message: "the served index declares no expires_at, so a stale copy can be replayed indefinitely",
			Fix:     []string{"run", "release-index"},
		}
	}
	deadline, err := time.Parse(time.RFC3339, idx.ExpiresAt)
	if err != nil {
		return types.DoctorCheck{
			Name: name, Status: types.DoctorFail,
			Message: fmt.Sprintf("expires_at %q is not RFC3339, so no client can read the bound it sets", idx.ExpiresAt),
		}
	}

	left := time.Until(deadline)
	switch {
	case left <= 0:
		return types.DoctorCheck{
			Name: name, Status: types.DoctorFail,
			Message: fmt.Sprintf("the served release index expired %s ago; every `magus self update` is refusing it", roughly(-left)),
			Details: []string{"re-sign it with the Release index workflow, then merge the pull request it opens"},
			Fix:     []string{"run", "release-index"},
		}
	case left <= releaseIndexWarnWindow:
		return types.DoctorCheck{
			Name: name, Status: types.DoctorAdvice,
			Message: fmt.Sprintf("the served release index expires in %s (%s)", roughly(left), idx.ExpiresAt),
			Details: []string{"a release re-signs it; the Release index workflow does too, if none is due"},
			Fix:     []string{"run", "release-index"},
		}
	default:
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK,
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

// checkRegistryFreshness is where staleness prompts, deliberately instead of a
// note appended to a correct answer. A row that reads `2028-04-30 (41d)` has
// already told the truth, and 41 days of drift changes nothing the reader is doing
// this minute - so a nag there is one they learn to filter, which teaches them to
// filter the never-synced hint too.
//
// The Fix is what makes it actionable without anyone having heard of the command:
// `magus doctor --fix` runs it.
func (r *runner) checkRegistryFreshness() types.DoctorCheck {
	const name = "registry"

	cached, err := registry.Load()
	if err != nil {
		return types.DoctorCheck{Name: name, Status: types.DoctorFail, Message: err.Error()}
	}
	if len(cached) == 0 {
		// Every source declined. magus ships a built-in one, so an empty list can only
		// mean someone said no on purpose - and telling them to sync is the nag this
		// design exists to avoid.
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "declined"}
	}

	var never, stale []string
	for _, c := range cached {
		switch c.State {
		case registry.StateNeverSynced:
			never = append(never, c.Source.Name)
		case registry.StateStale:
			stale = append(stale, fmt.Sprintf("%s (%s old)", c.Source.Name, roughly(c.Age)))
		}
	}
	switch {
	case len(never) > 0:
		return types.DoctorCheck{
			Name: name, Status: types.DoctorAdvice,
			Message: fmt.Sprintf("never synced: %s", strings.Join(never, ", ")),
			Details: []string{"this fetches a data file; it does not upgrade magus"},
			Fix:     []string{"self", "refresh"},
		}
	case len(stale) > 0:
		return types.DoctorCheck{
			Name: name, Status: types.DoctorAdvice,
			Message: fmt.Sprintf("data is older than its window: %s", strings.Join(stale, ", ")),
			Details: []string{"age is measured from the publisher's generated_at, so this may mean the publisher stopped"},
			Fix:     []string{"self", "refresh"},
		}
	default:
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK,
			Message: fmt.Sprintf("%d source(s), all fresh", len(cached)),
		}
	}
}
