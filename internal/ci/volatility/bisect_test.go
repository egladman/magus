package volatility

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/stretchr/testify/assert"
)

// TestBisectRefusesWithoutAConfirmedRegression: bisect rewrites the working tree, so
// it must not start on a project whose history shows no regression.
func TestBisectRefusesWithoutAConfirmedRegression(t *testing.T) {
	t.Parallel()

	_, err := Bisect(context.Background(), t.TempDir(), &forecast.History{}, testCfg, testProject, testTarget, "")
	assert.ErrorContains(t, err, `no confirmed regression detected for "proj"/"test"`)
}
