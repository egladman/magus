package job

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// parseLeaseCheck reads `<target> <project> [-- args]`, the shape a person types and the
// shape a record is written in. The project defaults to ".".
//
// A flag anywhere is REFUSED rather than read as a word: a check is an identity the output
// store can be asked about, and `-o json` in it either names a target nobody ran or
// silently shifts the project.
//
// The one flag read is --no-default-charms, which the record carries as NoDefaultCharms.
func parseLeaseCheck(s string) (types.LeaseCheck, error) {
	words := strings.Fields(s)
	var args []string
	if i := slices.Index(words, "--"); i >= 0 {
		words, args = words[:i], words[i+1:]
	}
	var noDefaultCharms bool
	positional := words[:0:0]
	for _, w := range words {
		if w == "--no-default-charms" {
			noDefaultCharms = true
			continue
		}
		positional = append(positional, w)
		if strings.HasPrefix(w, "-") {
			return types.LeaseCheck{}, &checkError{Line: s, Reason: fmt.Sprintf("%q carries the flag %s,"+
				" flags belong after `--`, where they reach the tool rather than the runner", s, w)}
		}
	}
	words = positional
	switch {
	case len(words) == 0:
		return types.LeaseCheck{}, &checkError{Line: s, Reason: "this one names no target"}
	case len(words) > 2:
		return types.LeaseCheck{}, &checkError{Line: s, Reason: fmt.Sprintf("%q carries %d words before any `--`", s, len(words))}
	}
	c := types.LeaseCheck{Target: words[0], Project: ".", Args: args, NoDefaultCharms: noDefaultCharms}
	if len(words) == 2 {
		c.Project = words[1]
	}
	c.Project = path.Clean(c.Project)
	return c, nil
}

// checkError is a check [parseLeaseCheck] refuses: the line it was given, and why.
type checkError struct {
	Line   string
	Reason string
}

func (e *checkError) Error() string {
	return "a check is `<target> <project> [-- args]` and " + e.Reason
}

// checkShapeError is a check that names both a script and a target, or gives a script
// what only a target takes, or a target what only a script takes.
type checkShapeError struct {
	// Field is the member at fault: "script" or "test".
	Field  string
	Reason string
}

func (e *checkShapeError) Error() string { return e.Reason }

// validateLeaseCheck is c as a store keeps it, or why c is no check. A target check
// names its project, "." when it named none, cleaned; a script check keeps only its
// script, args and test, since a recorded script run belongs to the workspace.
func validateLeaseCheck(c types.LeaseCheck) (types.LeaseCheck, error) {
	switch {
	case c.Script == "" && c.Test:
		return types.LeaseCheck{}, &checkShapeError{Field: "test",
			Reason: fmt.Sprintf("check target %q sets test, which runs a script's tests and so belongs to a script check", c.Target)}
	case c.Script != "" && c.Target != "":
		return types.LeaseCheck{}, &checkShapeError{Field: "script",
			Reason: fmt.Sprintf("check names target %q and script %q, a check names one", c.Target, c.Script)}
	case c.Script != "" && c.Project != "" && c.Project != ".":
		return types.LeaseCheck{}, &checkShapeError{Field: "script",
			Reason: fmt.Sprintf("check script %q names project %q, a recorded script run belongs to the workspace", c.Script, c.Project)}
	case c.Script != "" && c.NoDefaultCharms:
		return types.LeaseCheck{}, &checkShapeError{Field: "script",
			Reason: fmt.Sprintf("check script %q sets no_default_charms, a script runs under no charms", c.Script)}
	case c.Script != "":
		return types.LeaseCheck{Script: c.Script, Args: c.Args, Test: c.Test}, nil
	}
	parsed, err := parseLeaseCheck(fmt.Sprintf("%s %s", c.Target, c.Project))
	if err != nil {
		return types.LeaseCheck{}, err
	}
	parsed.Args = c.Args
	parsed.NoDefaultCharms = c.NoDefaultCharms
	return parsed, nil
}

