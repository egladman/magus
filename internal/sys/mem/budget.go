package mem

import "context"

// UsableFraction is the share of a machine's memory that build work may plan
// against. The remainder is not slack: it is the OS, the editor, the browser, the
// agent processes driving the build, and every toolchain cache that is memory-mapped
// rather than resident. A planner that budgets the whole machine is budgeting memory
// that was never available.
//
// One constant with two readers on purpose: the CI shard planner sizing a shard and
// the run-time admission gate sizing a host. They ask the same question, and two
// figures that drifted apart would be two different answers to it.
const UsableFraction = 0.75

// BudgetMB is the memory build work may plan against, in megabytes: usable narrowed by
// UsableFraction. Zero when the host is unmeasurable, which every caller reads as no
// budget to arbitrate rather than as a budget of nothing.
func BudgetMB(usableBytes int64) int {
	if usableBytes <= 0 {
		return 0
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
