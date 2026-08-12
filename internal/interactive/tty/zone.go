package tty

import (
	"io"
	"os"
	"sync"
)

// Zone is the process's single owner of a terminal's bottom rows, handing out
// contiguous bands of them as leases.
//
// It exists because the terminal's scroll margins are ONE GLOBAL SETTING and
// magus had more than one component setting them. A run could hold two
// [Region]s at once - the CLI's pretty handler and the one the cache opens for
// its own events - each computing row arithmetic from a different height, and
// the only thing keeping the user's shell usable was an unconditional
// ResetScrollMargins on the way out. That is a mop, not an owner: it cleans up
// after the damage rather than preventing it, and it cannot help at all while
// the run is still going.
//
// So the margins are set here, once, by whoever owns the whole zone, and every
// consumer asks for rows instead of taking the terminal. One DECSTBM, one
// Release, one composited repaint per change.
//
// The corollary is that Zone is the concurrency boundary a Region deliberately
// is not. A Region is single-threaded by contract; its consumers here are not
// (a cache handler paints from the run's pool, a notifier from an expiry
// sweeper, the daemon from a background job), so every Region access goes
// through z.mu.
type Zone struct {
	mu     sync.Mutex
	w      io.Writer
	probe  Probe
	leases []*Lease
	// region is built on demand and REBUILT whenever the leased total changes,
	// because a Region's height is fixed at construction. Nil until something
	// actually draws, so a run that never paints never touches the terminal.
	region *Region
	// isTTY is settled once, at construction: a writer either has a terminal
	// that can be drawn on or it does not, and re-probing per paint would buy
	// nothing.
	// Whether the terminal is big ENOUGH is a separate question, re-asked per
	// grant and per paint, because the window can be resized.
	isTTY bool
}

// NewZone returns a Zone over w, measured through p. Nothing is written here.
//
// Pass [SystemProbe] in production; tests pass their own Probe and a writer
// with a synthetic descriptor.
func NewZone(w io.Writer, p Probe) *Zone {
	return &Zone{w: w, probe: p, isTTY: CanRender(w, p)}
}

var (
	stderrZoneOnce sync.Once
	stderrZoneVal  *Zone
)

// StderrZone returns the process-wide owner of standard error's bottom rows.
//
// A package-level singleton rather than something threaded through call sites,
// because the thing it guards IS process-global: there is one terminal behind
// stderr, and a second Zone over it would recreate exactly the two-owners
// problem [Zone] exists to end. Consumers that cannot see each other - the
// cache's log handler, a Buzz notify call, a daemon job - have to arrive at the
// same owner without being introduced.
//
// Tests build their own with [NewZone] instead of reaching for this.
func StderrZone() *Zone {
	stderrZoneOnce.Do(func() { stderrZoneVal = NewZone(os.Stderr, SystemProbe) })
	return stderrZoneVal
}

// ZoneFor returns the Zone that owns w's bottom rows: the process-wide
// [StderrZone] when w IS standard error, and a fresh unshared Zone otherwise.
//
// The identity check is the point. Sharing an owner is only correct for
// consumers writing to the same terminal, and "the same terminal" is a
// property of the descriptor, not of anyone's intent to cooperate. A handler
// pointed at a log file or a test buffer gets its own Zone and cannot disturb
// the real one.
func ZoneFor(w io.Writer) *Zone {
	if w == os.Stderr {
		return StderrZone()
	}
	return NewZone(w, SystemProbe)
}

// Lease is one consumer's contiguous band of rows inside a [Zone]. Bands sit in
// the order they were acquired, top to bottom, so the last consumer to arrive
// is the one closest to the reader's cursor.
//
// The zero Lease is the DISABLED lease: it is what Acquire hands back when
// there is no terminal or no room, and every method on it is a safe no-op. That
// is what lets a caller write the same code for a TTY and a CI log.
type Lease struct {
	zone     *Zone
	rows     int
	band     []Row
	released bool
}

// Acquire grants rows at the bottom of the zone, or returns a disabled Lease
// when it cannot.
//
// A grant is REFUSED rather than allowed to shrink the scrolling area below
// what the leases already present depend on. That asymmetry is the whole
// arbitration rule: an incumbent that is drawing correctly must not go dark
// because a newcomer asked for space, so the newcomer degrades instead. A
// notifier that cannot get toast rows drops its toasts; the run's failure
// region above it keeps working.
func (z *Zone) Acquire(rows int) *Lease {
	if rows <= 0 || !z.isTTY {
		return &Lease{}
	}
	z.mu.Lock()
	defer z.mu.Unlock()

	total := rows
	for _, l := range z.leases {
		total += l.rows
	}
	fd, ok := Fd(z.w)
	if !ok {
		return &Lease{}
	}
	width, height, err := z.probe.Size(fd)
	if err != nil || !fits(width, height, total) {
		return &Lease{}
	}

	l := &Lease{zone: z, rows: rows}
	z.leases = append(z.leases, l)
	return l
}

// Enabled reports whether this lease has rows on a real terminal. It is a cheap
// pre-check for a caller deciding whether to COMPUTE something it would only
// display - the cache asks before sampling pool occupancy - and not the
// authority on whether a given paint landed. [Lease.Set] answers that.
func (l *Lease) Enabled() bool { return l.zone != nil && !l.released }

