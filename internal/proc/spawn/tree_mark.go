package spawn

import "errors"

// TreeMarkEnv carries the ids of every bounded process tree a process descends
// from. A child inherits it whatever its parentage, so a command's leftovers
// are found by it. A process meant to outlive its run drops it.
const TreeMarkEnv = "MAGUS_PROC_TREE"

// ErrTooManyProcesses is a command whose process tree passed its bound.
var ErrTooManyProcesses = errors.New("process tree over its bound")
