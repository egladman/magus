//go:build (amd64 || arm64) && !buzz_safe && !buzz_unsafe

package vm

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Recovery tests for the baseline JIT: what happens when a native run reports an
// exit the interpreter cannot resume from, and when codegen panics outright.
//
// These are internal (package vm) rather than alongside the differential suite in
// bytecode_test.go because both paths are reachable only from a codegen DEFECT,
// which no Buzz program can provoke — a program can make the JIT deopt, never make
// it lie. Driving them needs jitTestExitHook and hand-built chunks, neither of
// which is exported. The differential suite still owns the question these cannot
// answer: whether the generated code computes the right answer in the first place.

// jitTest runs fn with the JIT forced on, stats zeroed, and the exit hook and
// fault hook cleared afterwards, so one test's corruption cannot leak into the
// next. Never call t.Parallel in these: the hook and the stats are package state.
func jitTest(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	was := JITEnabled()
	SetJIT(true)
	ResetJITStats()
	t.Cleanup(func() {
		jitTestExitHook = nil
		jitCompileFn = compileJIT
		SetJIT(was)
		ResetJITStats()
	})
	fn(t)
}

// addChunk builds `x = 1; x = x + 41; return x` — the smallest chunk that is JIT
// eligible (every opcode is in the depths() set), needs a local window (jitRun
// declines a chunk with nothing to anchor &stack[0] to), and MUTATES that local,
// so a restart that failed to restore the entry state would be visible.
func addChunk() *Chunk {
	c := &Chunk{Name: "add", LocalCount: 1}
	k1 := c.AddConst(IntValue(1))
	k41 := c.AddConst(IntValue(41))
	c.Code = []Instr{
		{Op: OpLoadConst, A: k1},  // 0: depth 0 -> 1
		{Op: OpSetLocal, A: 0},    // 1: depth 1 -> 0
		{Op: OpGetLocal, A: 0},    // 2: depth 0 -> 1
		{Op: OpLoadConst, A: k41}, // 3: depth 1 -> 2
		{Op: OpAdd},               // 4: depth 2 -> 1
		{Op: OpSetLocal, A: 0},    // 5: depth 1 -> 0
		{Op: OpGetLocal, A: 0},    // 6: depth 0 -> 1
		{Op: OpReturn},            // 7: depth 1 -> 0
	}
	return c
}

func runChunk(t *testing.T, c *Chunk) (Value, error) {
	t.Helper()
	return NewVM(context.Background()).Run(c, NewEnv())
}

// TestJITEligibleChunkRunsNatively is the premise every other test here rests on:
// if addChunk stopped being compiled, the corruption tests below would pass while
// exercising nothing. Pin engagement explicitly rather than inferring it.
func TestJITEligibleChunkRunsNatively(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		got, err := runChunk(t, addChunk())
		require.NoError(t, err)
		assert.Equal(t, IntValue(42), got, "1 + 41")
		assert.Positive(t, JITRunCount(), "native entries")
		assert.Zero(t, JITDeoptCount(), "deopts on an all-int chunk")
		assert.Zero(t, JITBadExitCount(), "bad exits on an uncorrupted run")
		assert.Zero(t, JITCompileFailCount(), "codegen failures")
	})
}

