package mem

import (
	"context"

	"github.com/egladman/magus/types"
)

// UsableFraction is the share of a machine's memory that build work may plan against
// under the balanced and conservative profiles. The remainder is not slack: it is the
// OS, the editor, the browser, the agent processes driving the build, and every
// toolchain cache that is memory-mapped rather than resident. A planner that budgets
// the whole machine is budgeting memory that was never available.
//
// One constant with two readers on purpose: the CI shard planner sizing a shard and
// the run-time admission gate sizing a host. They ask the same question, and two
// figures that drifted apart would be two different answers to it.
//
// aggressive does not use this fraction at all; see BudgetMB.
const UsableFraction = 0.75

// aggressiveFloorMB is what the aggressive profile leaves off the top instead of a
// percentage: enough for the kernel and its page cache to keep working without
// tipping into reclaim pressure. 512 MiB is a figure a small dedicated CI runner (the
// case this profile exists for) can afford to give up, unlike the quarter that
// balanced and conservative reserve for a laptop's editor and browser.
const aggressiveFloorMB = 512

// BudgetMB is the memory build work may plan against, in megabytes.
//
// Under aggressive it is every usable megabyte minus aggressiveFloorMB, no percentage
// reservation: the profile's whole point is claiming the machine, and a laptop's
// editor and browser are exactly what it assumes are not there to protect memory for.
// Every other profile (including the zero value) keeps UsableFraction.
//
// Zero when the host is unmeasurable, which every caller reads as no budget to
// arbitrate rather than as a budget of nothing. A measurable host below the floor
// under aggressive still gets a budget of 1 MB rather than 0, so it is never
// mistaken for the unmeasurable case: refusing nearly everything and arbitrating
// nothing are different answers.
func BudgetMB(usableBytes int64, profile types.ConcurrencyProfile) int {
	if usableBytes <= 0 {
		return 0
	}
	if profile == types.ProfileAggressive {
		usableMB := int(usableBytes / (1 << 20))
		if usableMB <= aggressiveFloorMB {
			return 1
		}
		return usableMB - aggressiveFloorMB
	}
	return int(float64(usableBytes) * UsableFraction / (1 << 20))
}

// UsableBytes is the memory THIS PROCESS may actually commit: the machine's total,
// narrowed by any ceiling the process runs under.
//
// The distinction TotalBytes cannot make. In a memory-limited container the machine
// reports its own RAM, which the process can never have, so a budget computed from
// it admits work the OOM killer then takes: magus would report a scheduling success
// and a killed build. Every caller sizing work THIS process will run wants this;
// only a caller planning for a DIFFERENT machine wants TotalBytes.
//
// A limit at or above the machine's total is treated as no limit, which is what
// makes reading the raw cgroup files safe: both cgroup versions spell unlimited as
// a sentinel near the top of the address space, and neither sentinel has to be
// recognized to be discarded here.
func UsableBytes(ctx context.Context) int64 {
	return narrowToLimit(TotalBytes(ctx), LimitBytes(ctx))
}

// narrowToLimit is UsableBytes's arithmetic, separated because the two readings it
// combines are platform calls with no seam: the interesting cases (an absurd v1
// sentinel, an unmeasurable host inside a measured container) are unreachable in a
// test that has to take the machine's real answers.
func narrowToLimit(total, limit int64) int64 {
	if limit > 0 && (total <= 0 || limit < total) {
		return limit
	}
	return total
}
