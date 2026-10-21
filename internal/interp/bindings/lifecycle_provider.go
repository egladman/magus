package bindings

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interp/bindings/gen/providerops"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Registered at init for the reason workspace_provider.go gives: running the provider
// means running a Buzz spell, which only this layer can do.
func init() { //nolint:gochecknoinits // internal/workspace cannot link the Buzz VM; the runner registers from the layer that can
	workspace.RegisterLifecycleRunner(runLifecycleProvider)
}

// lifecycleDeadline bounds one list_lifecycles call, the budget ci_provider.go's
// runQueryTimeout gives a network question whose answer only adds information. A provider
// that runs out of it reads as unreached.
const lifecycleDeadline = 15 * time.Second

// runLifecycleProvider calls spellName's list_lifecycles op for keys and checks what it
// returns.
//
// MAGUS_OFFLINE is checked HERE, before the VM runs: std/http would refuse the provider's
// requests anyway, but answering ErrLifecycleOffline up front is what lets describe tools
// replay its stored answer instead of reporting the provider unreached.
func runLifecycleProvider(ctx context.Context, spellName, root string, keys []string) ([]spells.Lifecycle, error) {
	if config.Offline() {
		return nil, workspace.ErrLifecycleOffline
	}
	drv, ok := project.DefaultSpellRegistry().Lookup(spellName)
	if !ok {
		return nil, fmt.Errorf("spell %q is not registered, import it before wiring it as the lifecycle provider", spellName)
	}
	ctx, cancel := context.WithTimeout(ctx, lifecycleDeadline)
	defer cancel()
	op := providerops.ListLifecycles
	got, exported, err := CallOp(ctx, drv, op, providerTarget(ctx, op.Name, root), types.LifecycleQuery{Keys: keys})
	if err != nil {
		if !exported || errors.Is(err, errMalformedAnswer) {
			return nil, err
		}
		// A throw is the provider failing to reach its source, the review_threads posture:
		// what it adds is optional, so an unreachable host is a state and not an error.
		return nil, fmt.Errorf("%w: %w", workspace.ErrLifecycleUnreached, err)
	}
	if !exported {
		return nil, fmt.Errorf("spell %q is wired as the lifecycle provider but does not export %s(target: Target, req: LifecycleQuery) > [Lifecycle]",
			spellName, op.Name)
	}
	for i, l := range got {
		if err := checkLifecycle(spellName, keys, i, l); err != nil {
			return nil, err
		}
		if slices.ContainsFunc(got[:i], func(o spells.Lifecycle) bool { return o.Key == l.Key }) {
			return nil, fmt.Errorf("spell %q: %s answered key %q twice", spellName, op.Name, l.Key)
		}
	}
	return got, nil
}

// checkLifecycle holds one returned Lifecycle to what its fields mean, naming the spell,
// the key and the field of anything wrong: a silently zeroed date is an end of life
// nobody was told about.
func checkLifecycle(spellName string, keys []string, index int, l spells.Lifecycle) error {
	if !slices.Contains(keys, l.Key) {
		return fmt.Errorf("spell %q: %s[%d]: key %q was not asked for (asked: %v)", spellName, spells.ListLifecyclesContract, index, l.Key, keys)
	}
	where := fmt.Sprintf("spell %q: %s key %q", spellName, spells.ListLifecyclesContract, l.Key)
	if l.Source == "" {
		return fmt.Errorf("%s: field \"source\" is empty, name the URL the answer was read from", where)
	}
	if _, err := time.Parse(time.RFC3339, l.AsOf); err != nil {
		return fmt.Errorf("%s: field \"asOf\" is %q, want upstream's last-modified time in RFC 3339", where, l.AsOf)
	}
	for i, c := range l.Cycles {
		at := fmt.Sprintf("%s: cycles[%d]", where, i)
		if c.Cycle == "" {
			return fmt.Errorf("%s: field \"cycle\" is empty", at)
		}
		for _, f := range []struct{ key, date string }{{"released", c.Released}, {"eol", c.EOL}} {
			if f.date == "" {
				continue
			}
			if _, err := time.Parse(time.DateOnly, f.date); err != nil {
				return fmt.Errorf("%s: field %q is %q, want a YYYY-MM-DD date or empty", at, f.key, f.date)
			}
		}
	}
	return nil
}
