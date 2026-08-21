package tty

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

// Notifier renders a stack of expiring notifications - toasts - into a band of
// a [Zone].
//
// A toast has to VANISH with nothing to replace it, which an append-only
// surface cannot express: a line written into one stays until something newer
// displaces it, and that is right for a failure and wrong here. So the whole
// band is re-composited on every change, and expiry simply produces a shorter
// frame.
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
// The notification's decoration: accentBar is its color stripe, accentCols the
// width that stripe occupies, and marqueeHold how many ticks a scrolling
// message rests at each end before moving on.
const (
	accentBar = "\u258c"
	// accentCols is what the bar occupies on SCREEN. The rune is three bytes
	// and one column, and every budget here is in columns - len() would spend
	// three of them on a glyph that takes one, which is the same byte-for-column
	// mistake that clipped the region's border to a third of its width.
	accentCols  = 1
	marqueeHold = 8
)

// marquee returns the window of text visible at offset off in width columns.
//
// Text that fits is returned whole and off is ignored, so the common case costs
// nothing. Beyond that the window slides, resting at both ends: the offset
// space is padded by marqueeHold at each end (see advance), and clamping into
// the real range is what turns those ticks into a pause rather than a jump.
func marquee(text string, off, width int) string {
	// An unknown width means the band has not been built yet - Lease.Width has
	// no region to measure until the first paint. Return the message WHOLE
	// rather than nothing: scrolling is an enhancement, the layout clips to the
	// real width anyway, and a toast that renders empty is strictly worse than
	// one that renders long.
	if width <= 0 {
		return text
	}
	// COLUMNS, not bytes, and a rune-indexed window. Slicing a byte range out
	// of a column budget splits a UTF-8 sequence and emits a replacement
	// character the moment a message carries a non-ASCII project name.
	r := []rune(text)
	span := len(r) - width
	if span <= 0 {
		return text
	}
	at := off - marqueeHold
	at = min(max(at, 0), span)
	return string(r[at : at+width])
}