// Set replaces this lease's band and repaints the zone, reporting whether the
// rows actually reached the terminal.
//
// The bool is the point of the signature. A lease cannot do the non-TTY
// fallback itself, because only the caller knows what its content IS: a
// failure line is a RECORD and must be printed plainly when it cannot be
// pinned, while a status line or a toast is a VIEW and must be dropped, since
// replaying every repaint into a pipe is noise. Returning false and letting the
// caller decide keeps that judgement where the knowledge is.
//
// Fewer rows than the lease holds leaves the remainder blank, which is what
// lets an entry disappear; more are dropped.
func (l *Lease) Set(rows []Row) (rendered bool, err error) {
	if l.zone == nil || l.released {
		return false, nil
	}
	l.zone.mu.Lock()
	defer l.zone.mu.Unlock()
	l.band = rows
	return l.zone.repaint()
}

// Release gives this lease's rows back and repaints without them. Idempotent
// and safe to defer. Releasing the last lease hands the terminal back
// entirely: margins reset, rows returned.
func (l *Lease) Release() error {
	if l.zone == nil || l.released {
		return nil
	}
	z := l.zone
	z.mu.Lock()
	defer z.mu.Unlock()
	l.released = true
	for i, other := range z.leases {
		if other == l {
			z.leases = append(z.leases[:i], z.leases[i+1:]...)
			break
		}
	}
	_, err := z.repaint()
	return err
}

// HitTest maps an absolute terminal row - the coordinate space a mouse event
// arrives in - to the lease that owns it and the index of that row within the
// lease's band.
//
// This is the whole reason a click can act on what was clicked without anyone
// tracking screen geometry separately: the zone already knows where every band
// sits, because it put them there. A caller receives Row from an [Event] and
// asks; it never computes a row number itself, so there is no second copy of
// the layout to drift.
//
// Reports false for any row outside the reserved zone, which includes every row
// of ordinary scrolling output above it. A click up there belongs to the
// terminal's own selection, not to magus.
func (z *Zone) HitTest(row int) (lease *Lease, index int, ok bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.region == nil || !z.region.Enabled() || !z.region.open {
		return nil, 0, false
	}
	offset := row - z.region.firstRow()
	if offset < 0 {
		return nil, 0, false
	}
	for _, l := range z.leases {
		if offset < l.rows {
			return l, offset, true
		}
		offset -= l.rows
	}
	return nil, 0, false
}

// Grow enlarges this lease to rows, reporting whether it could.
//
// Growth ONLY: a band that shrank again would make the zone reflow every time
// its content came and went, and a reflow is a visible movement of the whole
// screen. Growing on demand instead means a consumer can claim what it needs
// when it needs it - one row for one notification - rather than reserving for
// its own worst case up front and charging every run for the possibility.
//
// Refused, like a fresh grant, when the larger total would leave too little
// scrolling area. The lease keeps the size it had.
func (l *Lease) Grow(rows int) bool {
	if l.zone == nil || l.released || rows <= l.rows {
		return false
	}
	z := l.zone
	z.mu.Lock()
	defer z.mu.Unlock()

	total := rows - l.rows
	for _, other := range z.leases {
		total += other.rows
	}
	fd, ok := Fd(z.w)
	if !ok {
		return false
	}
	width, height, err := z.probe.Size(fd)
	if err != nil || !fits(width, height, total) {
		return false
	}
	l.rows = rows
	_, err = z.repaint()
	return err == nil
}

// Rows reports how many rows this lease holds, for a caller bounds-checking an
// index it got from [Zone.HitTest] against its own content.
func (l *Lease) Rows() int {
	if l.zone == nil {
		return 0
	}
	return l.rows
}

// Close releases every lease and hands the terminal back. It is the
// process-exit counterpart to [Lease.Release], for the exit path that does not
// hold the individual leases.
func (z *Zone) Close() error {
	z.mu.Lock()
	defer z.mu.Unlock()
	for _, l := range z.leases {
		l.released = true
	}
	z.leases = nil
	return mustRepaintErr(z.repaint())
}

// mustRepaintErr drops repaint's rendered flag, for callers that only care
// whether the terminal write failed.
func mustRepaintErr(_ bool, err error) error { return err }

// repaint composites every lease's band into one slice and drives the Region.
// The caller holds z.mu.
//
// The Region is rebuilt whenever the leased total changes because its height is
// fixed at construction. That costs one visible reflow when a second consumer
// arrives mid-run, which is the price of NOT permanently reserving rows for a
// feature that may never be used: a run with no toasts pins exactly the rows
// the failure region asked for.
func (z *Zone) repaint() (rendered bool, err error) {
	total := 0
	for _, l := range z.leases {
		total += l.rows
	}
	if total == 0 {
		if z.region == nil {
			return false, nil
		}
		err := z.region.Release()
		z.region = nil
		return false, err
	}
	if z.region == nil || z.region.height != total {
		var relErr error
		if z.region != nil {
			relErr = z.region.Release()
		}
		z.region = NewRegion(z.w, total, z.probe)
		if relErr != nil {
			return false, relErr
		}
	}
	if !z.region.Enabled() {
		return false, nil
	}

	rows := make([]Row, 0, total)
	for _, l := range z.leases {
		for i := range l.rows {
			if i < len(l.band) {
				rows = append(rows, l.band[i])
				continue
			}
			rows = append(rows, Row{})
		}
	}
	if err := z.region.Render(rows); err != nil {
		return false, err
	}
	// Render can disable the region on the way through when the window has
	// shrunk past what the leases need, so the answer is read AFTER the paint,
	// not before it.
	return z.region.Enabled(), nil
}