// TestJITBadExitReturnsInterpreterAnswer is the property the recovery exists for:
// however incoherent the native exit, Run still returns what the interpreter would
// have returned. Each case rewrites a clean jitDone exit into one of the shapes a
// miscompiled stub could produce.
func TestJITBadExitReturnsInterpreterAnswer(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(c *jitCtx)
	}{{
		name: "deopt stack height disagrees with the depth model",
		// The exact check: at ip 2 the operand stack is empty, so the only
		// coherent sp is base+LocalCount+0 == 1. A height past the reservation is
		// also what used to panic outright in vm.stack[:ctx.sp].
		corrupt: func(c *jitCtx) { c.status, c.resumeIP, c.sp = jitDeopt, 2, 4096 },
	}, {
		name: "deopt stack height plausible but wrong",
		// In range, so it never panics — it silently hands the interpreter an
		// operand stack one slot deeper than the ip says. This is the case a
		// bounds check alone would miss and the equality catches.
		corrupt: func(c *jitCtx) { c.status, c.resumeIP, c.sp = jitDeopt, 2, 2 },
	}, {
		name:    "deopt resume ip past the end of the chunk",
		corrupt: func(c *jitCtx) { c.status, c.resumeIP, c.sp = jitDeopt, 99, 1 },
	}, {
		name:    "deopt resume ip negative",
		corrupt: func(c *jitCtx) { c.status, c.resumeIP, c.sp = jitDeopt, -1, 1 },
	}, {
		name: "cancel-poll re-entry ip out of range",
		// Not caught, this re-enters generated code whose dispatch chain falls
		// through to ip 0 — a silently re-run loop, not a crash.
		corrupt: func(c *jitCtx) { c.status, c.resumeIP = jitCancelCheck, 99 },
	}, {
		name:    "status is not a value any stub writes",
		corrupt: func(c *jitCtx) { c.status = 77 },
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jitTest(t, func(t *testing.T) {
				var kinds []FaultKind
				chunk := addChunk()
				jitTestExitHook = func(c *jitCtx) {
					if c.status == jitDone { // corrupt the real exit, once
						tc.corrupt(c)
					}
				}
				v := NewVM(context.Background())
				v.SetFaultHook(func(k FaultKind) { kinds = append(kinds, k) })

				got, err := v.Run(chunk, NewEnv())
				require.NoError(t, err)
				assert.Equal(t, IntValue(42), got, "interpreter answer after a bad exit")
				assert.Equal(t, int64(1), JITBadExitCount(), "bad exits")
				assert.Equal(t, []FaultKind{FaultJITBadExit}, kinds, "faults reported")

				// The chunk is now ineligible, so the defect costs one wasted
				// native entry per process, not one per run.
				jitTestExitHook = nil
				before := JITRunCount()
				got, err = runChunk(t, chunk)
				require.NoError(t, err)
				assert.Equal(t, IntValue(42), got, "interpreted re-run")
				assert.Equal(t, before, JITRunCount(), "native entries after the bad exit")
				assert.Equal(t, int64(1), JITBadExitCount(), "bad exits after the bad exit")
			})
		})
	}
}

// TestJITBadExitRestoresEntryLocals drives jitRun directly so the locals window
// holds something other than the Nulls Run seeds it with. Generated code mutates
// locals in place, so a restart is only sound if that window is put back first.
//
// The chunk has to both READ and WRITE the local for this to test anything: one
// that only reads it is unaffected by a missing restore, and one that writes
// before reading overwrites whatever the restore did. `x = x + 1` distinguishes
// them - 6 when the entry value came back, 7 when the native run's 6 was left in
// place and incremented a second time.
func TestJITBadExitRestoresEntryLocals(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		c := &Chunk{Name: "incr", LocalCount: 1}
		k := c.AddConst(IntValue(1))
		c.Code = []Instr{
			{Op: OpGetLocal, A: 0},
			{Op: OpLoadConst, A: k},
			{Op: OpAdd},
			{Op: OpSetLocal, A: 0},
			{Op: OpGetLocal, A: 0},
			{Op: OpReturn},
		}
		jitTestExitHook = func(ctx *jitCtx) {
			if ctx.status == jitDone {
				ctx.status, ctx.resumeIP, ctx.sp = jitDeopt, 0, 4096
			}
		}
		v := NewVM(context.Background())
		v.stack = append(v.stack[:0], IntValue(5))
		v.frames = append(v.frames[:0], frame{chunk: c, env: NewEnv(), this: Null})

		got, ok, err := v.jitRun()
		require.NoError(t, err)
		require.True(t, ok, "jitRun handled the run")
		assert.Equal(t, IntValue(6), got, "5 + 1, from the restored entry local")
		assert.Equal(t, int64(1), JITBadExitCount(), "bad exits")
	})
}

