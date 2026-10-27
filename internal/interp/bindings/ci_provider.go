package bindings

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/egladman/magus/internal/ci/annotate"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/interp/bindings/gen/providerops"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// This file lives in the bindings layer (not internal/ci/annotate) because
// a spell-backed CI provider needs the Buzz VM to run the spell's handler
// ops. It registers itself with the annotate package at init, so core can
// select a spell provider without linking the VM: the same indirection
// remote_cache.go uses for cache backends.
func init() { //nolint:gochecknoinits // internal/ci/annotate cannot link the Buzz VM; the opener registers from the layer that can
	annotate.RegisterOpener(openSpellAnnotator)
}

// ciProviderSpell names the spell a magusfile selected via
// magus.ci.provider(<spell handle>). Empty means none, in which case the
// built-in providers apply.
var (
	ciProviderMu   sync.RWMutex
	ciProviderName string
)

// SetCIProvider records the spell a magusfile selected as its CI provider.
func SetCIProvider(name string) {
	ciProviderMu.Lock()
	defer ciProviderMu.Unlock()
	ciProviderName = name
}

// openSpellAnnotator resolves the selected spell, if any, into an
// Annotator. Returning nil leaves the built-in providers to apply.
func openSpellAnnotator() annotate.Annotator {
	ciProviderMu.RLock()
	name := ciProviderName
	ciProviderMu.RUnlock()
	if name == "" {
		return nil
	}
	drv, ok := project.DefaultSpellRegistry().Lookup(name)
	if !ok {
		return nil
	}
	return &spellAnnotator{drv: drv}
}

// spellAnnotator adapts a spell to the [annotate.Annotator] contract. The spell is an
// ordinary magus spell exposing these provider ops, all optional:
//
//	enabled(target, Empty) > Ack                  active here?
//	group_start(target, CIGroup) > Ack            open a fold
//	group_end(target, CIGroupEnd) > Ack           close it
//	annotate(target, CIAnnotation) > Ack          raise a notice
//	quote_prefixes(target, Empty) > Strings       line prefixes that introduce a command
//	last_green_run(target, Empty) > GreenRun      the newest run that passed
//
// The adapter has no provider knowledge, so the binary stays CI-agnostic
// and a new provider is a spell rather than a change to magus.
//
// Every op here fires at most once per project (a group around a failure)
// or once per run (enabled, quote_prefixes), never per log line. That is
// what makes crossing into the VM affordable, and why quoting is declared
// as a prefix list read once rather than a hook called per line.
type spellAnnotator struct {
	drv spells.Driver

	mu          sync.Mutex
	activeKnown bool
	active      bool
	prefixes    []string
	prefixKnown bool
}

// opTimeout bounds every spell op. These are meant to be a few bytes of
// formatting, so any op still running after this is misbehaving, and a
// provider spell is third-party code that may call out to a network the
// build has no reason to wait on. Exceeding it degrades to "no
// annotation", never to a hung build.
const opTimeout = 5 * time.Second

// spellContext is the context a spell op runs under.
//
// The Annotator methods take no context: the annotator is reached from a
// deferred failure dump that genuinely has none to thread. Rather than
// run unbounded, each op gets its own deadline, so a spell that blocks
// costs one timeout instead of the whole run. Cancel is returned so the
// caller releases the timer.
func spellContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), opTimeout)
}

// Active probes the spell's optional enabled() op once and caches it, so
// a provider that no-ops outside its environment costs one probe per
// build rather than one per annotation. A probe *error* is not cached: a
// VM hiccup should not disable annotations for the whole build.
func (a *spellAnnotator) Active() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeKnown {
		return a.active
	}
	ctx, cancel := spellContext()
	defer cancel()
	op := providerops.CIEnabled
	ack, exported, err := CallOp(ctx, a.drv, op, providerTarget(ctx, op.Name, ""), types.Empty{})
	if err != nil {
		return false
	}
	// A provider that declares no enabled() is active wherever it was wired.
	a.active = !exported || ack.OK
	a.activeKnown = true
	return a.active
}

