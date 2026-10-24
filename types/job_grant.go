package types

// JobGrant is one line of a job store's grants file: a person's approval lowering one
// guard rule to advice for one job's calls, or the revocation of it. The last line per
// (Job, Rule) is the grant.
type JobGrant struct {
	Job  string `json:"job" yaml:"job"`
	Rule string `json:"rule" yaml:"rule"`
	// By is who typed the grant, as the trail records any origin.
	By Origin `json:"by" yaml:"by"`
	// At is the unix second the line was written.
	At int64 `json:"at" yaml:"at"`
	// Until is the unix second the grant lapses, zero for none: it still ends with the job.
	Until int64 `json:"until,omitzero" yaml:"until,omitempty"`
	// Attempt is the job's attempt the grant was given in. A fork of the ended row starts
	// the next attempt, which the grant does not reach.
	Attempt int `json:"attempt,omitzero" yaml:"attempt,omitempty"`
	// Revoked ends the grant the line keys.
	Revoked bool `json:"revoked,omitzero" yaml:"revoked,omitempty"`
}

// Key names the grant a line writes: a job and a rule.
func (g JobGrant) Key() string { return g.Job + "\x00" + g.Rule }

// Lowers reports whether g still lowers its rule for row at now: unrevoked, unexpired,
// and given in row's current attempt while row is still running.
func (g JobGrant) Lowers(row Job, now int64) bool {
	return !g.Revoked && g.Job == row.ID && row.State.Live() && g.Attempt == row.Attempts && (g.Until == 0 || now < g.Until)
}
