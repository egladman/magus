package buzz

import vmpackage "github.com/egladman/gopherbuzz/vm"

// Value type aliases — maintain public surface for existing consumers.
type Value = vmpackage.Value
type Chunk = vmpackage.Chunk
type Instr = vmpackage.Instr
type UpvalInfo = vmpackage.UpvalInfo
type OpCode = vmpackage.OpCode
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

// Re-export OpCode constants.
const (
	OpNop           = vmpackage.OpNop
	OpPop           = vmpackage.OpPop
	OpLoadConst     = vmpackage.OpLoadConst
	OpLoadNull      = vmpackage.OpLoadNull
	OpLoadTrue      = vmpackage.OpLoadTrue
	OpLoadFalse     = vmpackage.OpLoadFalse
	OpLoadName      = vmpackage.OpLoadName
	OpStoreName     = vmpackage.OpStoreName
	OpDefName       = vmpackage.OpDefName
	OpPushScope     = vmpackage.OpPushScope
	OpPopScope      = vmpackage.OpPopScope
	OpGetLocal      = vmpackage.OpGetLocal
	OpSetLocal      = vmpackage.OpSetLocal
	OpGetUpvalue    = vmpackage.OpGetUpvalue
	OpSetUpvalue    = vmpackage.OpSetUpvalue
	OpLoadThis      = vmpackage.OpLoadThis
	OpNeg           = vmpackage.OpNeg
	OpNot           = vmpackage.OpNot
	OpAdd           = vmpackage.OpAdd
	OpSub           = vmpackage.OpSub
	OpMul           = vmpackage.OpMul
	OpDiv           = vmpackage.OpDiv
	OpMod           = vmpackage.OpMod
	OpEqual         = vmpackage.OpEqual
	OpNotEqual      = vmpackage.OpNotEqual
	OpLess          = vmpackage.OpLess
	OpLessEqual     = vmpackage.OpLessEqual
	OpGreater       = vmpackage.OpGreater
	OpGreaterEqual  = vmpackage.OpGreaterEqual
	OpJump          = vmpackage.OpJump
	OpJumpFalse     = vmpackage.OpJumpFalse
	OpJumpTrue      = vmpackage.OpJumpTrue
	OpJumpFalsePeek = vmpackage.OpJumpFalsePeek
	OpJumpTruePeek  = vmpackage.OpJumpTruePeek
	OpJumpIfNull    = vmpackage.OpJumpIfNull
	OpGetMember     = vmpackage.OpGetMember
	OpSetMember     = vmpackage.OpSetMember
	OpGetIndex      = vmpackage.OpGetIndex
	OpSetIndex      = vmpackage.OpSetIndex
	OpNewList       = vmpackage.OpNewList
	OpNewMap        = vmpackage.OpNewMap
	OpNewClosure    = vmpackage.OpNewClosure
	OpCall          = vmpackage.OpCall
	OpReturn        = vmpackage.OpReturn
	OpReturnNull    = vmpackage.OpReturnNull
	OpInvoke        = vmpackage.OpInvoke
	OpNewObject     = vmpackage.OpNewObject
	OpIterInit      = vmpackage.OpIterInit
	OpIterNext      = vmpackage.OpIterNext
	OpRange         = vmpackage.OpRange
	OpIs            = vmpackage.OpIs
	OpAs            = vmpackage.OpAs
	OpTryBegin      = vmpackage.OpTryBegin
	OpTryEnd        = vmpackage.OpTryEnd
	OpThrow         = vmpackage.OpThrow
	OpYield         = vmpackage.OpYield
	OpFiber         = vmpackage.OpFiber
	OpBuildStr      = vmpackage.OpBuildStr
	OpCheckType     = vmpackage.OpCheckType
	OpGetField      = vmpackage.OpGetField
	OpSetField      = vmpackage.OpSetField
)

// Re-export OpCheckType type codes for the compiler.
const (
	CheckInt     = vmpackage.CheckInt
	CheckFloat   = vmpackage.CheckFloat
	CheckStr     = vmpackage.CheckStr
	CheckBool    = vmpackage.CheckBool
	CheckNonNull = vmpackage.CheckNonNull
	InstrMutBit  = vmpackage.InstrMutBit
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
