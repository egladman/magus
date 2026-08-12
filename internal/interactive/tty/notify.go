package tty

import (
	"io"
	"os"
	"sync"
	"time"
)

// Notifier renders a stack of expiring notifications - toasts - into a band of
// a [Zone].
//
// It is the piece a Region alone cannot be. A Region's WriteLine is a RECORD:
// once written, a line stays until something newer displaces it, which is right
// for a failure and wrong for anything that should disappear on its own. A
// toast has to vanish with nothing to replace it, so the whole band is
// re-composited on every change and expiry simply produces a shorter frame.
//
// Expiry is driven by a sweeper goroutine, because a toast that only expired
// when the next one arrived would not be a toast - it would be a log that
// happens to be pinned. That goroutine is also why the [Zone] mutex matters:
// the sweeper paints while the caller's own thread may be painting too.
type Notifier struct {
	mu   sync.Mutex
	zone *Zone
	// lease is nil until the first notification. Rows are claimed LAZILY and
	// then held: claiming them up front would cost every run three rows of
	// scrolling area for a band that, under the rule that only actionable
	// things are worth notifying about, most runs never fill. Holding them
	// afterwards is the other half - releasing on an empty stack would make the
	// zone grow and shrink as notifications came and went, and every one of
	// those is a visible reflow of the whole screen.
	lease *Lease
	// max bounds the stack; the lease itself starts at one row and grows toward
	// max only as more notifications are actually showing at once. Reserving
	// max up front would charge every run three rows of scrolling area for a
	// band that usually holds one message.
	max    int
	toasts []toast
	now    func() time.Time
	stop   chan struct{}
	// wake tells the sweeper the toast set changed and its timer must be
	// re-armed. Buffered and sent to without blocking, so a caller holding the
	// mutex can signal it safely.
	wake    chan struct{}
	started bool
	stopped bool
}

// toast is one notification and the moment it stops being shown. A zero
// deadline means it never expires on its own: it stays until a newer toast
// pushes it out of the band, or the notifier is closed.
type toast struct {
	text     string
	style    string
	deadline time.Time
	// key names a CONDITION this toast reports, for one raised by [Notifier.Pin]
	// and retracted by [Notifier.Clear]. Empty for an ordinary transient
	// notification, which nobody retracts because it is about a moment that has
	// already passed.
	key string
}

// NewNotifier returns a notifier that will draw into the bottom of z, holding
// at most max notifications at once.
//
// Nothing is claimed and no goroutine starts here. The band is taken on the
// first notification and grows toward max only as more are actually showing, so
// a process that never notifies - which, under the rule that only actionable
// things are worth notifying about, is most of them - pays nothing at all.
//
// A grant that is refused, on a pipe, in CI, or on a terminal with no room,
// leaves every method a safe no-op, so callers write the same code either way.
func NewNotifier(z *Zone, max int) *Notifier {
	return &Notifier{zone: z, max: max, now: time.Now,
		stop: make(chan struct{}), wake: make(chan struct{}, 1)}
}

// ensureLease claims the band on first use and starts the expiry sweeper.
// Callers hold n.mu. Reports whether there is a band to draw in.
func (n *Notifier) ensureLease() bool {
	if n.lease != nil {
		return n.lease.Enabled()
	}
	n.lease = n.zone.Acquire(1)
	if !n.lease.Enabled() {
		return false
	}
	if !n.started && !n.stopped {
		n.started = true
		go n.sweepLoop()
	}
	return true
}

// notifyRows is how many notifications the process band shows at once. Three
// is enough for a burst to read as a burst, and small enough that it still
// fits alongside the run's six-row failure band on an ordinary 24-row
// terminal (6 + 3 leased leaves 15 scrolling, over the 8-row minimum).
const notifyRows = 3

// A mutex rather than a sync.Once, because CloseStderr reads this from the
// exit path - including the signal-driven one - while another goroutine may
// still be reaching for the notifier. A Once orders its own callers and says
// nothing about a reader outside it.
var (
	stderrNotifierMu  sync.Mutex
	stderrNotifierVal *Notifier
)

