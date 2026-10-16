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

var ruleDocSpawnUnvalidated = types.RuleDoc{
	Name:    string(denyRuleSpawnUnvalidated),
	Catches: "a spawn for a job whose record was never validated",
	Why: "A spawn titled `<parent>/<role> <job>` hands that job to its child, and `magus job fork` and `magus job apply` are the doors that validate a record whole and stamp the row. " +
		"A row with no stamp skipped that check, so its problems would surface in the worker, after the work began. " +
		"A title naming no live row is spawn-unplaced's, not this rule's. " +
		"It cannot tell a row an older magus wrote, before rows were stamped, from one a test or a catalog seeded straight into the store: both carry no stamp, and both are re-validated by applying the record again.",
}
