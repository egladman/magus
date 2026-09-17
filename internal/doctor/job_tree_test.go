package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckJobTreeFlagsOrphansAndStaleJobs(t *testing.T) {
	_, root, store := tmpLedger(t)
	seed(t, store, types.Job{ID: "root", State: types.StatePass})
	seed(t, store, types.Job{ID: "root/orphan", Parent: "root", State: types.StateRunning})
	seed(t, store, types.Job{ID: "live", State: types.StateRunning})

	now := time.Now().Unix()
	got := checkJobTree(root, config.Jobs{}, now)
	require.Equal(t, types.DoctorAdvice, got.Status, got.Message)
	details := strings.Join(got.Details, "\n")
	assert.Contains(t, got.Message, "root/orphan")
	assert.Contains(t, details, hint.JobExit.With("root/orphan"))
	assert.NotContains(t, details, hint.JobExit.With("live"), "a live root is nobody's orphan, and staleness is unset")

	stale := checkJobTree(root, config.Jobs{StaleAfter: time.Minute}, now+3600)
	require.Equal(t, types.DoctorAdvice, stale.Status)
	assert.Contains(t, strings.Join(stale.Details, "\n"), hint.JobExit.With("live"))
	assert.Contains(t, stale.Message, "jobs.stale_after")
}

func TestCheckJobTreePassesAHealthyPlan(t *testing.T) {
	_, root, store := tmpLedger(t)
	seed(t, store, types.Job{ID: "live", State: types.StateRunning})

	got := checkJobTree(root, config.Jobs{StaleAfter: time.Hour}, time.Now().Unix())
	assert.Equal(t, types.DoctorOK, got.Status, got.Message)
}

func TestCheckJobTreeNamesJobsBlockedOnAnEndedDependency(t *testing.T) {
	_, root, store := tmpLedger(t)
	seed(t, store, types.Job{ID: "dep", State: types.StateFail})
	seed(t, store, types.Job{ID: "waiter", State: types.StateDeclared, DependsOn: []string{"dep"}, WritePaths: []string{"internal/job"}})
	seed(t, store, types.Job{ID: "queued", State: types.StateDeclared, DependsOn: []string{"live"}})
	seed(t, store, types.Job{ID: "live", State: types.StateRunning})

	got := checkJobTree(root, config.Jobs{}, time.Now().Unix())
	require.Equal(t, types.DoctorAdvice, got.Status, got.Message)
	assert.Contains(t, got.Message, "waiter is blocked on dep which is fail")
	assert.NotContains(t, got.Message, "queued", "a dependency still running is a plan in order, not a finding")
}