// StderrNotifier returns the process-wide notification band on standard error,
// creating it on first use.
//
// A singleton for the reason [StderrZone] is one, and more so: its three
// consumers - magus's own run events, a term\notify call from a magusfile, and
// a daemon background job - cannot see each other, run on different threads,
// and have different lifetimes. Toasts from all of them belong in ONE stack,
// in arrival order, or the reader gets three competing bands.
//
// Nothing is drawn and no rows are taken until the first Notify.
func StderrNotifier() *Notifier {
	stderrNotifierMu.Lock()
	defer stderrNotifierMu.Unlock()
	if stderrNotifierVal == nil {
		stderrNotifierVal = NewNotifier(StderrZone(), notifyRows)
	}
	return stderrNotifierVal
}

// NotifierFor returns the notification band for w: the process-wide
// [StderrNotifier] when w IS standard error, and a fresh unshared one
// otherwise. It is the [ZoneFor] of notifications and makes the same identity
// check for the same reason.
func NotifierFor(w io.Writer) *Notifier {
	if w == os.Stderr {
		return StderrNotifier()
	}
	return NewNotifier(NewZone(w, SystemProbe), notifyRows)
}

// CloseStderr stops the process's notification band and hands back every
// leased row on standard error. It is the exit-path counterpart to the lazy
// singletons above, and is safe to call when neither was ever used.
func CloseStderr() error {
	stderrNotifierMu.Lock()
	n := stderrNotifierVal
	stderrNotifierVal = nil
	stderrNotifierMu.Unlock()

	if n != nil {
		if err := n.Close(); err != nil {
			return err
		}
	}
	// StderrZone is not re-read through its own accessor: creating the zone
	// here just to close it would set margins nothing ever asked for.
	if stderrZoneVal != nil {
		return stderrZoneVal.Close()
	}
	return nil
}

// Notify pushes a notification onto the stack, to be shown for ttl. A ttl of
// zero or less never expires on its own.
//
// The newest toast sits at the bottom of the band, closest to the reader. When
// the stack overflows its rows the OLDEST is dropped: a notification the reader
// has already had time to see is the right one to lose, and silently discarding
// the newest would make a burst look like nothing happened.
func (n *Notifier) Notify(text, style string, ttl time.Duration) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.ensureLease() {
		return nil
	}

	t := toast{text: text, style: style}
	if ttl > 0 {
		t.deadline = n.now().Add(ttl)
	}
	n.expire(n.now())
	n.toasts = append(n.toasts, t)
	n.evict()
	n.signal()
	return n.paint()
}

