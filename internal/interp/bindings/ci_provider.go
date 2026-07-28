package bindings

import (
	"context"
	"io"
	"sync"

	"github.com/egladman/magus/internal/ci/annotate"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
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
//	concurrency()                              -> int   suggested parallelism
//
// The adapter has no provider knowledge, so the binary stays CI-agnostic
// and a new provider is a spell rather than a change to magus.
//
// Every op here fires at most once per project (a group around a failure)
// or once per run (enabled, concurrency), never per log line. That is what
// makes crossing into the VM affordable: a per-line hook would need a
// declarative descriptor evaluated once instead, not a call per line.
type spellAnnotator struct {
	drv types.SpellDriver

	mu          sync.Mutex
	activeKnown bool
	active      bool
	concurrency int
	concKnown   bool
}

// ctx is the context spell ops run under. The annotator is reached from
// output paths that do not thread one (a deferred failure dump), and
// these ops are short, local, and side-effect-only, so a background
// context is honest rather than a shortcut.
func (a *spellAnnotator) ctx() context.Context { return context.Background() }

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
	resp, err := a.drv.Invoke(a.ctx(), types.InvokeRequest{Target: "enabled"})
	if err != nil {
		return false
	}
	if resp.Data == nil {
		a.active = true // no enabled() op declared -> always active
	} else {
		a.active, _ = resp.Data.(bool)
	}
	a.activeKnown = true
	return a.active
}

func (a *spellAnnotator) StartGroup(g annotate.Group) error {
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

// Concurrency reads the spell's optional concurrency() op once. A spell
// that declares none, or returns a non-positive value, expresses no
// opinion and the caller falls back to its own default.
func (a *spellAnnotator) Concurrency() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.concKnown {
		return a.concurrency
	}
	resp, err := a.drv.Invoke(a.ctx(), types.InvokeRequest{Target: "concurrency"})
	if err == nil {
		switch v := resp.Data.(type) {
		case int:
			a.concurrency = v
		case int64:
			a.concurrency = int(v)
		case float64:
			a.concurrency = int(v)
		}
		a.concKnown = true
	}
	if a.concurrency < 0 {
		a.concurrency = 0
	}
	return a.concurrency
}

// Quote is deliberately not delegated to the spell. It runs over every
// replayed line of captured output, which is the one path where crossing
// into the VM per call would be too expensive to accept. A spell provider
// therefore gets no say in neutralising injected commands; a provider
// whose syntax needs quoting belongs in the Go builtins beside the
// GitHub one until a declarative form for this exists.
func (a *spellAnnotator) Quote(text string) string { return text }

// call invokes an optional op, treating an undeclared op as success. A
// spell implements only the verbs its provider supports, and the missing
// rest are not errors: annotations do not exist at all on some providers.
func (a *spellAnnotator) call(op string, params map[string]any) error {
	_, err := a.drv.Invoke(a.ctx(), types.InvokeRequest{Target: op, Params: params})
	if err != nil {
		return nil //nolint:nilerr // an unsupported verb is normal, not a failure
	}
	return nil
}

var _ annotate.Annotator = (*spellAnnotator)(nil)
