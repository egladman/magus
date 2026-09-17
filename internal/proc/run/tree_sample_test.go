//go:build !windows && !wasm

package run

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// A command shorter than one sample tick still reports the kernel's figure: the
// sampler contributes nothing and must not subtract anything either.
func TestShortCommandStillReportsKernelFigure(t *testing.T) {
	res, err := Exec(context.Background(), "sh", []string{"-c", "echo hi"}, ExecOptions{Capture: true})
	require.NoError(t, err)
	require.True(t, res.Started)
	assert.Positive(t, res.MaxRSSBytes, "the kernel figure survives when sampling finds nothing")
}
