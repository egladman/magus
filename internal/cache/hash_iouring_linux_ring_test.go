//go:build linux

package cache

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

// ── BUG 1: CQE.ReadErr short-read detection ───────────────────────────────

func TestCQEReadErr_success(t *testing.T) {
	c := CQE{UserData: 1, Result: 100}
	if err := c.ReadErr(100); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCQEReadErr_shortRead(t *testing.T) {
	c := CQE{UserData: 1, Result: 50}
	err := c.ReadErr(100)
	if err == nil {
		t.Fatal("expected error for short read, got nil")
	}
	if !strings.Contains(err.Error(), "short read") {
		t.Fatalf("expected 'short read' in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "got 50 want 100") {
		t.Fatalf("expected counts in error message, got %q", err.Error())
	}
}

func TestCQEReadErr_kernelError(t *testing.T) {
	// Result < 0 means a negated errno from the kernel.
	c := CQE{UserData: 1, Result: -int32(syscall.EIO)}
	err := c.ReadErr(100)
	if err == nil {
		t.Fatal("expected error for negative result, got nil")
	}
	if !errors.Is(err, syscall.EIO) {
		t.Fatalf("expected EIO, got %v", err)
	}
}

func TestCQEReadErr_zeroResult(t *testing.T) {
	// Result == 0 with wantLen > 0 is a short read (EOF before full buffer).
	c := CQE{UserData: 1, Result: 0}
	err := c.ReadErr(100)
	if err == nil {
		t.Fatal("expected error for zero-byte result, got nil")
	}
	if !strings.Contains(err.Error(), "short read") {
		t.Fatalf("expected 'short read' in error, got %q", err.Error())
	}
}

func TestCQEReadErr_exactZero(t *testing.T) {
	// wantLen == 0 and Result == 0 should be fine (degenerate, but correct).
	c := CQE{UserData: 1, Result: 0}
	if err := c.ReadErr(0); err != nil {
		t.Fatalf("expected nil for zero-byte read, got %v", err)
	}
}

// ── BUG 3: checkKernelVersion ─────────────────────────────────────────────

func TestCheckKernelVersion_pass(t *testing.T) {
	// The test machine must be Linux ≥ 5.6 to run this package at all.
	// checkKernelVersion(5, 6) must succeed.
	if err := checkKernelVersion(5, 6); err != nil {
		t.Fatalf("unexpected error on current kernel: %v", err)
	}
}

func TestCheckKernelVersion_tooOld(t *testing.T) {
	// A future kernel requirement (99.0) must fail.
	err := checkKernelVersion(99, 0)
	if err == nil {
		t.Fatal("expected error for unreachable kernel requirement, got nil")
	}
	if !strings.Contains(err.Error(), "< required") {
		t.Fatalf("expected '< required' in error, got %q", err.Error())
	}
}

// ── BUG 4: SubmitRead empty-buffer guard ──────────────────────────────────

func TestSubmitRead_emptyBuf(t *testing.T) {
	// We don't need a real Ring; the guard fires before any field access.
	// Construct a minimal Ring with zeroed fields — the guard at the top of
	// SubmitRead must return an error before touching any mmap pointers.
	r := &Ring{}
	err := r.SubmitRead(0, nil, 0)
	if err == nil {
		t.Fatal("expected error for empty buffer, got nil")
	}
	if !strings.Contains(err.Error(), "empty buffer") {
		t.Fatalf("expected 'empty buffer' in error, got %q", err.Error())
	}
}

func TestSubmitRead_emptySlice(t *testing.T) {
	r := &Ring{}
	err := r.SubmitRead(0, []byte{}, 0)
	if err == nil {
		t.Fatal("expected error for empty slice, got nil")
	}
	if !strings.Contains(err.Error(), "empty buffer") {
		t.Fatalf("expected 'empty buffer' in error, got %q", err.Error())
	}
}
