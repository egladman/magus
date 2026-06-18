package buzz

import vmpackage "github.com/egladman/gopherbuzz/vm"

// Value type aliases — maintain public surface for existing consumers.
type Value = vmpackage.Value
type Chunk = vmpackage.Chunk
type Callable = vmpackage.Callable
type StepEvent = vmpackage.StepEvent
type StepMask = vmpackage.StepMask
type DebugFrame = vmpackage.DebugFrame

// Re-export value constructors.
var (
	BoolValue   = vmpackage.BoolValue
	IntValue    = vmpackage.IntValue
	FloatValue  = vmpackage.FloatValue
	StrValue    = vmpackage.StrValue
	UDValue     = vmpackage.UDValue
	ListValue   = vmpackage.ListValue
	PatValue    = vmpackage.PatValue
	DirectValue = vmpackage.DirectValue
	NewMap      = vmpackage.NewMap
	Null        = vmpackage.Null
	True        = vmpackage.True
	False       = vmpackage.False
)

// Re-export step event constants.
const (
	StepCall   = vmpackage.StepCall
	StepReturn = vmpackage.StepReturn
	MaskLine   = vmpackage.MaskLine
	MaskCall   = vmpackage.MaskCall
	MaskReturn = vmpackage.MaskReturn
)

// Re-export the enum-definition value constructor for compiler use.
var EnumDefValue = vmpackage.EnumDefValue

// BytecodeVersion is the current bytecode format version.
const BytecodeVersion = vmpackage.BytecodeVersion
