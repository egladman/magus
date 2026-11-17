package job

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// CheckCommand renders c as the command a person types and a job stores: [checkArgv]
// joined by spaces. Running it satisfies the check. The charms it executes under,
// default_charms included, are the charms the check is resolved to (ranCheck).
func CheckCommand(c job.LeaseCheck) string {
	return strings.Join(checkArgv(c), " ")
}

// checkArgv is c as the argv that runs it, led by the binary as this process was invoked
// (see [hint.BinaryName]). It is the one rendering of a check: [CheckCommand] joins it, and
// a witnessed run executes it, so an argument holding a space stays one word there.
func checkArgv(c job.LeaseCheck) []string {
	argv := []string{hint.BinaryName()}
	switch {
	case c.Script != "" && c.Test:
		argv = append(argv, "buzz", "-t", c.Script)
	case c.Script != "":
		argv = append(argv, "buzz", "--record", c.Script)
	default:
		argv = append(argv, "run", c.Target, cmp.Or(c.Project, "."))
		if c.NoDefaultCharms {
			argv = append(argv, "--no-default-charms")
		}
	}
	if len(c.Args) > 0 {
		argv = append(append(argv, "--"), c.Args...)
	}
	return argv
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
				return job.LeaseCheck{}, &checkCommandError{Line: s, Script: true}
			}
			c.Args = rest[1:]
		}
		return c, nil
	}
	if len(words) == 0 || words[0] != "run" {
		return job.LeaseCheck{}, &checkCommandError{Line: s}
	}
	return job.ParseLeaseCheck(strings.Join(words[1:], " "))
}

// checkCommandError is a line [ParseCheckCommand] refuses. Script says it began as a
// script check.
type checkCommandError struct {
	Line   string
	Script bool
}

func (e *checkCommandError) Error() string {
	if e.Script {
		return fmt.Sprintf("a script check line is `magus buzz --record <script> [-- args]` and %q is not one", e.Line)
	}
	return fmt.Sprintf("a check line is `magus run <target> <project> [-- args]` and %q is not one", e.Line)
}

// recordedCommand renders the command a captured run recorded, its spell qualifying the
// target.
func recordedCommand(a job.CapturedRun) string {
	target := a.Target
	if a.Spell != "" {
		target = fmt.Sprintf("%s::%s", a.Spell, target)
	}
	return CheckCommand(job.LeaseCheck{Target: target, Project: a.Project, Args: a.ExtraArgs})
}
