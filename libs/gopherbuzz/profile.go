package buzz

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Profile records the compile phases and import resolutions a session reports.
// It is a CompileObserver: add it with Session.AddCompileObserver, or put it
// on a context with WithProfile and let the host add it.
//
// Nothing records until a host adds the profile. A hook that does not ask
// for one pays no clocks beyond the nil check the session already makes.
type Profile struct {
	mu     sync.Mutex
	events []profileEvent
}

type profileEvent struct {
	name string
	d    time.Duration
}

func NewProfile() *Profile { return &Profile{} }

func (p *Profile) Phase(phase CompilePhase, elapsed time.Duration, _ error) {
	if p == nil {
		return
	}
	p.record(phase.String(), elapsed)
}

// Import implements CompileObserver. The line names the path and how it resolved,
// because a slow "file" and a slow "native" are different fixes.
func (p *Profile) Import(importPath string, outcome ImportOutcome, elapsed time.Duration, _ error) {
	if p == nil {
		return
	}
	p.record("import "+importPath+" "+outcome.String(), elapsed)
}

func (p *Profile) record(name string, d time.Duration) {
	p.mu.Lock()
	p.events = append(p.events, profileEvent{name, d})
	p.mu.Unlock()
}

// Report renders the events slowest first. Empty when nothing was recorded, so a
// caller can print it unconditionally and stay silent on a run that attached a
// profile and then did no Buzz work.
func (p *Profile) Report() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	events := slices.Clone(p.events)
	p.mu.Unlock()
	if len(events) == 0 {
		return ""
	}
	slices.SortFunc(events, func(a, b profileEvent) int {
		return cmp.Compare(b.d, a.d)
	})
	var b strings.Builder
	fmt.Fprintf(&b, "buzz profile:\n")
	for _, e := range events {
		fmt.Fprintf(&b, "  %12s  %s\n", e.d.Round(time.Microsecond), e.name)
	}
	return b.String()
}

type profileCtxKey struct{}

// WithProfile returns ctx carrying p. A host that loads Buzz from this ctx
// (a magusfile, a spell, a script) adds p and records into it.
func WithProfile(ctx context.Context, p *Profile) context.Context {
	if p == nil {
		return ctx
	}
	return context.WithValue(ctx, profileCtxKey{}, p)
}

// ProfileFromContext returns the profile on ctx, or nil.
func ProfileFromContext(ctx context.Context) *Profile {
	p, _ := ctx.Value(profileCtxKey{}).(*Profile)
	return p
}
