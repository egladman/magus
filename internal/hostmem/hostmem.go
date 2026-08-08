// Package hostmem reports the machine's memory, which magus needs to answer two
// different questions with two different numbers.
//
// Total is a property of the MACHINE CLASS and is what the CI shard planner
// budgets against: it must not depend on whatever happened to be running on the
// planner at the moment it planned.
//
// Available is a property of THIS MOMENT and is what the run-time watchdog
// watches: the question there is whether the work now in flight is about to take
// the machine down.
//
// Both report 0 for UNKNOWN rather than guessing. A caller must branch on 0
// instead of treating it as "no memory" - the planner plans as it did before a
// budget existed, and the watchdog stays silent rather than crying wolf on a host
// it cannot measure.
package hostmem

// Total reports the machine's total physical memory in bytes, or 0 when it cannot
// be determined. Implemented per platform in total_*.go.

// Available reports memory that can be handed out right now without swapping, in
// bytes, or 0 when it cannot be determined. Implemented per platform in
// available_*.go.
//
// "Available" deliberately means Linux's MemAvailable rather than MemFree: free
// memory on a busy CI runner is near zero and always has been, because the page
// cache holds everything the build just read. Watching MemFree would report an
// emergency on every healthy run.
