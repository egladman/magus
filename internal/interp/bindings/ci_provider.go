package bindings

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/egladman/magus/internal/ci/annotate"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// This file lives in the bindings layer (not internal/ci/annotate) because
// a spell-backed CI provider needs the Buzz VM to run the spell's handler
// ops. It registers itself with the annotate package at init, so core can
// select a spell provider without linking the VM - the same indirection
// remote_cache.go uses for cache backends.
func init() {
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
func openSpellAnnotator(io.Writer) annotate.Annotator {
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

// spellAnnotator adapts a spell to the [annotate.Annotator] contract. The
// spell is an ordinary magus spell exposing these handler ops, all
// optional:
//
//	enabled()                                  -> bool  active here?
//	group_start({id, title, collapsed})        -> bool  open a fold
//	group_end({id})                            -> bool  close it
//	annotate({level, message, title, code,
//	          file, line, end_line, col, end_col}) -> bool  raise a notice
//	quote_prefixes()                           -> [str] line prefixes that
//	                                                   introduce a command
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
// formatting, so any op still running after this is misbehaving - and a
// provider spell is third-party code that may call out to a network the
// build has no reason to wait on. Exceeding it degrades to "no
// annotation", never to a hung build.
const opTimeout = 5 * time.Second

// spellCtx is the context a spell op runs under.
//
// The Annotator methods take no context: the annotator is reached from a
// deferred failure dump that genuinely has none to thread. Rather than
// run unbounded, each op gets its own deadline, so a spell that blocks
// costs one timeout instead of the whole run. Cancel is returned so the
// caller releases the timer.
func spellCtx() (context.Context, context.CancelFunc) {
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
	ctx, cancel := spellCtx()
	defer cancel()
	resp, err := a.drv.Invoke(ctx, spells.InvokeRequest{Target: "enabled"})
	if err != nil {
		return false
	}
	if resp.Data == nil {
		a.active = true // no enabled() op declared -> always active
	} else if b, ok := resp.Data.(bool); ok {
		a.active = b
	} else {
		// A declared enabled() that returns a non-bool is a spell contract violation. Stay
		// inactive rather than guess: activating on a malformed signal could emit a provider's
		// markers outside its CI, the exact misfire Active exists to prevent.
		a.active = false
	}
	a.activeKnown = true
	return a.active
}

func (a *spellAnnotator) StartGroup(g annotate.Group) error {
	g = annotate.SanitizeGroup(g)
	return a.call("group_start", map[string]any{
		"id":        g.ID,
		"title":     g.Title,
		"collapsed": g.Collapsed,
	})
}

func (a *spellAnnotator) EndGroup(id string) error {
	return a.call("group_end", map[string]any{"id": id})
}

func (a *spellAnnotator) Annotate(an annotate.Annotation) error {
	// Bound and de-fang before it crosses into third-party code: Message
	// carries a failing process's own output. See annotate.Annotation.
	an = annotate.Sanitize(an)
	return a.call("annotate", map[string]any{
		"level":    an.Level.String(),
		"message":  an.Message,
		"title":    an.Title,
		"code":     an.Code,
		"file":     an.File,
		"line":     an.Line,
		"end_line": an.EndLine,
		"col":      an.Col,
		"end_col":  an.EndCol,
	})
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
	ctx, cancel := spellCtx()
	defer cancel()
	resp, err := a.drv.Invoke(ctx, spells.InvokeRequest{Target: "quote_prefixes"})
	if err != nil {
		return nil // transient: do not latch an empty list
	}
	if list, ok := resp.Data.([]any); ok {
		raw := make([]string, 0, len(list))
		for _, v := range list {
			if s, ok := v.(string); ok {
				raw = append(raw, s)
			}
		}
		// A provider could otherwise make magus match an unbounded list
		// against every replayed line, or hand over an empty prefix that
		// matches all of them.
		a.prefixes = annotate.ClampPrefixes(raw)
	}
	a.prefixKnown = true
	return a.prefixes
}

// call invokes an optional op, treating an undeclared op as success. A
// spell implements only the verbs its provider supports, and the missing
// rest are not errors: annotations do not exist at all on some providers.
//
// That tolerance is the INVOKER's, not this method's: an op the spell does not
// declare comes back as a nil result with no error (see newBuzzSpellInvoker), so
// every error reaching here is a real failure - a handler that raised, a spell
// that would not load, an op that timed out - and is reported. Swallowing it made
// a broken provider report success for the life of the build.
func (a *spellAnnotator) call(op string, params map[string]any) error {
	ctx, cancel := spellCtx()
	defer cancel()
	if _, err := a.drv.Invoke(ctx, spells.InvokeRequest{Target: op, Params: params}); err != nil {
		return fmt.Errorf("ci provider op %q: %w", op, err)
	}
	return nil
}

var _ annotate.Annotator = (*spellAnnotator)(nil)
