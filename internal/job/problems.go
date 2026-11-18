package job

import (
	"errors"
	"fmt"
	"slices"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// declarationProblems is every problem d shows on its own, without a workspace: its
// faults under magus's codes, with a deviation allowed only a [types.Permissible] code,
// and every check it carries that does not read as a lease check.
func declarationProblems(d job.Declaration) types.JobProblems {
	codes := types.PermissibleCodes()
	permissible := make([]job.Code, len(codes))
	for i, c := range codes {
		permissible[i] = job.Code(c)
	}
	// The module allows a deviation only a listed code, and a dropped field is permitted
	// by its subject under JobRecordInvalid, so its fault is taken back here.
	drops := map[string]bool{}
	for i, w := range d.Deviations {
		code := types.DiagnosticCode(w.Code)
		if !types.Permissible(code) && (types.JobProblem{Code: code, Subject: w.Subject}).Permissible() {
			drops[fmt.Sprintf("deviations[%d].code", i)] = true
		}
	}
	fs := slices.DeleteFunc(d.Faults(permissible...), func(f job.Fault) bool { return drops[f.Field] })
	if d.Enter == "" && len(d.Claim) == 0 && !d.Settles() {
		fs = append(fs, checkFaults(job.FaultRecord, "check", d.Check)...)
		for i, g := range d.Goals {
			fs = append(fs, goalCheckFaults(g, fmt.Sprintf("goals[%d].", i))...)
		}
	}
	return types.FaultProblems(fs)
}

// ValidateDeclaration reports what is wrong with a declared job, or nil: every problem
// [declarationProblems] finds, joined.
func ValidateDeclaration(d job.Declaration) error { return declarationProblems(d).Error() }

// validateGoal reports whether g can be matched to an output record without interpreting
// a shell command: every problem it shows on its own, joined.
func validateGoal(g job.Goal) error {
	return types.FaultProblems(append(g.Faults(), goalCheckFaults(g, "")...)).Error()
}

// goalCheckFaults is the faults of a check goal's check, each Field under prefix. A goal
// of another kind carrying a check is the module's fault to report.
func goalCheckFaults(g job.Goal, prefix string) []job.Fault {
	if g.Resolve().Kind != job.GoalKindCheck {
		return nil
	}
	return checkFaults(job.FaultGoal, prefix+"check", g.Check)
}

// checkFaults is why raw, at field, is no lease check: it does not decode, or it names
// what [validateLeaseCheck] refuses. A field that declares no check has none.
func checkFaults(kind job.FaultKind, field string, raw codec.RawMessage) []job.Fault {
	if !job.HasCheck(raw) {
		return nil
	}
	c, err := types.DecodeLeaseCheck(raw)
	if err != nil {
		return []job.Fault{{Kind: kind, Field: field, Message: err.Error(),
			Fix: "write the check as {target, project, args, no_default_charms} or {script, args, test}"}}
	}
	_, err = validateLeaseCheck(c)
	var shape *checkShapeError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &shape) && shape.Field == "test" && kind == job.FaultRecord:
		return []job.Fault{{Kind: kind, Field: field + ".test", Message: err.Error(), Fix: "drop test, or name a script"}}
	case errors.As(err, &shape):
		return []job.Fault{{Kind: kind, Field: field + ".script", Message: err.Error(),
			Fix: "name a target or a script, and give a script no project or charms"}}
	default:
		return []job.Fault{{Kind: kind, Field: field + ".target", Message: err.Error(), Fix: "name one target and the project it runs in"}}
	}
}
