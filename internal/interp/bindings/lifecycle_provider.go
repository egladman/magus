package bindings

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// Registered at init for the reason workspace_provider.go gives: running the provider
// means running a Buzz spell, which only this layer can do.
func init() {
	workspace.RegisterLifecycleRunner(runLifecycleProvider)
}

// lifecycleDeadline bounds one list_lifecycles call, the budget ci_provider.go's
// runQueryTimeout gives a network question whose answer only adds information. A provider
// that runs out of it reads as unreached.
const lifecycleDeadline = 15 * time.Second

// runLifecycleProvider invokes spellName's list_lifecycles contract for keys and decodes
// what it returns.
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
	resp, err := drv.Invoke(ctx, spells.InvokeRequest{
		Target: spells.ListLifecyclesContract,
		Dir:    root,
		Params: map[string]any{"keys": keys},
	})
	if err != nil {
		// A throw is the provider failing to reach its source, the review_threads posture:
		// what it adds is optional, so an unreachable host is a state and not an error.
		return nil, fmt.Errorf("%w: spell %q: %s: %w", workspace.ErrLifecycleUnreached, spellName, spells.ListLifecyclesContract, err)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("spell %q is wired as the lifecycle provider but its %s(target, cb) returned nothing, it must be exported and must return a list of Lifecycle (an empty list if it knows none of the keys)",
			spellName, spells.ListLifecyclesContract)
	}
	items, ok := resp.Data.([]any)
	if !ok {
		return nil, fmt.Errorf("spell %q: %s returned %T, want a list of Lifecycle", spellName, spells.ListLifecyclesContract, resp.Data)
	}
	out := make([]spells.Lifecycle, 0, len(items))
	for i, item := range items {
		l, err := decodeLifecycle(spellName, keys, i, item)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(out, func(o spells.Lifecycle) bool { return o.Key == l.Key }) {
			return nil, fmt.Errorf("spell %q: %s answered key %q twice", spellName, spells.ListLifecyclesContract, l.Key)
		}
		out = append(out, l)
	}
	return out, nil
}

// decodeLifecycle turns one returned Lifecycle into the typed record, naming the spell, the
// key and the field of anything wrong. Strict for the reason decodeProvidedProject is: a
// silently zeroed date is an end of life nobody was told about.
func decodeLifecycle(spellName string, keys []string, index int, item any) (spells.Lifecycle, error) {
	where := fmt.Sprintf("spell %q: %s[%d]", spellName, spells.ListLifecyclesContract, index)
	m, ok := item.(map[string]any)
	if !ok {
		return spells.Lifecycle{}, fmt.Errorf("%s is %T, want a Lifecycle", where, item)
	}
	var l spells.Lifecycle
	var err error
	if l.Key, err = strField(m, "key", where); err != nil {
		return spells.Lifecycle{}, err
	}
	if !slices.Contains(keys, l.Key) {
		return spells.Lifecycle{}, fmt.Errorf("%s: key %q was not asked for (asked: %v)", where, l.Key, keys)
	}
	where = fmt.Sprintf("spell %q: %s key %q", spellName, spells.ListLifecyclesContract, l.Key)
	if l.Source, err = strField(m, "source", where); err != nil {
		return spells.Lifecycle{}, err
	}
	if l.Source == "" {
		return spells.Lifecycle{}, fmt.Errorf("%s: field \"source\" is empty, name the URL the answer was read from", where)
	}
	if l.AsOf, err = strField(m, "asOf", where); err != nil {
		return spells.Lifecycle{}, err
	}
	if _, perr := time.Parse(time.RFC3339, l.AsOf); perr != nil {
		return spells.Lifecycle{}, fmt.Errorf("%s: field \"asOf\" is %q, want upstream's last-modified time in RFC 3339", where, l.AsOf)
	}
	raw, present := m["cycles"]
	if !present || raw == nil {
		return l, nil
	}
	rows, ok := raw.([]any)
	if !ok {
		return spells.Lifecycle{}, fmt.Errorf("%s: field \"cycles\" is %T, want [ReleaseCycle]", where, raw)
	}
	for i, row := range rows {
		c, err := decodeReleaseCycle(fmt.Sprintf("%s: cycles[%d]", where, i), row)
		if err != nil {
			return spells.Lifecycle{}, err
		}
		l.Cycles = append(l.Cycles, c)
	}
	return l, nil
}

func decodeReleaseCycle(where string, row any) (spells.ReleaseCycle, error) {
	m, ok := row.(map[string]any)
	if !ok {
		return spells.ReleaseCycle{}, fmt.Errorf("%s is %T, want a ReleaseCycle", where, row)
	}
	var c spells.ReleaseCycle
	var err error
	if c.Cycle, err = strField(m, "cycle", where); err != nil {
		return spells.ReleaseCycle{}, err
	}
	if c.Cycle == "" {
		return spells.ReleaseCycle{}, fmt.Errorf("%s: field \"cycle\" is empty", where)
	}
	for _, f := range []struct {
		key string
		dst *string
	}{{"released", &c.Released}, {"eol", &c.EOL}} {
		if *f.dst, err = strField(m, f.key, where); err != nil {
			return spells.ReleaseCycle{}, err
		}
		if *f.dst == "" {
			continue
		}
		if _, perr := time.Parse(time.DateOnly, *f.dst); perr != nil {
			return spells.ReleaseCycle{}, fmt.Errorf("%s: field %q is %q, want a YYYY-MM-DD date or empty", where, f.key, *f.dst)
		}
	}
	if c.Latest, err = strField(m, "latest", where); err != nil {
		return spells.ReleaseCycle{}, err
	}
	if v, present := m["lts"]; present && v != nil {
		b, ok := v.(bool)
		if !ok {
			return spells.ReleaseCycle{}, fmt.Errorf("%s: field \"lts\" is %T, want bool", where, v)
		}
		c.LTS = b
	}
	return c, nil
}
