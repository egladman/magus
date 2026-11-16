package job

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/job"
)

// CheckCommand renders c as the command a person types and a job stores. Running it
// satisfies the check. The charms it executes under, default_charms included, are the
// charms the check is resolved to (ranCheck).
func CheckCommand(c job.LeaseCheck) string {
	if c.Script != "" {
		line := "magus buzz --record " + c.Script
		if c.Test {
			line = "magus buzz -t " + c.Script
		}
		if len(c.Args) > 0 {
			line += " -- " + strings.Join(c.Args, " ")
		}
		return line
	}
	project := c.Project
	if project == "" {
		project = "."
	}
	line := fmt.Sprintf("magus run %s %s", c.Target, project)
	if c.NoDefaultCharms {
		line += " --no-default-charms"
	}
	if len(c.Args) > 0 {
		line += " -- " + strings.Join(c.Args, " ")
	}
	return line
}

// validationLine is a row's Validation: its check as [CheckCommand] renders it, "" for a row
// with none. A store write renders it so the two cannot disagree.
func validationLine(c *job.LeaseCheck) string {
	if c == nil {
		return ""
	}
	return CheckCommand(*c)
}

// ParseCheckCommand reads a rendered `[magus] run <target> <project> [-- args]`, or
// `[magus] buzz --record <script> [-- args]`, back into a record.
//
// compat(until: no client still sends a rendered validation line): jobs
// declared it before the check record existed. Observe it by grepping the
// ledger archives under the per-repository state dir for a record with
// `validation` and no `check`.
func ParseCheckCommand(s string) (job.LeaseCheck, error) {
	words := strings.Fields(s)
	if len(words) > 0 && words[0] != "run" && words[0] != "buzz" {
		words = words[1:]
	}
	if len(words) >= 3 && words[0] == "buzz" && words[1] == "--record" {
		c := job.LeaseCheck{Script: words[2]}
		if rest := words[3:]; len(rest) > 0 {
			if rest[0] != "--" {
				return job.LeaseCheck{}, &CheckCommandError{Line: s, Script: true}
			}
			c.Args = rest[1:]
		}
		return c, nil
	}
	if len(words) == 0 || words[0] != "run" {
		return job.LeaseCheck{}, &CheckCommandError{Line: s}
	}
	return job.ParseLeaseCheck(strings.Join(words[1:], " "))
}

// CheckCommandError is a line [ParseCheckCommand] refuses. Script says it began as a
// script check.
type CheckCommandError struct {
	Line   string
	Script bool
}

func (e *CheckCommandError) Error() string {
	if e.Script {
		return fmt.Sprintf("a script check line is `magus buzz --record <script> [-- args]` and %q is not one", e.Line)
	}
	return fmt.Sprintf("a check line is `magus run <target> <project> [-- args]` and %q is not one", e.Line)
}

// RecordedCommand renders the command a captured run recorded, its spell qualifying the
// target.
func RecordedCommand(a job.CapturedRun) string {
	target := a.Target
	if a.Spell != "" {
		target = fmt.Sprintf("%s::%s", a.Spell, target)
	}
	return CheckCommand(job.LeaseCheck{Target: target, Project: a.Project, Args: a.ExtraArgs})
}
