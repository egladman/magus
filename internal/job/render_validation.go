package job

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// RenderValidation writes v for a person: every problem numbered, each as
// `[MGSxxxx] <field>: <message>` with its fix beneath, then the record as it would be
// stored when dryRun is set, the waived problems with their reasons, and each check that
// could not run. A validation with nothing to say writes nothing, except under dryRun,
// which says the record is clean.
//
// Fork, apply and describe share it, so one record reads the same at every door.
func RenderValidation(w io.Writer, v types.JobValidation, dryRun bool) {
	switch {
	case len(v.Problems) > 0:
		noun := "problems"
		if len(v.Problems) == 1 {
			noun = "problem"
		}
		fmt.Fprintf(w, "%d %s:\n", len(v.Problems), noun)
		for i, p := range v.Problems {
			fmt.Fprintf(w, "  %d. [%s] %s\n", i+1, p.Code, problemText(p))
			if p.Fix != "" {
				fmt.Fprintf(w, "     fix: %s\n", p.Fix)
			}
		}
	case dryRun:
		fmt.Fprintln(w, "no problems")
	}
	if dryRun {
		if stored, err := json.MarshalIndent(v.Job, "", "  "); err == nil {
			fmt.Fprintf(w, "would store:\n%s\n", stored)
		}
	}
	if len(v.Waived) > 0 {
		fmt.Fprintf(w, "waived %d:\n", len(v.Waived))
		for _, p := range v.Waived {
			line := fmt.Sprintf("  [%s] %s", p.Code, problemText(p))
			if i := slices.IndexFunc(v.Job.Waivers, func(wv types.JobWaiver) bool { return wv.Code == p.Code && wv.Subject == p.Subject }); i >= 0 {
				line += " (waived: " + v.Job.Waivers[i].Reason + ")"
			}
			fmt.Fprintln(w, line)
		}
	}
	if len(v.Unchecked) > 0 {
		fmt.Fprintln(w, "not checked:")
		for _, u := range v.Unchecked {
			fmt.Fprintf(w, "  %s\n", u)
		}
	}
}

// ApplyValidations regroups the error [Apply] returns into one validation per record, in
// the order ids name them, each record's problems carrying the text Apply gave them with
// the record id led off. It reports false when any part of err is not such a problem (an
// authorization refusal, a store that could not be read), which the caller reports as it
// is.
//
// Apply joins its problems as errors, so a problem's fix rides inside its message here
// rather than on a line of its own.
func ApplyValidations(err error, ids []string) ([]types.JobValidation, bool) {
	var leaves []error
	var walk func(error)
	walk = func(e error) {
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range joined.Unwrap() {
				walk(inner)
			}
			return
		}
		leaves = append(leaves, e)
	}
	walk(err)
	byID := map[string]*types.JobValidation{}
	for _, leaf := range leaves {
		var diag *types.DiagnosticError
		if !errors.As(leaf, &diag) {
			return nil, false
		}
		i := slices.IndexFunc(ids, func(id string) bool { return strings.HasPrefix(diag.Msg, id+": ") })
		if i < 0 {
			return nil, false
		}
		v := byID[ids[i]]
		if v == nil {
			v = &types.JobValidation{Job: types.Job{ID: ids[i]}}
			byID[ids[i]] = v
		}
		v.Problems = append(v.Problems, types.JobProblem{Code: diag.Code, Message: strings.TrimPrefix(diag.Msg, ids[i]+": ")})
	}
	var out []types.JobValidation
	for _, id := range ids {
		if v := byID[id]; v != nil {
			out = append(out, *v)
		}
	}
	return out, len(out) > 0
}

// problemText is p's message led by its field, the way a person finds it in the record.
func problemText(p types.JobProblem) string {
	if p.Field == "" {
		return p.Message
	}
	return p.Field + ": " + p.Message
}