func (a *spellAnnotator) StartGroup(g annotate.Group) error {
	g = annotate.SanitizeGroup(g)
	return callCI(a.drv, providerops.GroupStart, types.CIGroup{ID: g.ID, Title: g.Title, Collapsed: g.Collapsed})
}

func (a *spellAnnotator) EndGroup(id string) error {
	return callCI(a.drv, providerops.GroupEnd, types.CIGroupEnd{ID: id})
}

func (a *spellAnnotator) Annotate(an annotate.Annotation) error {
	// Bound and de-fang before it crosses into third-party code: Message
	// carries a failing process's own output. See annotate.Annotation.
	an = annotate.Sanitize(an)
	return callCI(a.drv, providerops.Annotate, types.CIAnnotation{
		Level: an.Level.String(), Message: an.Message, Title: an.Title, Code: an.Code, File: an.File,
		Line: an.Line, EndLine: an.EndLine, Col: an.Col, EndCol: an.EndCol,
	})
}

// runQueryTimeout bounds last_green_run alone. Unlike the formatting ops
// under opTimeout, answering it means a round trip to the provider's API,
// so it gets API headroom, and it runs once per plan, never per line.
const runQueryTimeout = 15 * time.Second

// LastGreenRun implements [annotate.RunSource] over the spell's optional
// last_green_run op. Any failure (an undeclared op, an empty answer, a malformed
// record, a timeout) reads as "no green run": inheritance is an optimization, never
// a capability.
func (a *spellAnnotator) LastGreenRun() (annotate.GreenRun, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), runQueryTimeout)
	defer cancel()
	op := providerops.LastGreenRun
	got, _, err := CallOp(ctx, a.drv, op, providerTarget(ctx, op.Name, ""), types.Empty{})
	if err != nil || got.Run == "" || got.Commit == "" {
		return annotate.GreenRun{}, false
	}
	return annotate.GreenRun{Run: got.Run, Commit: got.Commit}, true
}

// Defang neutralizes the provider's own command syntax in replayed output.
//
// The spell declares its command prefixes once, via quote_prefixes, and
// the matching runs here in Go. That split is the point: this is called
// for every replayed line of a failing build's output, so asking the
// spell per line would cost far more than the feature is worth, while
// asking it once costs nothing and keeps the syntax knowledge in the
// spell where it belongs.
func (a *spellAnnotator) Defang(text string) string {
	return annotate.DefangWith(text, a.quotePrefixes())
}

// quotePrefixes reads the spell's optional quote_prefixes op once. A
// spell that declares none contributes no prefixes, and replayed output
// passes through untouched.
func (a *spellAnnotator) quotePrefixes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.prefixKnown {
		return a.prefixes
	}
	ctx, cancel := spellContext()
	defer cancel()
	op := providerops.QuotePrefixes
	got, _, err := CallOp(ctx, a.drv, op, providerTarget(ctx, op.Name, ""), types.Empty{})
	if err != nil {
		return nil // transient: do not latch an empty list
	}
	// A provider could otherwise make magus match an unbounded list against every
	// replayed line, or hand over an empty prefix that matches all of them.
	a.prefixes = annotate.ClampPrefixes(got.Items)
	a.prefixKnown = true
	return a.prefixes
}

// callCI calls an optional formatting op, treating an undeclared op as success: a spell
// implements only the verbs its provider supports, and annotations do not exist at all on
// some providers. Every error is a real failure (a handler that raised, a spell that would
// not load, an op that timed out) and is reported; swallowing it made a broken provider
// report success for the life of the build. What the op answers is not read.
func callCI[Req any](drv spells.Driver, op bindgen.ProviderOp[Req, types.Ack], req Req) error {
	ctx, cancel := spellContext()
	defer cancel()
	if _, _, err := CallOp(ctx, drv, op, providerTarget(ctx, op.Name, ""), req); err != nil {
		return fmt.Errorf("ci provider op %q: %w", op.Name, err)
	}
	return nil
}

var _ annotate.Annotator = (*spellAnnotator)(nil)
