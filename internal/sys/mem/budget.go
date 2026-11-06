package mem

import (
	"cmp"
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
// aggressive keeps less; see Reserve.
const UsableFraction = 0.75

// aggressiveFloorMB and aggressiveShare are what the aggressive profile keeps
// for the OS: the larger of 1 GiB and an eighth. aggressive is the shipped
// default, and a flat 512 MiB budgeted 23.5 GiB of a 24 GiB Mac on 2026-11-05.
// Two seated steps then starved WindowServer.
const (
	aggressiveFloorMB = 1024
	aggressiveShare   = 8
)

// BudgetMB is the memory build work may plan against, in megabytes: Reserve's
// total less what it keeps back.
//
// Zero when the host is unmeasurable, which every caller reads as no budget to
// arbitrate rather than as a budget of nothing. A measurable host below the floor
// under aggressive still gets a budget of 1 MB rather than 0, so it is never
// mistaken for the unmeasurable case: refusing nearly everything and arbitrating
// nothing are different answers.
func BudgetMB(usableBytes int64, profile types.ConcurrencyProfile) int {
	r := Reserve(usableBytes, profile)
	if r.TotalMB <= 0 {
		return 0
	}
	return max(r.TotalMB-r.ReservedMB, 1)
}

// Reserve is what profile keeps back from usableBytes for the OS and desktop.
// No profile keeps nothing, so a budget never equals the machine.
func Reserve(usableBytes int64, profile types.ConcurrencyProfile) types.MemoryReserve {
	if usableBytes <= 0 {
		return types.MemoryReserve{}
	}
	totalMB := int(usableBytes / (1 << 20))
	if profile == types.ProfileAggressive {
		return types.MemoryReserve{
			TotalMB:    totalMB,
			ReservedMB: min(max(totalMB/aggressiveShare, aggressiveFloorMB), totalMB),
			Rule:       "the aggressive profile keeps 1/8, at least 1 GiB",
		}
	}
	budgetMB := int(float64(usableBytes) * UsableFraction / (1 << 20))
	return types.MemoryReserve{
		TotalMB:    totalMB,
		ReservedMB: totalMB - budgetMB,
		Rule:       "the " + string(cmp.Or(profile, types.ProfileBalanced)) + " profile keeps 1/4",
	}
}

// UsableBytes is the memory THIS PROCESS may actually commit: the machine's total,
// narrowed by any ceiling the process runs under.
//
// The distinction the machine total cannot make. In a memory-limited container the
// machine reports its own RAM, which the process can never have, so a budget
// computed from it admits work the OOM killer then takes: magus would report a
// scheduling success and a killed build. Every caller sizing work THIS process will
// run wants this.
//
// A limit at or above the machine's total is treated as no limit, which is what
// makes reading the raw cgroup files safe: both cgroup versions spell unlimited as
// a sentinel near the top of the address space, and neither sentinel has to be
// recognized to be discarded here.
func UsableBytes(ctx context.Context) int64 {
	return narrowToLimit(totalBytes(ctx), limitBytes(ctx))
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
