package cache

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapNeedFailureKeepsTheErrorText(t *testing.T) {
	base := errors.New("[MGS4007] .:conventions failed")
	wrapped := WrapNeedFailure(fmt.Errorf("ctx.needs: %w", base), NeedMember{Target: "conventions", Ref: "outaaaaaaaaaaaa"})

	assert.Equal(t, "ctx.needs: [MGS4007] .:conventions failed", wrapped.Error(), "hopChain and causeSignature read this text")
	require.ErrorIs(t, wrapped, base)
	target, ref := asNeedFailure(fmt.Errorf("ctx.needs: %w", wrapped))
	assert.Equal(t, "conventions", target)
	assert.Equal(t, "outaaaaaaaaaaaa", ref)
}

func TestWrapNeedFailureKeepsTheDeepestMember(t *testing.T) {
	inner := WrapNeedFailure(errors.New("golangci exited 1"), NeedMember{Target: "lint-go", Ref: "outbbbbbbbbbbbb"})
	outer := WrapNeedFailure(fmt.Errorf("ctx.needs: %w", inner), NeedMember{Target: "lint", Ref: "outcccccccccccc"})

	target, ref := asNeedFailure(outer)
	assert.Equal(t, "lint-go", target)
	assert.Equal(t, "outbbbbbbbbbbbb", ref)
}

func TestWrapNeedFailureLeavesWhatItCannotName(t *testing.T) {
	require.NoError(t, WrapNeedFailure(nil, NeedMember{Target: "lint", Ref: "outaaaaaaaaaaaa"}))

	boom := errors.New("boom")
	assert.Same(t, boom, WrapNeedFailure(boom, NeedMember{Target: "lint"}), "a member whose output was not persisted has no ref to name")
	target, ref := asNeedFailure(boom)
	assert.Empty(t, target)
	assert.Empty(t, ref)
}
