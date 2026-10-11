package job

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

// CheckVerdict is a [CheckGrader]'s answer. Denied is false for a check the guard lets the
// holder run, and for one the grader could not judge: a grader that cannot reach a verdict
// on the check accepts and says why in NotGraded, because a refusal must come from a
// verdict, not from a failure to reach one.
type CheckVerdict struct {
	Denied bool
	// Rule names the rule that refused, as the guard prints it.
	Rule string
	// Reason is the guard's own wording, quoted back to the person forking.
	Reason string
	// NotGraded says why no verdict on the check was reached, empty when one was.
	NotGraded string
}

// CheckGrader judges row's check the way the guard will judge row's holder running it in
// the checkout at root, against that workspace's rules. The guard lives above this
// package, so the CLI supplies the implementation. One grader serves every checkout a
// door opens stores for, so it must be safe for concurrent use.
type CheckGrader func(ctx context.Context, root string, row types.Job) CheckVerdict

// CompilingCheck is the check a refusal recommends: it compiles the workspace without
// linking its binary, so a worker the guard keeps from building can still run it. The
// grader also judges it to tell a deny of the declared check from a deny of every command
// the worker runs.
func CompilingCheck() types.LeaseCheck {
	return types.LeaseCheck{Target: "go::go-vet", Project: "."}
}

// RefuseDeniedCheck refuses a job whose declared check the guard would deny its holder. The
// holder runs the check under the lease, so a denied one leaves the job unable to verify
// itself and ends it unverified. A Store with no grader, and a job with no check, refuse
// nothing.
func RefuseDeniedCheck(ctx context.Context, store *Store, row types.Job) error {
	if store == nil || store.grader == nil || row.Check == nil {
		return nil
	}
	verdict := store.grader(ctx, store.root, row)
	if !verdict.Denied {
		return nil
	}
	rule := ""
	if verdict.Rule != "" {
		rule = " (" + verdict.Rule + ")"
	}
	compiling := CompilingCheck()
	recommended := compiling.Target + " " + compiling.Project
	return fmt.Errorf("job: %s's check `%s` would be denied to its worker by the guard%s: %s,"+
		" and the holder runs the check under its lease, so a denied one can never verify the job,"+
		" declare a target that compiles without building the binary, such as `%s`,"+
		" with `magus job fork %s --check '%s'`",
		row.ID, row.Check.String(), rule, strings.Join(strings.Fields(verdict.Reason), " "),
		recommended, row.ID, recommended)
}
