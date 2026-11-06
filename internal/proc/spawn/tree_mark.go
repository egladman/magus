package spawn

// TreeMarkEnv carries the ids of every bounded process tree a process descends
// from. A child inherits it whatever its parentage, so a command's leftovers
// are found by it. A process meant to outlive its run drops it.
const TreeMarkEnv = "MAGUS_PROC_TREE"
