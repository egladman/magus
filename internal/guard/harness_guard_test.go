package guard

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckoutLeaseNamesAPlaceThatCannotBeResolved(t *testing.T) {
	_, _, err := CheckoutLease(t.Context(), t.TempDir(), func(string) (job.Location, error) { return job.Location{}, errors.New("no state dir") })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve the checkout's lease")
	assert.Contains(t, err.Error(), "no state dir")
}