// TestJITValidDeoptResumesInPlace guards the other direction: the new exit check
// must not reject a LEGITIMATE deopt. Both backends have an int and a double fast
// path and promote across them, so a non-number operand is what actually declines
// at run time. That exercises a real stub-written sp and proves it agrees with
// entryDepth — the equality is only worth having if it holds on the live path.
func TestJITValidDeoptResumesInPlace(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		c := &Chunk{Name: "boolcmp", LocalCount: 1}
		k := c.AddConst(BoolValue(true))
		c.Code = []Instr{
			{Op: OpLoadConst, A: k}, // 0: depth 0 -> 1
			{Op: OpLoadConst, A: k}, // 1: depth 1 -> 2
			{Op: OpEqual},           // 2: bools, not numbers: deopts here at depth 2
			{Op: OpReturn},          // 3
		}
		got, err := runChunk(t, c)
		require.NoError(t, err)
		assert.Equal(t, True, got, "true == true")
		assert.Equal(t, int64(1), JITDeoptCount(), "deopts")
		assert.Zero(t, JITBadExitCount(), "a legitimate deopt is not a bad exit")
	})
}

// TestJITCancelPollReentersAtIPZero covers the other exit that carries a resume
// ip: the back-edge cancellation poll, which hands control back and re-enters
// generated code rather than the interpreter. Two things make it worth its own
// test. The loop runs past the 256-iteration poll interval, so the re-entry
// actually happens; and its header sits at ip 0, the boundary where a resume ip is
// indistinguishable from the "first entry" sentinel, so an off-by-one in the range
// check turns a working loop into a discarded native run.
//
// It drives jitRun directly because a chunk compiled from source opens with a
// prologue, which puts every loop header at a nonzero ip and leaves this boundary
// unreachable through Run.
func TestJITCancelPollReentersAtIPZero(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		c := &Chunk{Name: "loop", LocalCount: 1}
		kn := c.AddConst(IntValue(300)) // > 256, so at least one poll fires
		k1 := c.AddConst(IntValue(1))
		c.Code = []Instr{
			{Op: OpGetLocal, A: 0},   // 0: loop header, and the back-edge target
			{Op: OpLoadConst, A: kn}, // 1
			{Op: OpLess},             // 2
			{Op: OpJumpFalse, A: 9},  // 3
			{Op: OpGetLocal, A: 0},   // 4
			{Op: OpLoadConst, A: k1}, // 5
			{Op: OpAdd},              // 6
			{Op: OpSetLocal, A: 0},   // 7
			{Op: OpJump, A: 0},       // 8: back edge to ip 0
			{Op: OpGetLocal, A: 0},   // 9
			{Op: OpReturn},           // 10
		}
		v := NewVM(context.Background())
		v.stack = append(v.stack[:0], IntValue(0))
		v.frames = append(v.frames[:0], frame{chunk: c, env: NewEnv(), this: Null})

		got, ok, err := v.jitRun()
		require.NoError(t, err)
		require.True(t, ok, "jitRun handled the run")
		assert.Equal(t, IntValue(300), got, "loop counter")
		assert.Greater(t, JITRunCount(), int64(1), "native entries (entry plus poll re-entry)")
		assert.Zero(t, JITDeoptCount(), "deopts on an all-int loop")
		assert.Zero(t, JITBadExitCount(), "a poll re-entry at ip 0 is not a bad exit")
	})
}

// TestJITCodegenPanicIsReported substitutes a panicking code generator, which is
// the only way in: depths() now validates every operand a backend indexes, so no
// malformed chunk reaches codegen and the recovery has no reachable trigger. The
// panic still has to degrade to the interpreter AND be reported, since a silently
// swallowed one is indistinguishable from an unsupported opcode.
func TestJITCodegenPanicIsReported(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		jitCompileFn = func(*Chunk) *compiledJIT { panic("simulated codegen defect") }
		var kinds []FaultKind
		chunk := addChunk()
		v := NewVM(context.Background())
		v.SetFaultHook(func(k FaultKind) { kinds = append(kinds, k) })

		got, err := v.Run(chunk, NewEnv())
		require.NoError(t, err)
		assert.Equal(t, IntValue(42), got, "interpreted answer after a codegen panic")
		assert.Equal(t, int64(1), JITCompileFailCount(), "codegen failures")
		assert.Equal(t, []FaultKind{FaultJITCompile}, kinds, "faults reported")
		assert.Zero(t, JITRunCount(), "native entries for a chunk that never compiled")

		// Cached as ineligible: the second run neither recompiles nor re-reports.
		got, err = runChunk(t, chunk)
		require.NoError(t, err)
		assert.Equal(t, IntValue(42), got, "interpreted re-run")
		assert.Equal(t, int64(1), JITCompileFailCount(), "codegen failures after a second run")
	})
}

