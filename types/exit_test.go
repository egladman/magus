package types

import (
	"context"
	"errors"
	"testing"
)

func TestExitRecorder(t *testing.T) {
	ctx, read := WithExitRecorder(context.Background())
	if code, ok := read(); ok {
		t.Fatalf("recorder set before RecordExit: code=%d", code)
	}
	RecordExit(ctx, 5)
	code, ok := read()
	if !ok || code != 5 {
		t.Errorf("read() = (%d, %v), want (5, true)", code, ok)
	}
	// RecordExit on a ctx without a recorder is a no-op (must not panic).
	RecordExit(context.Background(), 9)
}

func TestExitError(t *testing.T) {
	var err error = ExitError{Code: 3}
	if err.Error() != "exit 3" {
		t.Errorf("Error() = %q, want %q", err.Error(), "exit 3")
	}
	// Must be recoverable via errors.As so the CLI/daemon can read the code
	// after it propagates (wrapped) up from a target.
	wrapped := errors.Join(errors.New("magusfile: target ci"), ExitError{Code: 2})
	var ex ExitError
	if !errors.As(wrapped, &ex) {
		t.Fatal("errors.As failed to recover ExitError")
	}
	if ex.Code != 2 {
		t.Errorf("recovered Code = %d, want 2", ex.Code)
	}
}
