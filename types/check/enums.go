package check

import "github.com/egladman/magus/types/enum"

var statuses = enum.Set[Status]{"ok", "fail", "advice"}

func (v Status) Values() []string { return statuses.Strings() }
func (v Status) Valid() bool      { return statuses.Valid(v) }
func (v Status) String() string   { return enum.String(v) }