// TestJITRejectsMalformedChunk covers the validation depths() now performs. Every
// case is a chunk the COMPILER cannot emit but marshal.Unmarshal can hand over,
// and each one names an operand a backend would otherwise index or turn into an
// address. The required verdict is the same for all of them and is deliberately
// the boring one: declined, silent, interpreted. Not a codegen panic (the old
// behavior for the branch-target cases) and not a native run (which for the slot
// cases would read past the stack reservation with nothing to catch it).
func TestJITRejectsMalformedChunk(t *testing.T) {
	k := func(c *Chunk, v Value) int32 { return c.AddConst(v) }
	tests := []struct {
		name  string
		build func() *Chunk
	}{{
		name: "jump target past the end",
		build: func() *Chunk {
			return &Chunk{LocalCount: 1, Code: []Instr{{Op: OpJump, A: 99}, {Op: OpReturnNull}}}
		},
	}, {
		name: "jump target negative",
		build: func() *Chunk {
			return &Chunk{LocalCount: 1, Code: []Instr{{Op: OpJump, A: -1}, {Op: OpReturnNull}}}
		},
	}, {
		name: "conditional jump target past the end",
		build: func() *Chunk {
			c := &Chunk{LocalCount: 1}
			c.Code = []Instr{
				{Op: OpLoadConst, A: k(c, BoolValue(true))},
				{Op: OpJumpFalse, A: 99},
				{Op: OpReturnNull},
			}
			return c
		},
	}, {
		name: "local slot past LocalCount",
		build: func() *Chunk {
			// slotOff(7) with one local: an offset past the reservation.
			return &Chunk{LocalCount: 1, Code: []Instr{{Op: OpGetLocal, A: 7}, {Op: OpReturn}}}
		},
	}, {
		name: "local slot negative",
		build: func() *Chunk {
			return &Chunk{LocalCount: 1, Code: []Instr{{Op: OpGetLocal, A: -1}, {Op: OpReturn}}}
		},
	}, {
		name: "store to a local slot past LocalCount",
		build: func() *Chunk {
			c := &Chunk{LocalCount: 1}
			c.Code = []Instr{
				{Op: OpLoadConst, A: k(c, IntValue(1))},
				{Op: OpSetLocal, A: 7},
				{Op: OpReturnNull},
			}
			return c
		},
	}, {
		name: "const index past the pool",
		build: func() *Chunk {
			return &Chunk{LocalCount: 1, Code: []Instr{{Op: OpLoadConst, A: 3}, {Op: OpReturn}}}
		},
	}, {
		name: "chunk does not end in a return",
		build: func() *Chunk {
			// Would make a numeric op the last instruction, and every one of them
			// emits `cont := label[ip+1]`.
			c := &Chunk{LocalCount: 1}
			c.Code = []Instr{
				{Op: OpLoadConst, A: k(c, IntValue(1))},
				{Op: OpLoadConst, A: k(c, IntValue(2))},
				{Op: OpAdd},
			}
			return c
		},
	}, {
		name:  "empty chunk",
		build: func() *Chunk { return &Chunk{LocalCount: 1} },
	}, {
		name: "fused compare with no absorbed nops",
		build: func() *Chunk {
			// OpCmpLC reads code[ip+1].A as its branch target; here ip+1 is the
			// return, whose A is not a target at all.
			c := &Chunk{LocalCount: 1}
			c.Code = []Instr{
				{Op: OpCmpLC, A: 0, B: k(c, IntValue(5)) | int32(OpLess)<<24},
				{Op: OpReturnNull},
			}
			return c
		},
	}, {
		name: "fused compare whose absorbed slot is not a nop",
		build: func() *Chunk {
			c := &Chunk{LocalCount: 1}
			c.Code = []Instr{
				{Op: OpCmpLC, A: 0, B: k(c, IntValue(5)) | int32(OpLess)<<24},
				{Op: OpNop, A: 4},
				{Op: OpGetLocal, A: 0}, // the interpreter skips this; the JIT would run it
				{Op: OpNop},
				{Op: OpReturnNull},
			}
			return c
		},
	}, {
		name: "fused binop sub-opcode is not an arithmetic op",
		build: func() *Chunk {
			c := &Chunk{LocalCount: 1}
			c.Code = []Instr{
				{Op: OpBinLC, A: 0, B: k(c, IntValue(5)) | int32(OpLoadTrue)<<24},
				{Op: OpNop},
				{Op: OpNop},
				{Op: OpReturn},
			}
			return c
		},
	}, {
		name: "fused binop destination slot past LocalCount",
		build: func() *Chunk {
			c := &Chunk{LocalCount: 1}
			c.Code = []Instr{
				{Op: OpBinLC, A: 0, B: k(c, IntValue(5)) | int32(OpMul)<<24, C: 8},
				{Op: OpNop},
				{Op: OpNop},
				{Op: OpNop},
				{Op: OpReturnNull},
			}
			return c
		},
	}, {
		name: "fused binop right-hand slot past LocalCount",
		build: func() *Chunk {
			c := &Chunk{LocalCount: 1}
			c.Code = []Instr{
				{Op: OpBinLL, A: 0, B: 9 | int32(OpMul)<<16, C: 1},
				{Op: OpNop},
				{Op: OpNop},
				{Op: OpNop},
				{Op: OpReturnNull},
			}
			return c
		},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jitTest(t, func(t *testing.T) {
				c := tc.build()
				_, _, ok := depths(c)
				assert.False(t, ok, "depths() accepted a malformed chunk")

				// Going through Run is the part that matters: declining has to be
				// silent and leave the interpreter to it. The interpreter may well
				// fault on a chunk this malformed (a wild jump target is caught by
				// Exec's own recover, as FaultPanic) and that is its business, not
				// this test's -- what must not appear is a JIT fault, because a
				// decline is routine rather than a defect.
				var kinds []FaultKind
				v := NewVM(context.Background())
				v.SetFaultHook(func(k FaultKind) { kinds = append(kinds, k) })
				_, _ = v.Run(c, NewEnv())
				assert.Zero(t, JITRunCount(), "native entries")
				assert.Zero(t, JITCompileFailCount(), "codegen failures")
				assert.NotContains(t, kinds, FaultJITCompile, "JIT faults reported")
				assert.NotContains(t, kinds, FaultJITBadExit, "JIT faults reported")
			})
		})
	}
}

