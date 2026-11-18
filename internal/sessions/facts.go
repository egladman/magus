package sessions

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/session"
)

// FactHandler turns the execution journal's result events into durable session facts.
// Its Handle is a sink on the fan-out magus already builds per invocation.
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
	start session.InvocationStart

	mu     sync.Mutex
	writer *session.Writer
	broken bool
}

// newFactHandler builds the capture handler that records an invocation's target results
// as facts under root's store. start describes the invocation and is written as the
// KindInvocationStart record ahead of the first fact.
//
// It returns nil when the store cannot be resolved, which the fan-out treats
// as no sink: a machine with no usable state directory still runs builds.
//
// It lives HERE rather than in the CLI because the server executes adopted runs itself
// (cmd/magus/main.go dispatchAdopted), so "who produces an invocation's facts" is not a
// CLI-only question. start.Lease is read by the CALLER for that reason; see
// cmd/magus/journal_hook.go, which documents what the server gets wrong today.
func newFactHandler(root string, start session.InvocationStart) func(context.Context, journal.Record) {
	dir, err := Dir(root)
	if err != nil {
		return nil
	}
	return (&FactHandler{dir: dir, start: start}).Handle
}

// Handle files r when it is a run's result; it is a recorder.Sink.
func (h *FactHandler) Handle(ctx context.Context, r journal.Record) {
	run, ok := r.Run()
	if !ok || run.Target == "" {
		return
	}
	// The journal's invocation id files the facts. Reusing it rather than minting a
	// second identifier is what keeps a fact joinable to the execution journal that
	// produced it; an event without one is unattributable, so it is dropped rather
	// than filed under an invented id.
	if r.Invocation == "" {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.broken {
		return
	}
	if h.writer == nil {
		w, err := openWriter(ctx, h.dir, r.Invocation, h.start)
		if err != nil {
			h.stopRecording(ctx, err)
			return
		}
		h.writer = w
	}
	if err := h.writer.Append(ctx, session.KindTargetResult, session.TargetResult{
		Target:         run.Target,
		Project:        run.Project,
		Outcome:        factOutcome(run.Status),
		DurationMs:     run.Duration.Milliseconds(),
		Replayed:       run.Status == journal.StatusCached,
		Permitted:      run.Status == journal.StatusPermitted,
		Ref:            run.Ref,
		DiagnosticCode: run.DiagnosticCode,
		Lease:          h.start.Lease,
		LeaseFrom:      h.start.LeaseFrom,
		StartedMs:      unixMilli(run.Started),
		Slots:          run.Slots,
		MemoryMB:       run.MemoryMB,
		Sizing:         string(run.Sizing),
		WaitedMs:       run.Waited.Milliseconds(),
		MaxRSSBytes:    run.MaxRSSBytes,
		CPUMs:          run.CPU.Milliseconds(),
	}); err != nil {
		// One failed append means the store is unwritable (a full disk, a read-only
		// state dir); retrying per target would repeat the same failure once per
		// result and slow the run down to prove it.
		h.stopRecording(ctx, err)
	}
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// stopRecording abandons the session store for the rest of this invocation, and says so
// once; h.broken gates every later Handle, so this cannot repeat per target.
//
// Failing silently is what made an unwritable store indistinguishable from an idle
// repository: nothing recorded, and `magus session` then reporting in good faith that
// no session has run yet. The warning is what keeps that empty state honest.
//
// The caller holds h.mu.
func (h *FactHandler) stopRecording(ctx context.Context, err error) {
	h.broken = true
	slog.With(attr.Component("magus")).WarnContext(ctx, "this run was not recorded in the session store, so `magus session` will not list it; check the store is writable and has space",
		slog.String("store", h.dir),
		attr.Error(err))
}

// factOutcome maps the execution journal's three statuses onto the session store's
// pass/fail axis. A cache hit is a PASS that was replayed, not a third outcome: "did
// this target end up green" and "did work run" are different questions, and
// [session.TargetResult.Replayed] answers the second. A permitted result is green on
// the same axis, so a push gate and a job's owed check read it as covered.
func factOutcome(status journal.Status) string {
	if status == journal.StatusFail {
		return session.OutcomeFail
	}
	return session.OutcomePass
}
