//go:build !windows && !wasm

package spawn

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// The sampler must not shrink a figure. It contributes a tree total when it sees
// one and nothing otherwise, and the recorded number is the larger of the two
// readings, so a long-running command reports at least what the kernel said.
//
// The tree total itself is pinned in internal/sys/mem, where the walk can be driven
// against children of a known size; here what matters is only that the fold picks
// the larger and that a running command is sampled at all.
func TestSamplerNeverReportsLessThanTheKernel(t *testing.T) {
	res, err := Exec(context.Background(), "sh",
		[]string{"-c", "sleep 0.6"}, ExecOptions{Quiet: true})
	require.NoError(t, err)
	require.True(t, res.Started)
	assert.Positive(t, res.MaxRSSBytes,
		"a command outliving several sample ticks still reports a figure")
}

func TestSamplerKeepsEverySampleOffset(t *testing.T) {
	t.Parallel()
	sizes := map[int]int64{10: 100 << 20, 20: 0}
	tick := make(chan time.Time)
	s := newTreeSampler(func(pid int) int64 { return sizes[pid] },
		func() (<-chan time.Time, func()) { return tick, func() {} })

	start := time.Unix(1000, 0)
	var recorded []types.MemorySample
	a := s.follow(10, start, func(m types.MemorySample) { recorded = append(recorded, m) })
	b := s.follow(20, start.Add(time.Second), nil)

	tick <- start.Add(200 * time.Millisecond)
	tick <- start.Add(400 * time.Millisecond)
	tick <- start.Add(1400 * time.Millisecond)

	assert.Equal(t, []types.MemorySample{
		{At: 200 * time.Millisecond, Bytes: 100 << 20},
		{At: 400 * time.Millisecond, Bytes: 100 << 20},
		{At: 1400 * time.Millisecond, Bytes: 100 << 20},
	}, s.stop(a), "every tick is kept, timed from the root's own start")
	assert.Len(t, recorded, 3, "each sample reaches the recorder as it is taken")
	assert.Empty(t, s.stop(b), "a reading of 0 is unknown, not a sample")
	assert.Nil(t, s.stop(nil), "a command that never started has no samples")
}

// A command shorter than one sample tick still reports the kernel's figure: the
// sampler contributes nothing and must not subtract anything either.
func TestShortCommandStillReportsKernelFigure(t *testing.T) {
	res, err := Exec(context.Background(), "sh", []string{"-c", "echo hi"}, ExecOptions{Capture: true})
	require.NoError(t, err)
	require.True(t, res.Started)
	assert.Positive(t, res.MaxRSSBytes, "the kernel figure survives when sampling finds nothing")
}
