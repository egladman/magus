package diff

import "github.com/egladman/magus/types/enum"

var diffUncoveredReasons = enum.Set[UncoveredReason]{"no-indexer"}

func (v UncoveredReason) Values() []string { return diffUncoveredReasons.Strings() }
func (v UncoveredReason) Valid() bool      { return diffUncoveredReasons.Valid(v) }
func (v UncoveredReason) String() string   { return enum.String(v) }

var diffReadStates = enum.Set[ReadState]{"known", "unknown"}

func (v ReadState) Values() []string { return diffReadStates.Strings() }
func (v ReadState) Valid() bool      { return diffReadStates.Valid(v) }
func (v ReadState) String() string   { return enum.String(v) }
