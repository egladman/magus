//go:build linux

package cache

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCQEReadErr_success(t *testing.T) {
	c := CQE{UserData: 1, Result: 100}
	assert.NoError(t, c.ReadErr(100))
}

func TestCQEReadErr_shortRead(t *testing.T) {
	c := CQE{UserData: 1, Result: 50}
	err := c.ReadErr(100)
	require.Error(t, err, "expected error for short read")
	assert.Contains(t, err.Error(), "short read")
	assert.Contains(t, err.Error(), "got 50 want 100", "expected counts in error message")
}

func TestCQEReadErr_kernelError(t *testing.T) {
	// Result < 0 means a negated errno from the kernel.
	c := CQE{UserData: 1, Result: -int32(syscall.EIO)}
	err := c.ReadErr(100)
	require.Error(t, err, "expected error for negative result")
	assert.ErrorIs(t, err, syscall.EIO)
}

func TestCQEReadErr_zeroResult(t *testing.T) {
	// Result == 0 with wantLen > 0 is a short read (EOF before full buffer).
	c := CQE{UserData: 1, Result: 0}
	err := c.ReadErr(100)
	require.Error(t, err, "expected error for zero-byte result")
	assert.Contains(t, err.Error(), "short read")
}

func TestCQEReadErr_exactZero(t *testing.T) {
	// wantLen == 0 and Result == 0 should be fine (degenerate, but correct).
	c := CQE{UserData: 1, Result: 0}
	assert.NoError(t, c.ReadErr(0), "expected nil for zero-byte read")
}

func TestCheckKernelVersion_pass(t *testing.T) {
	// The test machine must be Linux ≥ 5.6 to run this package at all.
	// checkKernelVersion(5, 6) must succeed.
	assert.NoError(t, checkKernelVersion(5, 6), "unexpected error on current kernel")
}

func TestCheckKernelVersion_tooOld(t *testing.T) {
	// A future kernel requirement (99.0) must fail.
	err := checkKernelVersion(99, 0)
	require.Error(t, err, "expected error for unreachable kernel requirement")
	assert.Contains(t, err.Error(), "< required")
}

func TestSubmitRead_emptyBuf(t *testing.T) {
	// We don't need a real Ring; the guard fires before any field access.
	// Construct a minimal Ring with zeroed fields — the guard at the top of
	// SubmitRead must return an error before touching any mmap pointers.
	r := &Ring{}
	err := r.SubmitRead(0, nil, 0)
	require.Error(t, err, "expected error for empty buffer")
	assert.Contains(t, err.Error(), "empty buffer")
}

func TestSubmitRead_emptySlice(t *testing.T) {
	r := &Ring{}
	err := r.SubmitRead(0, []byte{}, 0)
	require.Error(t, err, "expected error for empty slice")
	assert.Contains(t, err.Error(), "empty buffer")
}
