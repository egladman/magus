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

// LastRun reports how long the newest real run of target in project took, false when none
// is recorded.
type LastRun func(project, target string) (time.Duration, bool)

// ForegroundMax reports the longest a wired host lets a foreground call wait, false when
// no host declares one.
type ForegroundMax func() (time.Duration, bool)

// OverrunsForeground reports row's first target check whose last real run, as lastRun
// reports it, took longer than limit reports; false when none did, when the row names no
// target check, or when the limit is unknown. A check with no recorded run is passed over.
// limit is asked once, and only once a run is found, since answering it loads the wired
// harnesses.
func OverrunsForeground(row types.Job, lastRun LastRun, limit ForegroundMax) (ForegroundOverrun, bool) {
	var (
		longest         time.Duration
		asked, declared bool
	)
	for _, g := range row.EffectiveGoals() {
		if g.Kind != types.GoalKindCheck || g.Check.Target == "" {
			continue
		}
		project := cmp.Or(g.Check.Project, ".")
		took, ok := lastRun(project, g.Check.Target)
		if !ok {
			continue
		}
		if !asked {
			longest, declared = limit()
			asked = true
		}
		if !declared {
			return ForegroundOverrun{}, false
		}
		if took <= longest {
			continue
		}
		return ForegroundOverrun{
			Check:           hint.Run.With(g.Check.Target, project),
			LastRunMs:       took.Milliseconds(),
			ForegroundMaxMs: longest.Milliseconds(),
		}, true
	}
	return ForegroundOverrun{}, false
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