// sameLeaseCheck reports whether a and b declare the same check. Two nil checks are the
// same, and a nil check is not the same as any declared one.
func sameLeaseCheck(a, b *types.LeaseCheck) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Target == b.Target && a.Script == b.Script && cmp.Or(a.Project, ".") == cmp.Or(b.Project, ".") &&
		a.NoDefaultCharms == b.NoDefaultCharms && a.Test == b.Test && slices.Equal(a.Args, b.Args)
}

// rawCheck is c as the job store keeps it: validated, then encoded.
func rawCheck(c types.LeaseCheck) (job.Check, error) {
	valid, err := validateLeaseCheck(c)
	if err != nil {
		return job.Check{}, err
	}
	return types.EncodeLeaseCheck(valid)
}

// rowCheck is row's own check and whether it declares one.
func rowCheck(row job.Job) (types.LeaseCheck, bool, error) {
	if row.Check.IsZero() {
		return types.LeaseCheck{}, false, nil
	}
	c, err := types.DecodeLeaseCheck(row.Check)
	if err != nil {
		return types.LeaseCheck{}, false, fmt.Errorf("job %s: %w", row.ID, err)
	}
	return c, true, nil
}

// PostconditionCheck is g's check. A postcondition that carries none is refused with
// [types.ErrNoLeaseCheck].
func PostconditionCheck(g job.Postcondition) (types.LeaseCheck, error) {
	c, err := types.DecodeLeaseCheck(g.Check)
	if err != nil {
		return types.LeaseCheck{}, fmt.Errorf("postcondition %s: %w", g.ID, err)
	}
	return c, nil
}

// leaseChecks is how magus's job store reads the checks it keeps: as [types.LeaseCheck].
type leaseChecks struct{}

func (leaseChecks) Validate(stored job.Check) (job.Check, error) {
	c, err := types.DecodeLeaseCheck(stored)
	if err != nil {
		return job.Check{}, err
	}
	return rawCheck(c)
}

func (leaseChecks) Equal(a, b job.Check) (bool, error) {
	ca, err := types.DecodeLeaseCheck(a)
	if err != nil {
		return false, err
	}
	cb, err := types.DecodeLeaseCheck(b)
	if err != nil {
		return false, err
	}
	return sameLeaseCheck(&ca, &cb), nil
}

func (leaseChecks) Render(stored job.Check) (string, error) {
	c, err := types.DecodeLeaseCheck(stored)
	if err != nil {
		return "", err
	}
	return CheckCommand(c), nil
}

// postconditionCheck is a check postcondition with its check read.
type postconditionCheck struct {
	Postcondition job.Postcondition
	Check         types.LeaseCheck
}

// readPostconditionChecks is every postcondition of postconditions that carries a check, each check read. A check that
// does not read refuses the whole set: the store keeps only checks that do.
func readPostconditionChecks(postconditions []job.Postcondition) ([]postconditionCheck, error) {
	var out []postconditionCheck
	for _, g := range postconditions {
		if g.Check.IsZero() {
			continue
		}
		c, err := PostconditionCheck(g)
		if err != nil {
			return nil, err
		}
		out = append(out, postconditionCheck{Postcondition: g, Check: c})
	}
	return out, nil
}

// effectiveChecks is readPostconditionChecks row's effective postconditions, its own check first.
func effectiveChecks(row job.Job) ([]postconditionCheck, error) {
	checks, err := readPostconditionChecks(row.EffectivePostconditions())
	if err != nil {
		return nil, fmt.Errorf("job %s: %w", row.ID, err)
	}
	return checks, nil
}

// renderPostconditionCheck is g's check as the command that runs it, or a sentence saying it does
// not read: a blank would pass for a postcondition with no check.
func renderPostconditionCheck(g job.Postcondition) string {
	c, err := PostconditionCheck(g)
	if err != nil {
		return fmt.Sprintf("(a check that does not read: %v)", err)
	}
	return CheckCommand(c)
}

// sameStoredCheck reports whether two stored checks declare the same one: two that
// declare none are the same, and two that cannot be compared are not, so an unreadable
// check is judged again.
func sameStoredCheck(a, b job.Check) bool {
	if a.IsZero() || b.IsZero() {
		return a.IsZero() == b.IsZero()
	}
	same, err := leaseChecks{}.Equal(a, b)
	return err == nil && same
}
