package quiet

import "diag"

func calls() {
	_ = diag.Errorf(1, "refused because the row ended, so nothing runs")
	_ = diag.Errorf(1, "run `magus graph build`, then `magus refs X`")
	_ = diag.Errorf(1, "server: not running") // want `message-tag: Drop the leading 'server:' tag: open with the verdict; see the runbook`
}
