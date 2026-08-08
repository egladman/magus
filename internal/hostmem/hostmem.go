// Package hostmem reads the machine's memory.
//
// Total is a property of the machine class and is what the CI shard planner
// budgets against. Available is a property of this moment and is what the
// run-time watchdog watches. Both return 0 for UNKNOWN; callers must branch on
// that rather than read it as "no memory".
package hostmem
