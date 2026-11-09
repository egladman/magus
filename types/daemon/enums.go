package daemon

import "github.com/egladman/magus/types/enum"

// The case lists of this package's closed string types, less each zero
// value. TestBoundaryEnumsMatchTheirSets holds each set equal to the
// constants buzzgen derives the Buzz cases from.

var serviceStates = enum.Set[ServiceState]{"starting", "running", "idle", "failed"}

func (v ServiceState) Values() []string { return serviceStates.Strings() }
func (v ServiceState) Valid() bool      { return serviceStates.Valid(v) }
func (v ServiceState) String() string   { return enum.String(v) }

var targetRunStates = enum.Set[TargetRunState]{"queued", "running", "passed", "failed", "cached", "permitted"}

func (v TargetRunState) Values() []string { return targetRunStates.Strings() }
func (v TargetRunState) Valid() bool      { return targetRunStates.Valid(v) }
func (v TargetRunState) String() string   { return enum.String(v) }