// Pin raises a notification about a CONDITION that is currently true, and keeps
// it until [Notifier.Clear] retracts it. Calling it again with the same key
// updates the text in place rather than stacking a duplicate.
//
// This is the shape most things worth notifying about actually have. A toast
// earns its place only when the reader has to act, and something a reader must
// act on is rarely a moment - it is a state that persists until they do
// something about it: a run stalled behind another process's lock, a daemon
// that has gone away, credentials that have expired. Reporting those as
// expiring notifications would be wrong twice over, since the message vanishes
// while the problem does not, and it reappears on nothing when the problem
// finally ends.
//
// A pinned notification never expires on a clock and is evicted only when the
// band is full of other pins. Transient notifications lose their row first.
func (n *Notifier) Pin(key, text, style string) error {
	if key == "" {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.ensureLease() {
		return nil
	}
	n.expire(n.now())
	for i := range n.toasts {
		if n.toasts[i].key == key {
			n.toasts[i].text, n.toasts[i].style = text, style
			return n.paint()
		}
	}
	n.toasts = append(n.toasts, toast{text: text, style: style, key: key})
	n.evict()
	n.signal()
	return n.paint()
}

// Clear retracts the pinned notification named by key, if it is showing.
// Retracting one that is not showing is a no-op, so a caller reporting the end
// of a condition does not have to know whether the start was ever displayed.
func (n *Notifier) Clear(key string) error {
	if key == "" {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.lease == nil || !n.lease.Enabled() {
		return nil
	}
	for i := range n.toasts {
		if n.toasts[i].key != key {
			continue
		}
		n.toasts = append(n.toasts[:i], n.toasts[i+1:]...)
		n.signal()
		return n.paint()
	}
	return nil
}

// evict trims the stack to the band's height, dropping the oldest TRANSIENT
// notification first and only falling back to pins when there is nothing else
// to give up. Callers hold n.mu.
//
// Without that preference a burst of ordinary notifications would push out the
// standing report of a condition, which is the one message that cannot be
// re-sent: it was raised once, when the condition began.
func (n *Notifier) evict() {
	for len(n.toasts) > n.max {
		victim := -1
		for i := range n.toasts {
			if n.toasts[i].key == "" {
				victim = i
				break
			}
		}
		if victim < 0 {
			victim = 0
		}
		n.toasts = append(n.toasts[:victim], n.toasts[victim+1:]...)
	}
}

// Close stops the sweeper and gives the band back. Idempotent and safe to
// defer.
func (n *Notifier) Close() error {
	n.mu.Lock()
	if n.stopped {
		n.mu.Unlock()
		return nil
	}
	n.stopped = true
	close(n.stop)
	n.toasts = nil
	lease := n.lease
	n.mu.Unlock()
	if lease == nil {
		// Never notified, so no rows were ever taken.
		return nil
	}
	return lease.Release()
}

// sweepLoop drops expired toasts, waking only when one is actually due.
//
// A fixed-interval ticker would have been simpler and is wrong for anything
// long-lived: at ten wakeups a second it costs nothing measurable per tick and
// still keeps the CPU from idling, for hours or days, in a process whose band
// is usually empty. A TUI left open all afternoon must be as close to free as a
// process that is genuinely doing nothing.
//
// So the timer is armed to the NEAREST deadline and not armed at all when
// nothing can expire. A band holding only pinned conditions - which have no
// deadline, by design - wakes this goroutine zero times.
func (n *Notifier) sweepLoop() {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		if d, ok := n.untilNextDeadline(); ok {
			timer.Reset(d)
		} else {
			timer.Stop()
		}
		select {
		case <-n.stop:
			return
		case <-n.wake:
			// The set changed; re-arm from the top.
		case <-timer.C:
			n.sweep()
		}
	}
}

// untilNextDeadline reports how long until the soonest expiry, and whether
// there is one at all. A non-positive result means something is already due.
func (n *Notifier) untilNextDeadline() (time.Duration, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	var soonest time.Time
	for _, t := range n.toasts {
		if t.deadline.IsZero() {
			continue
		}
		if soonest.IsZero() || t.deadline.Before(soonest) {
			soonest = t.deadline
		}
	}
	if soonest.IsZero() {
		return 0, false
	}
	d := soonest.Sub(n.now())
	if d < 0 {
		d = 0
	}
	return d, true
}

// signal nudges the sweeper to re-arm. Safe to call under n.mu: the channel is
// buffered and the send never blocks.
func (n *Notifier) signal() {
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// sweep drops whatever has expired and repaints if anything did. Split from
// sweepLoop so a test can drive expiry without waiting on a real clock.
func (n *Notifier) sweep() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.expire(n.now()) {
		return
	}
	_ = n.paint()
}

// expire removes every toast whose deadline has passed, reporting whether any
// did. Callers hold n.mu.
func (n *Notifier) expire(now time.Time) bool {
	kept := n.toasts[:0]
	for _, t := range n.toasts {
		if !t.deadline.IsZero() && !now.Before(t.deadline) {
			continue
		}
		kept = append(kept, t)
	}
	if len(kept) == len(n.toasts) {
		return false
	}
	n.toasts = kept
	return true
}

// paint pushes the current stack to the lease. Callers hold n.mu.
//
// The band is filled from the BOTTOM: a stack of one toast in a three-row band
// draws on the last row, not the first, so a toast appears next to the reader's
// cursor and the stack grows upward away from it. Filling from the top would
// make a single toast look like it belonged to whatever band sits above.
func (n *Notifier) paint() error {
	// Claim another row if more notifications are showing than the band holds.
	// A refusal is not an error: the stack simply stays as tall as the terminal
	// allowed, and the oldest entry drops as usual.
	if len(n.toasts) > n.lease.Rows() {
		n.lease.Grow(min(len(n.toasts), n.max))
	}
	height := n.lease.Rows()
	rows := make([]Row, height)
	offset := height - len(n.toasts)
	if offset < 0 {
		// More toasts than rows: show the newest that fit.
		n.toasts = n.toasts[len(n.toasts)-height:]
		offset = 0
	}
	for i, t := range n.toasts {
		rows[offset+i] = Row{Text: t.text, Style: t.style}
	}
	_, err := n.lease.Set(rows)
	return err
}
