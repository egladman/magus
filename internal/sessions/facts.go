package sessions

import (
	"context"
	"log/slog"
	"sync"

	"github.com/egladman/magus/internal/journal"
)

// FactHandler turns the execution journal's result events into durable session facts.
// It is a slog.Handler so it can ride the capture fan-out magus already builds per
// invocation, rather than threading a second observer down the run path.
//
// It writes NOTHING for a run that produces no result, and Handle never returns an
// error: capture is best-effort by contract, and a store that can fail a build would be
// worse than no store at all. A store it cannot write is abandoned for the rest of the
// invocation and reported once through slog.
//
// It is safe for concurrent use, which the fan-out requires: results arrive from the
// worker goroutine that finished each target.
type FactHandler struct {
	dir   string
	start SessionStart

	mu     sync.Mutex
	writer *Writer
	broken bool
}

// NewFactHandler builds the capture handler that records an invocation's target results
// as session facts under root's store. start describes the invocation and is written as
// the KindSessionStart record ahead of the first fact.
//
// It returns nil - a nil slog.Handler interface, safe to compare - when the store cannot
// be resolved, which the fan-out treats as "no handler": a machine with no usable state
// directory still runs builds.
//
// It lives HERE rather than in the CLI because the daemon executes adopted runs itself
// (cmd/magus/main.go dispatchAdopted), so "who produces a session fact" is not a
// CLI-only question. start.Unit is read by the CALLER for that reason - see
// cmd/magus/journalhook.go, which documents what the daemon gets wrong today.
func NewFactHandler(root string, start SessionStart) slog.Handler {
	dir, err := Dir(root)
	if err != nil {
		return nil
	}
	return &FactHandler{dir: dir, start: start}
}

func (h *FactHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *FactHandler) Handle(_ context.Context, r slog.Record) error {
	e, ok := journal.EventFromRecord(r)
	if !ok || e.Kind != journal.KindResult || e.Target == "" {
		return nil
	}
	// The invocation id IS the session id. Reusing it rather than minting a second
	// identifier is what keeps a session fact joinable to the execution journal that
	// produced it; an event without one is unattributable, so it is dropped rather
	// than filed under an invented session.
	if e.Inv == "" {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.broken {
		return nil
	}
	if h.writer == nil {
		w, err := Open(h.dir, e.Inv, h.start)
		if err != nil {
			h.stopRecording(err)
			return nil
		}
		h.writer = w
	}
	if err := h.writer.Append(KindTargetResult, TargetResult{
		Target:   e.Target,
		Project:  e.Project,
		Outcome:  factOutcome(e.Status),
		DurMs:    e.DurMs,
		Replayed: e.Status == journal.StatusCached,
		Ref:      e.Ref,
		Unit:     h.start.Unit,
	}); err != nil {
		// One failed append means the store is unwritable (a full disk, a read-only
		// state dir); retrying per target would repeat the same failure once per
		// result and slow the run down to prove it.
		h.stopRecording(err)
	}
	return nil
}

// stopRecording abandons the session store for the rest of this invocation, and says so
// once - h.broken gates every later Handle, so this cannot repeat per target.
//
// Failing silently is what made an unwritable store indistinguishable from an idle
// repository: nothing recorded, and `magus sessions` then reporting in good faith that
// no session has run yet. The warning is what keeps that empty state honest.
//
// The caller holds h.mu. slog.Warn cannot re-enter it: a plain log record carries no
// journal event, so Handle returns before it reaches the lock.
func (h *FactHandler) stopRecording(err error) {
	h.broken = true
	slog.Warn("magus: this run was not recorded in the session store, so `magus sessions` will not list it; check the store is writable and has space",
		slog.String("store", h.dir),
		slog.String("error", err.Error()))
}

func (h *FactHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *FactHandler) WithGroup(string) slog.Handler      { return h }

// factOutcome maps the execution journal's three statuses onto the session store's
// pass/fail axis. A cache hit is a PASS that was replayed, not a third outcome: "did
// this target end up green" and "did work run" are different questions, and
// [TargetResult.Replayed] answers the second.
//
// Note the known imprecision on the way in: a cache hit whose ref is missing from the
// output store is reported as StatusPass, so Replayed under-reports rather than
// over-reports cache hits.
func factOutcome(status string) string {
	if status == journal.StatusFail {
		return OutcomeFail
	}
	return OutcomePass
}
