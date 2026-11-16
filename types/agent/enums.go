package agent

import "github.com/egladman/magus/types/enum"

var workerTypes = enum.Set[WorkerType]{WorkerTypeResumed, WorkerTypeNew}

func (v WorkerType) Values() []string { return workerTypes.Strings() }
func (v WorkerType) Valid() bool      { return workerTypes.Valid(v) }
func (v WorkerType) String() string   { return enum.String(v) }
