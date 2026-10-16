package guard

import (
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

const denyRuleSpawnUnvalidated denyRuleName = "spawn-unvalidated"

// denySpawnUnvalidated refuses handing row to a worker when the store never validated it.
func denySpawnUnvalidated(row types.Job) denial {
	return denial{
		Say: "job " + row.ID + " carries no `validated` stamp, so nothing checked its record before a worker would take it: `" +
			hint.JobFork.With(row.ID, "--dry-run") + "` lists its problems, then `" + hint.JobApply.With("-f", "<record>") + "` validates it.",
		Why: "A fork or an apply validates the whole record and stamps the row; a row without the stamp was written some other way, " +
			"so its write paths, check and goals may hold a problem every worker would meet only after it started.",
	}
}
