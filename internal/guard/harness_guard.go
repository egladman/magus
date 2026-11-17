package guard

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

// RefreshPolicySnapshot loads the guard's policy with no hook deadline, so the
// hooks that follow judge from its snapshot and warm bytecode. A policy
// that does not load is the hooks' to report; install still succeeds.
func RefreshPolicySnapshot(ctx context.Context, w Wiring) {
	if err := WritePolicySnapshot(ctx, w); err != nil {
		slog.With(attr.Component("agent harness install")).WarnContext(ctx,
			"the guard policy snapshot was not written, so the first hook loads the policy", attr.Error(err))
	}
}

// CheckoutLease resolves the lease a CLI call acts under in root's checkout through
// job.RunLease: the checkout's record, else the claim of the process that asked for the
// work (a forwarded call's lease, else BAGGAGE). A CLI process knows no host session or
// subagent, so those sources never answer here. place finds root's job location.
func CheckoutLease(ctx context.Context, root string, place func(root string) (job.Location, error)) (string, types.LeaseSource, error) {
	loc, err := place(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve the checkout's lease: %w", err)
	}
	lease, from := job.RunLease(ctx, loc)
	return lease, from, nil
}