type toast struct {
	text     string
	style    SGR
	deadline time.Time
	// key names a CONDITION this toast reports, for one raised by [Notifier.Pin]
	// and retracted by [Notifier.Clear]. Empty for an ordinary transient
	// notification, which nobody retracts because it is about a moment that has
	// already passed.
	key string
	// off is how far a message too wide for the band has scrolled, in columns.
	// Zero for anything that fits, which is almost everything.
	off int
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

// ensureLease claims the band on first use and starts the expiry sweeper,
// reporting whether there is a band to draw in. Callers hold n.mu.
//
// Rows are claimed on first use and then KEPT: handing them back mid-run
// reflows the whole screen, which TestNotifierHoldsItsRowsOnceClaimed pins.
func (n *Notifier) ensureLease() bool {
	// A closed notifier takes no rows. Without this, a Notify after Close
	// acquired a band that the (suppressed) sweeper would never release, and
	// held toasts whose deadlines nobody was left to enforce.
	if n.stopped {
		return false
	}
	// Retried on a DISABLED lease, not just a nil one: Acquire refuses by
	// handing back a disabled Lease rather than nil, so keying on nil alone
	// left the notifier dark forever after a single refusal - the opposite of
	// what Pin promises for a condition recorded when there was no room. An
	// enabled lease is kept, which is what the hold-its-rows test pins.
	if n.lease == nil || !n.lease.Enabled() {
		n.lease = n.zone.Acquire(1)
	}
	if !n.lease.Enabled() {
		// No band, so no sweeper. Starting one anyway parked a goroutine for
		// the life of a process that has no terminal to draw on, which is every
		// CI run - and NewNotifier promises it pays nothing at all there.
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

// A mutex rather than a sync.Once, because ReleaseStderr reads this from the
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

// ReleaseStderr stops the process's notification band and hands back every
// leased row on standard error. It is the exit-path counterpart to the lazy
// singletons above, and is safe to call when neither was ever used.
func ReleaseStderr() error {
	stderrNotifierMu.Lock()
	n := stderrNotifierVal
	stderrNotifierVal = nil
	stderrNotifierMu.Unlock()

	// Both are closed and the errors joined: returning on the notifier's error
	// left the zone's scroll margins set on the way out, which is the one thing
	// this function exists to prevent.
	var errs []error
	if n != nil {
		errs = append(errs, n.Close())
	}
	if z := takeStderrZone(); z != nil {
		errs = append(errs, z.Close())
	}
	return errors.Join(errs...)
}

// Notify pushes a notification onto the stack, to be shown for ttl. A ttl of
// zero or less never expires on its own.
//
// The newest toast sits at the bottom of the band, closest to the reader. When
// the stack overflows its rows the OLDEST is dropped: a notification the reader
// has already had time to see is the right one to lose, and silently discarding
// the newest would make a burst look like nothing happened.
func (n *Notifier) Notify(text string, style SGR, ttl time.Duration) error {
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
func (n *Notifier) Pin(key, text string, style SGR) error {
	if key == "" {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	// The lease is NOT a precondition for recording. A pin is a CONDITION - it
	// is true until retracted - so the model has to hold it whether or not
	// there is anywhere to draw it today; the band can gain rows later, when
	// another lease releases or the window grows, and the pin must be there to
	// appear. Returning early here dropped it outright, so a terminal too small
	// at the moment of pinning lost the condition permanently.
	n.ensureLease()
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
	// Retraction is a MODEL operation, like pinning: a condition that became
	// false has to stop being true whether or not it is currently on screen.
	// Guarding on the lease meant a pin recorded when there was no room could
	// never be cleared, so it would surface the moment the band gained a row -
	// long after the thing it described had ended.
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
func (n *Notifier) evict() { n.toasts = trim(n.toasts, n.max) }

// trim reduces toasts to at most n entries, dropping the oldest TRANSIENT
// first and only falling back to pins when there is nothing else to give up.
//
// Shared with the paint path, which used to truncate from the front instead.
// That was a second, contradictory copy of this rule: a band that could not
// grow silently discarded a pinned condition, and a pin dropped that way is
// unreachable by Clear, which then no-ops forever on a condition still true.
func trim(toasts []toast, n int) []toast {
	for len(toasts) > n {
		victim := -1
		for i := range toasts {
			if toasts[i].key == "" {
				victim = i
				break
			}
		}
		if victim < 0 {
			victim = 0
		}
		toasts = append(toasts[:victim], toasts[victim+1:]...)
	}
	return toasts
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
	// A scrolling message needs a tick of its own. Deadlines are the only thing
	// that used to arm this timer, and a PINNED condition has none - so the one
	// message in magus long enough to need scrolling (the lock wait) woke the
	// sweeper zero times and never moved a column. The feature was dead in
	// production while its unit tests passed, because they called the scroller
	// directly.
	if n.overflowsLocked() {
		if soonest.IsZero() {
			return marqueeTick, true
		}
		if d := soonest.Sub(n.now()); d > marqueeTick {
			return marqueeTick, true
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
	expired := n.expire(n.now())
	// Advance any message too wide to fit, on the tick that is ALREADY running
	// for expiry. A second timer for animation would double the wakeups on an
	// idle terminal, which is the wrong trade for a row that scrolls.
	scrolled := n.advance(n.lease.Width())
	if !expired && !scrolled {
		return
	}
	_ = n.paint()
}

// marqueeTick is how often a message too wide to fit moves a column. Slow
// enough to read, and slow enough that an idle band with a long message is not
// a repaint loop.
const marqueeTick = 220 * time.Millisecond

// overflowsLocked reports whether any message is too wide for the band, which
// is the only reason to keep waking up. Callers hold n.mu.
func (n *Notifier) overflowsLocked() bool {
	body := n.lease.Width() - accentCols - 1
	if body <= 0 {
		return false
	}
	for _, t := range n.toasts {
		if Cols(t.text) > body {
			return true
		}
	}
	return false
}

// bolden adds weight to a style without producing ";1" for an empty one, which
// is a valid-looking escape that appendStyled would emit where it used to emit
// nothing at all.
func bolden(s SGR) SGR {
	if s == "" {
		return SGRBold
	}
	return s + ";" + SGRBold
}

// style drops color when the terminal does not want it. The notifier draws
// straight into the band rather than through the handler, so NO_COLOR has to
// reach it here or the one row a reader is meant to act on is the only colored
// thing left on a monochrome terminal.
func (n *Notifier) style(s SGR) SGR {
	if n.zone == nil || !WantsColor(n.zone.w, n.zone.probe) {
		return ""
	}
	return s
}

// advance moves every overflowing message one column, reporting whether any
// did. Callers hold n.mu.
//
// Only what does not FIT moves. A toast that fits is static, which is nearly
// all of them - so an idle band repaints not at all, and the motion appears
// exactly where a reader would otherwise be unable to read the end of a line.
func (n *Notifier) advance(width int) bool {
	if width <= 0 {
		return false
	}
	body := width - accentCols - 1
	moved := false
	for i := range n.toasts {
		span := Cols(n.toasts[i].text) - body
		if span <= 0 {
			n.toasts[i].off = 0
			continue
		}
		// The cycle runs one hold PAST each end so the start and the finish are
		// both readable at rest. A continuous crawl never shows either whole.
		n.toasts[i].off = (n.toasts[i].off + 1) % (span + 2*marqueeHold)
		moved = true
	}
	return moved
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
	height := n.lease.Rows()
	rows := make([]Line, height)
	// The band may hold fewer rows than the stack when a grow was refused.
	// Choose what to SHOW by the same rule eviction uses, on a copy - the model
	// is not the view, and rendering must not destroy a pin the reader still
	// needs.
	shown := n.toasts
	if len(shown) > height {
		shown = trim(append([]toast(nil), shown...), height)
	}
	offset := height - len(shown)
	width := n.lease.Width()
	for i, t := range shown {
		// An accent bar in the toast's own color, then the message in BOLD.
		//
		// The message used to be a single plain span, which put it at the same
		// weight as the dim chrome around it - so the one row on screen that
		// exists because a person has to act on it read like everything else.
		// The bar is the affordance every toast UI uses, and it survives
		// NO_COLOR as a shape even when the color is dropped.
		rows[offset+i] = Line{Spans: []Span{
			{Text: accentBar + " ", Style: n.style(t.style)},
			{Text: marquee(t.text, t.off, width-accentCols-1), Style: n.style(bolden(t.style))},
		}}
	}
	// Content FIRST, then the grow. Growing first repaints the band at its new
	// height while it still holds the OLD rows, so a toast that was just
	// dropped is drawn once more before being overwritten - a flash of stale
	// content on a surface whose whole promise is that it holds still. Setting
	// first means the worst case is a row briefly absent rather than a row
	// briefly wrong.
	if _, err := n.lease.Set(rows); err != nil {
		return err
	}
	if before := n.lease.Rows(); len(n.toasts) > before {
		// A refused grow is not an error: the stack simply stays as tall as the
		// terminal allowed, and the oldest entry drops as usual. Rows is asked
		// again rather than read off a bool, so "did it grow" is answered by the
		// lease itself.
		_ = n.lease.Grow(min(len(n.toasts), n.max))
		if n.lease.Rows() > before {
			return n.paint() // now that there is room, draw what fits it
		}
	}
	return nil
}
