package guard

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// The stall rule routes a worker's block to the root. A gauge counts each leased worker's
// refusals in levels: 1 is any refusal, 2 the same rule refusing twice in a row or a run
// of refused calls, 3 a refusal the worker cannot clear itself. Only level 3 acts: the
// refusal names the job's exit, and one attention row per lease and rule states the facts.
// Levels 1 and 2 are recorded and never said, until data shows they help.
const denyRuleStall denyRuleName = "stall"

// unclearable are the refusals no command of a leased worker's own clears: a boundary its
// orchestrator drew, or a magus that cannot load the tree's rules.
var unclearable = map[string]bool{
	string(denyRuleLeaseWrite):      true,
	string(denyRuleLeaseVCS):        true,
	string(denyRuleLeaseHarness):    true,
	string(denyRuleLeaseRebind):     true,
	string(denyRuleLeaseUndeclared): true,
	string(denyRuleStaleBinary):     true,
}

// stallRowKind marks the attention row filed for one rule's refusals of a lease.
func stallRowKind(rule string) hint.MarkerKind {
	return hint.MarkerKind("stall-row-" + lineDigest(rule))
}

// gradeStall ends the first line of a refusal a leased worker cannot clear with the job's
// exit. It says so once per lease and rule, the firing that files the attention row; a
// repeat is worded as the one-line repeat every refusal gets.
//
// The exit rides the verdict line rather than a next command: a worker is never served a
// command that writes outside its write paths, and the job store is outside them.
func gradeStall(deps Dependencies, verdict types.GuardJudgment, lease string, at location) types.GuardJudgment {
	if verdict.Decision != decisionDeny || lease == "" || !unclearable[verdict.Rule] {
		return verdict
	}
	if deps.settingOf(string(denyRuleStall)).Decision == builtin.Off || stallGate(at.cacheDir, lease).AlreadyFired(stallRowKind(verdict.Rule)) {
		return verdict
	}
	line, rest, more := strings.Cut(verdict.Reason, "\n")
	verdict.Reason = line + " Only the root can clear this: exit with `" + hint.JobExit.With(lease, "--stdin") + "`, naming it in unresolved_risks."
	if more {
		verdict.Reason += "\n" + rest
	}
	return verdict
}

// stallRecord is the gauge for one lease.
type stallRecord struct {
	// Refusals counts every refused call under the lease.
	Refusals int `json:"refusals"`
	// Run counts refused calls in a row, and Last names the rule that refused the last.
	Run  int    `json:"run"`
	Last string `json:"last,omitempty"`
	// Level is the highest level the lease reached.
	Level int `json:"level"`
}

const stallRecordKind hint.MarkerKind = "stall"

// stallGate keys one lease's gauge, whichever session holds the lease.
func stallGate(cacheDir, lease string) hint.Gate {
	return hint.NewGate(cacheDir, "guard/stall/"+callerKeyEscaper.Replace(lease))
}

func readStall(gate hint.Gate) stallRecord {
	var rec stallRecord
	if gate.CacheDir() == "" {
		return rec
	}
	if body, err := os.ReadFile(hint.MarkerPath(gate.CacheDir(), gate.Session(), stallRecordKind)); err == nil {
		_ = json.Unmarshal(body, &rec)
	}
	return rec
}

func writeStall(gate hint.Gate, rec stallRecord) {
	if gate.CacheDir() == "" {
		return
	}
	if body, err := json.Marshal(rec); err == nil {
		_ = file.ReplaceFile(hint.MarkerPath(gate.CacheDir(), gate.Session(), stallRecordKind), body, 0o644)
	}
}

// stallRun is how many refused calls in a row reach level 2.
func stallRun(s builtin.Setting) int {
	if s.Calls > 0 {
		return s.Calls
	}
	if s.Profile == types.GuardProfileStrict {
		return 3
	}
	return 4
}

// stallLevel is the level a refusal of rule reaches, given the gauge before it.
func stallLevel(rec stallRecord, rule string, s builtin.Setting) int {
	switch {
	case unclearable[rule]:
		return 3
	case rec.Last == rule || rec.Run+1 >= stallRun(s):
		return 2
	}
	return 1
}

// observeStall moves a lease's gauge by one judged call, and files the lease's attention
// row the first time a rule refuses it at level 3. The settings are the ones the judgment
// recorded, since a trail write has none of its own.
func observeStall(ctx context.Context, at location, who hookAttribution, lease string, verdict types.GuardJudgment) {
	gate := stallGate(at.cacheDir, lease)
	rec := readStall(gate)
	s := recallSettings(at.cacheDir)[string(denyRuleStall)]
	switch verdict.Decision {
	case decisionDeny:
		level := stallLevel(rec, verdict.Rule, s)
		rec.Refusals++
		rec.Run++
		rec.Last = verdict.Rule
		rec.Level = max(rec.Level, level)
		writeStall(gate, rec)
		if level == 3 && s.Decision != builtin.Off && s.Decision != "" && !gate.MarkFired(stallRowKind(verdict.Rule)) {
			fileStallRow(ctx, at, who, lease, verdict, rec)
		}
	case decisionPass, decisionAdvise:
		if rec.Run > 0 {
			rec.Run, rec.Last = 0, ""
			writeStall(gate, rec)
		}
	}
}

// fileStallRow opens the attention row for a lease blocked at level 3. It states the facts
// a root decides on, the model the job ran on and how often it was refused, and names no
// remedy.
func fileStallRow(ctx context.Context, at location, who hookAttribution, lease string, verdict types.GuardJudgment, rec stallRecord) {
	dir, err := sessions.Dir(at.workspace)
	if err != nil {
		return
	}
	open := sessions.AttentionOpen{
		Outcome:  string(types.OutcomeWaiting),
		Severity: string(types.SeverityWarning),
		Source:   sessions.SourceLabel("guard", string(denyRuleStall)),
		Lease:    lease,
		Message: fmt.Sprintf("job %s is blocked by %s, which only the root can clear: %s It ran on %s; refusals under this lease: %d.",
			lease, verdict.Rule, firstLine(verdict.Reason), workerModel(ctx, at, who, lease), rec.Refusals),
	}
	origin := trail.StampOrigin(ctx, types.Origin{EntryPoint: types.EntryPointHook, Host: who.Host, Session: who.Session, Agent: who.Agent})
	_, _, _ = sessions.OpenRequest(dir, cmp.Or(who.Session, lease), open, sessions.InvocationStart{Origin: origin, Workspace: at.workspace, Lease: lease})
}

// workerModel is the model a leased worker runs on as magus recorded it: what its spawn
// named, else what its job row says, else that magus does not know.
func workerModel(ctx context.Context, at location, who hookAttribution, lease string) string {
	if rec, ok := readSpawnedAgent(hint.NewGate(at.cacheDir, who.factsKey()), who.Agent); ok && rec.Model != "" {
		return rec.Model
	}
	if rows, err := leaseRows(ctx, at); err == nil {
		for _, row := range rows {
			if row.ID == lease && row.Model != "" {
				return row.Model
			}
		}
	}
	return "a model magus has no record of"
}