// TestJITAcceptsBoundaryOperands is the other half of the validation: the checks
// must not reject the LAST valid value of each range. An off-by-one here would not
// break a program, it would silently stop compiling one - which is why the
// differential suite asserts engagement rather than just answers.
func TestJITAcceptsBoundaryOperands(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		c := &Chunk{Name: "boundary", LocalCount: 2}
		k := c.AddConst(IntValue(41)) // last const index
		c.Code = []Instr{
			{Op: OpLoadConst, A: k},
			{Op: OpSetLocal, A: 1}, // last local slot
			{Op: OpGetLocal, A: 1},
			{Op: OpLoadConst, A: k},
			{Op: OpEqual},
			{Op: OpJumpFalse, A: 7}, // target is the last instruction
			{Op: OpJump, A: 7},
			{Op: OpGetLocal, A: 1},
			{Op: OpReturn},
		}
		_, _, ok := depths(c)
		require.True(t, ok, "depths() rejected a well-formed chunk at its range boundaries")

		got, err := runChunk(t, c)
		require.NoError(t, err)
		assert.Equal(t, IntValue(41), got, "chunk result")
		assert.Positive(t, JITRunCount(), "native entries")
		assert.Zero(t, JITBadExitCount(), "bad exits")
	})
}

// TestJITDeclinedChunkIsNotAFailure separates the two ways a chunk ends up
// interpreted. An unsupported opcode is routine and must stay silent; conflating
// it with a codegen panic is exactly what made a broken backend invisible.
func TestJITDeclinedChunkIsNotAFailure(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		// OpLoadTrue is outside the compilable set, so depths() bails.
		c := &Chunk{Name: "declined", LocalCount: 1, Code: []Instr{
			{Op: OpLoadTrue},
			{Op: OpReturn},
		}}
		var kinds []FaultKind
		v := NewVM(context.Background())
		v.SetFaultHook(func(k FaultKind) { kinds = append(kinds, k) })

		got, err := v.Run(c, NewEnv())
		require.NoError(t, err)
		assert.Equal(t, True, got, "interpreted answer")
		assert.Zero(t, JITCompileFailCount(), "a declined shape is not a codegen failure")
		assert.Zero(t, JITRunCount(), "native entries")
		assert.Empty(t, kinds, "faults reported")
	})
}

