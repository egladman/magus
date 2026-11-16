package job

import "github.com/egladman/magus/libs/safetyscissors/internal/enum"

var workerTypes = enum.Set[WorkerType]{WorkerTypeResumed, WorkerTypeNew}

func (v WorkerType) Values() []string { return workerTypes.Strings() }
func (v WorkerType) Valid() bool      { return workerTypes.Valid(v) }
func (v WorkerType) String() string   { return enum.String(v) }

var briefFormats = enum.Set[BriefFormat]{BriefFormatMarkdown, BriefFormatBuzz}

func (v BriefFormat) Values() []string { return briefFormats.Strings() }
func (v BriefFormat) Valid() bool      { return briefFormats.Valid(v) }
func (v BriefFormat) String() string   { return enum.String(v) }

var claimKinds = enum.Set[ClaimKind]{ClaimRefs, ClaimTest, ClaimOutput, ClaimReleased, ClaimDeadHolder}

func (v ClaimKind) Values() []string { return claimKinds.Strings() }
func (v ClaimKind) Valid() bool      { return claimKinds.Valid(v) }
func (v ClaimKind) String() string   { return enum.String(v) }
