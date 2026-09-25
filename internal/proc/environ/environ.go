// Package environ is one run's view of the environment: the process environment with the
// run's own env\set and env\unset layered on top. A leaf, like sockdir, so the env host
// module can reach it from the wasm build where internal/proc/run does not compile.
package environ

import (
	"context"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
)

// Overlay is one run's own changes to the environment: what env\set, env\unset and
// env\load_dotenv wrote. The process environment is shared by every run the server holds,
// so writing there would let one workspace's run change another's children.
//
// Safe for concurrent use. A nil *Overlay has no changes.
type Overlay struct {
	mu   sync.RWMutex
	vars map[string]*string // nil: unset by this run
}

type overlayKey struct{}

// With returns ctx carrying a fresh, empty overlay. Each invocation installs one at its
// root, so what its env\set writes is seen by its own reads and children only.
func With(ctx context.Context) context.Context {
	return context.WithValue(ctx, overlayKey{}, &Overlay{vars: map[string]*string{}})
}

// From returns the overlay on ctx, or nil when none was installed.
func From(ctx context.Context) *Overlay {
	o, _ := ctx.Value(overlayKey{}).(*Overlay)
	return o
}

// Set records name=value for this run.
func (o *Overlay) Set(name, value string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.vars[name] = &value
}

// Unset records name as removed for this run.
func (o *Overlay) Unset(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.vars[name] = nil
}

// Lookup reports name's value in the overlay, whether it is set there, and whether the
// overlay says anything about name at all.
func (o *Overlay) Lookup(name string) (value string, set, known bool) {
	if o == nil {
		return "", false, false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	v, known := o.vars[name]
	if !known || v == nil {
		return "", false, known
	}
	return *v, true, true
}

// Apply returns env ("KEY=value" entries) with the overlay's changes made, and env itself
// when there are none.
func (o *Overlay) Apply(env []string) []string {
	if o == nil {
		return env
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if len(o.vars) == 0 {
		return env
	}
	names := slices.Sorted(maps.Keys(o.vars))
	out := make([]string, 0, len(env)+len(names))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if _, changed := o.vars[name]; !changed {
			out = append(out, kv)
		}
	}
	for _, name := range names {
		if v := o.vars[name]; v != nil {
			out = append(out, name+"="+*v)
		}
	}
	return out
}

// Lookup is os.LookupEnv as the run on ctx sees it: its overlay first, then the process.
func Lookup(ctx context.Context, name string) (string, bool) {
	if v, set, known := From(ctx).Lookup(name); known {
		return v, set
	}
	return os.LookupEnv(name)
}

// Of is os.Environ as the run on ctx sees it.
func Of(ctx context.Context) []string {
	return From(ctx).Apply(os.Environ())
}
