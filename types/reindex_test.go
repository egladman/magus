package types_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
)

func TestReindexRefusalsKeepsOnlyGateRefusals(t *testing.T) {
	refusal := fmt.Errorf("magus: %w", types.DiagnosticErrorf(types.ProjectLockHeldByAncestor,
		"project . is locked by the magus run this one is nested inside"))
	missing := errors.New("docs: exit status 127; install scip-typescript")

	require.NoError(t, types.ReindexRefusals(missing), "a missing indexer stays a warning")

	got := types.ReindexRefusals(errors.Join(missing, refusal))
	require.ErrorIs(t, got, types.ProjectLockHeldByAncestor)
	assert.NotContains(t, got.Error(), "scip-typescript", "only the refusal fails the build")

	require.ErrorIs(t, types.ReindexRefusals(refusal), types.ProjectLockHeldByAncestor, "an unjoined refusal counts too")

	full := types.DiagnosticErrorf(types.MachineBudgetExhausted, "not starting scip: this machine's build budget is full")
	got = types.ReindexRefusals(errors.Join(missing, full))
	require.ErrorIs(t, got, types.MachineBudgetExhausted, "a full budget fails the build")
	assert.NotContains(t, got.Error(), "scip-typescript", "only the refusal fails the build")

	short := types.ExitError{Code: exit.TempFail, Err: types.DiagnosticErrorf(types.MemoryPressureHigh, "not starting scip: this machine is short of memory")}
	got = types.ReindexRefusals(errors.Join(missing, short))
	require.ErrorIs(t, got, types.MemoryPressureHigh)
	assert.NotContains(t, got.Error(), "scip-typescript")
	assert.Equal(t, exit.TempFail, exit.Code(got))
}