// TestJITCacheReleasesDeadChunks is the leak test. A strong *Chunk key made the
// cache the reason its own entries could never expire, so the executable pages and
// the chunk's Code and Consts lasted the process rather than the chunk — invisible
// in a CLI run, cumulative in the daemon.
//
// It asserts on the exact weak key, never on a whole-cache total: jitCache and
// JITMappedBytes are process-wide, and the runtime.GC() calls below collect other
// tests' chunks too, so any assertion against an absolute baseline is a flake
// waiting for a busy suite. Holding the key proves nothing about liveness — a weak
// pointer is precisely the reference that does not keep its object alive, which is
// what lets the test observe the collection it caused.
func TestJITCacheReleasesDeadChunks(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		var key weak.Pointer[Chunk]
		func() {
			c := addChunk()
			key = weak.Make(c)
			got, err := runChunk(t, c)
			require.NoError(t, err)
			require.Equal(t, IntValue(42), got)
		}() // c goes out of scope here and nothing else holds it

		v, cached := jitCache.Load(key)
		require.True(t, cached, "compilation is cached while the chunk lives")
		mine := int64(len(v.(*compiledJIT).code))
		require.Positive(t, mine, "compilation mapped executable memory")
		peak := JITMappedBytes()

		// Cleanups run on their own goroutine after a collection, so poll rather
		// than assume one GC is enough. Poll the KEY, which only this test's chunk
		// can clear.
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, still := jitCache.Load(key); !still || time.Now().After(deadline) {
				break
			}
			runtime.GC()
			time.Sleep(5 * time.Millisecond)
		}

		_, cached = jitCache.Load(key)
		assert.False(t, cached, "cache entry removed once the chunk died")
		assert.Nil(t, key.Value(), "chunk itself collected (a strong cache key would pin it)")
		// Nothing compiles during the loop, so the gauge only falls; this chunk
		// accounts for at least `mine` of the fall however many others also went.
		assert.LessOrEqual(t, JITMappedBytes(), peak-mine, "executable memory released")
	})
}

// TestJITIneligibleEntryIsAlsoEvicted covers the entry type that was actually the
// bulk of the leak. Executable pages are only ever mapped for a chunk the backends
// COMPILE, and most chunks are not: anything with a call, a string, or a member
// access is declined. Every one of those still got a cached verdict, and under a
// strong key that entry pinned the whole chunk — Code, Consts and all — for the
// life of the process. So the dominant cost was never the RX pages; it was one
// pinned Chunk per chunk the process had ever run.
func TestJITIneligibleEntryIsAlsoEvicted(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		var key weak.Pointer[Chunk]
		func() {
			// OpLoadTrue is outside the compilable set, so this is declined.
			c := &Chunk{Name: "declined", LocalCount: 1, Code: []Instr{
				{Op: OpLoadTrue},
				{Op: OpReturn},
			}}
			key = weak.Make(c)
			got, err := runChunk(t, c)
			require.NoError(t, err)
			require.Equal(t, True, got)
		}()

		v, cached := jitCache.Load(key)
		require.True(t, cached, "a declined verdict is cached too")
		require.Same(t, jitIneligible, v, "cached as ineligible")
		require.Zero(t, JITRunCount(), "nothing was compiled, so nothing was mapped")

		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, still := jitCache.Load(key); !still || time.Now().After(deadline) {
				break
			}
			runtime.GC()
			time.Sleep(5 * time.Millisecond)
		}

		_, cached = jitCache.Load(key)
		assert.False(t, cached, "ineligible entry evicted with its chunk")
		assert.Nil(t, key.Value(), "declined chunk collected, not pinned by its own verdict")
	})
}

