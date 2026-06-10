package buzz

import vmpackage "github.com/egladman/gopherbuzz/vm"

// Value type aliases — maintain public surface for existing consumers.
type Value = vmpackage.Value
type Chunk = vmpackage.Chunk
type Instr = vmpackage.Instr
type UpvalInfo = vmpackage.UpvalInfo
type Env = vmpackage.Env
type Callable = vmpackage.Callable
type StepEvent = vmpackage.StepEvent
type StepMask = vmpackage.StepMask
type DebugFrame = vmpackage.DebugFrame

// Re-export value constructors.
var (
	NullValue   = vmpackage.NullValue
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
	StepLine   = vmpackage.StepLine
	StepCall   = vmpackage.StepCall
	StepReturn = vmpackage.StepReturn
	MaskLine   = vmpackage.MaskLine
	MaskCall   = vmpackage.MaskCall
	MaskReturn = vmpackage.MaskReturn
)

// Re-export chunk helpers used by compiler.
var (
	FoldConsts   = vmpackage.FoldConsts
	FusePeephole = vmpackage.FusePeephole
)

// Re-export env constructor.
var NewEnv = vmpackage.NewEnv

// Re-export value constructors for compiler use.
var (
	ObjDeclValue = vmpackage.ObjDeclValue
	EnumDefValue = vmpackage.EnumDefValue
)

// BytecodeVersion is the current bytecode format version.
const BytecodeVersion = vmpackage.BytecodeVersion
