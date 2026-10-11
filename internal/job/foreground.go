package job

import (
	"cmp"
	"fmt"
	"io"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// ForegroundOverrun is a job's check whose last real run took longer than a wired agent
// host lets a foreground call wait. Its holder cannot wait on it: the host moves the run to
// the background, and a worker that then ends its turn parks.
type ForegroundOverrun struct {
	// Check is the check as the command that runs it.
	Check string `json:"check" yaml:"check"`
	// LastRunMs is how long its newest real run took, in milliseconds.
	LastRunMs int64 `json:"last_run_ms" yaml:"last_run_ms"`
	// ForegroundMaxMs is the longest a wired host lets a foreground call wait, the shortest
	// across hosts when several are wired.
	ForegroundMaxMs int64 `json:"foreground_max_ms" yaml:"foreground_max_ms"`
}

// CheckOverrun is row's first check when its last real run, as lastRun reports it, took
// longer than foregroundMax reports; nil when it did not, when the row names no target
// check, or when either is unknown. foregroundMax is asked only once a run is found, since
// answering it loads the wired harnesses.
func CheckOverrun(row types.Job, lastRun func(project, target string) (time.Duration, bool), foregroundMax func() (time.Duration, bool)) *ForegroundOverrun {
	for _, g := range row.EffectiveGoals() {
		if g.Kind != types.GoalKindCheck || g.Check.Target == "" {
			continue
		}
		project := cmp.Or(g.Check.Project, ".")
		took, ok := lastRun(project, g.Check.Target)
		if !ok {
			return nil
		}
		limit, ok := foregroundMax()
		if !ok || took <= limit {
			return nil
		}
		return &ForegroundOverrun{
			Check:           hint.Run.With(g.Check.Target, project),
			LastRunMs:       took.Milliseconds(),
			ForegroundMaxMs: limit.Milliseconds(),
		}
	}
	return nil
}

// RenderForegroundOverrun writes o as one note, and nothing for nil.
func RenderForegroundOverrun(w io.Writer, o *ForegroundOverrun) {
	if o == nil {
		return
	}
	took := (time.Duration(o.LastRunMs) * time.Millisecond).Round(time.Second)
	limit := time.Duration(o.ForegroundMaxMs) * time.Millisecond
	fmt.Fprintf(w, "the check `%s` last ran %s here, past the %s a foreground call may wait, so its holder cannot wait on it; fork a narrower check, such as the same target with `-- -run <pattern>` over the tests the job touches\n",
		o.Check, took, limit)
}