// TestJITConcurrentCompileMapsOnce drives several VMs into the code generator at
// once. Under -race this is what exposed a data race that had always been there:
// golang-asm initializes package-level assembler tables from NewBuilder without
// synchronization, so concurrent compilation corrupts the code generator's own
// state. Nothing caught it because the differential suite runs one VM at a time.
//
// Keep it running under -race. The assertions below (one shared compilation, one
// mapping) hold whether or not the scheduler overlaps the goroutines, but the race
// detector only has something to find when it does.
func TestJITConcurrentCompileMapsOnce(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		const racers = 8
		c := addChunk()
		before := JITMappedBytes()
		start := make(chan struct{})
		var wg sync.WaitGroup
		got := make([]*compiledJIT, racers)
		for i := range got {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start // release them together to maximize the overlap
				got[i], _ = jitCompileCached(c)
			}()
		}
		close(start)
		wg.Wait()

		require.NotNil(t, got[0], "chunk compiled")
		for i := 1; i < racers; i++ {
			assert.Same(t, got[0], got[i], "racer %d got a different compilation", i)
		}
		// At most ONE mapping's worth was added: the compile path re-checks the
		// cache under the lock, so racers dedupe instead of each compiling and
		// discarding. Stated as an upper bound because a background collection may
		// release some other test's chunk in the same window, which only moves this
		// down; duplicate compilations would move it up by a multiple.
		assert.LessOrEqual(t, JITMappedBytes()-before, int64(len(got[0].code)),
			"the same chunk was compiled more than once")
		runtime.KeepAlive(c)
	})
}

// TestJITLiveChunkKeepsItsMapping is the other half, and the one that matters for
// safety rather than footprint: while a chunk is reachable, neither its entry nor
// its pages may go anywhere. Eviction keyed on reachability is what makes
// unmapping safe at all, so an entry disappearing early would mean unmapping an
// instruction stream something is about to enter.
func TestJITLiveChunkKeepsItsMapping(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		c := addChunk()
		_, err := runChunk(t, c)
		require.NoError(t, err)
		key := weak.Make(c)

		for range 5 {
			runtime.GC()
		}

		// Again on the key, not on a total: other tests' chunks are collectable
		// during these GCs, so the process-wide gauge is expected to move.
		_, cached := jitCache.Load(key)
		assert.True(t, cached, "cache entry retained while the chunk is reachable")
		assert.NotNil(t, key.Value(), "chunk still live")

		// Still usable, which is the point of retaining it.
		got, err := runChunk(t, c)
		require.NoError(t, err)
		assert.Equal(t, IntValue(42), got, "chunk still runs after a GC")
		runtime.KeepAlive(c)
	})
}

// TestJITFaultKindStrings pins the metric labels, which are plain ASCII and reach
// logs and dashboards by name.
func TestJITFaultKindStrings(t *testing.T) {
	assert.Equal(t, "jit-compile-fail", FaultJITCompile.String())
	assert.Equal(t, "jit-bad-exit", FaultJITBadExit.String())
}

// TestResetJITStatsClearsEveryCounter keeps ResetJITStats honest: a counter added
// without being reset here would leak across tests and make a later assertion
// pass or fail for reasons that have nothing to do with the code under test.
func TestResetJITStatsClearsEveryCounter(t *testing.T) {
	jitTest(t, func(t *testing.T) {
		jitRuns.Store(3)
		jitDeopts.Store(4)
		jitCompileFails.Store(5)
		jitBadExits.Store(6)
		ResetJITStats()
		assert.Zero(t, JITRunCount(), "runs")
		assert.Zero(t, JITDeoptCount(), "deopts")
		assert.Zero(t, JITCompileFailCount(), "compile failures")
		assert.Zero(t, JITBadExitCount(), "bad exits")
	})
}
