package tty

import (
	"io"
	"os"
	"sync"
)

// The box the zone draws around its pinned rows, marking where the scrolling
// transcript ends and the rows that hold still begin.
//
// borderRowsPerEdge is the top rule and the bottom rule; borderRows is what the
// pair costs in rows, and borderCols what the vertical edges cost in columns.
//
// Constants rather than options: a boundary that some runs draw and others do
// not would make the one thing it communicates - which lines hold still -
// depend on configuration.
const (
	borderRowsPerEdge = 1
	borderRows        = 2 * borderRowsPerEdge
	borderCols        = 1
)

// Zone is the process's single owner of a terminal's bottom rows, handing out
// contiguous bands of them as leases.
//
// EVERY field of a Lease is guarded by this type's mutex, including the ones a
// Lease method reads about itself. The exit path calls Zone.Close from a signal
// handler while the run's own goroutines are still painting, so an unlocked
// read of `released` or `rows` is a live race rather than a theoretical one.
//
// It exists because the terminal's scroll margins are ONE GLOBAL SETTING and
// magus had more than one component setting them: two [region]s at once, each
// computing row arithmetic from a different height, with only an unconditional
// ResetScrollMargins on the way out keeping the shell usable. That is a mop, not
// an owner - it cannot help while the run is still going.
//
// So the margins are set here once, by whoever owns the whole zone, and every
// consumer asks for rows instead of taking the terminal.
//
// The corollary is that Zone is the concurrency boundary a region deliberately is
// not: a region is single-threaded by contract and its consumers here are not, so
// every region access goes through z.mu.
type Zone struct {
	mu     sync.Mutex
	w      io.Writer
	probe  Probe
	leases []*Lease
	// region is built on demand and REBUILT whenever the leased total changes,
	// because a region's height is fixed at construction. Nil until something
	// actually draws, so a run that never paints never touches the terminal.
	region *region
	// isTTY is settled once, at construction: a writer either has a terminal
	// that can be drawn on or it does not, and re-probing per paint would buy
	// nothing.
	// Whether the terminal is big ENOUGH is a separate question, re-asked per
	// grant and per paint, because the window can be resized.
	isTTY bool
	// titleL and titleR caption the top rule. Held here as well as on the
	// region because the region is REBUILT whenever the leased total changes,
	// and a caption must survive that.
	titleL, titleR string
}

// NewZone returns a Zone over w, measured through p. Nothing is written here.
//
// Pass [SystemProbe] in production; tests pass their own Probe and a writer
// with a synthetic descriptor.
func NewZone(w io.Writer, p Probe) *Zone {
	return &Zone{w: w, probe: p, isTTY: CanRender(w, p)}
}

// Guarded by a mutex rather than published through a Once, for the reason
// stated on stderrNotifierMu: ReleaseStderr reads this from the signal path
// while a worker may still be inside the constructor. A Once orders its own
// callers and says nothing about a reader outside it.
var (
	stderrZoneMu  sync.Mutex
	stderrZoneVal *Zone
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
	stderrZoneMu.Lock()
	defer stderrZoneMu.Unlock()
	if stderrZoneVal == nil {
		stderrZoneVal = NewZone(os.Stderr, SystemProbe)
	}
	return stderrZoneVal
}

// takeStderrZone hands the process-wide zone to an exit path and forgets it,
// reporting nil when none was ever created. Separate from [StderrZone] because
// creating a zone in order to close it would set margins nothing asked for.
func takeStderrZone() *Zone {
	stderrZoneMu.Lock()
	defer stderrZoneMu.Unlock()
	z := stderrZoneVal
	stderrZoneVal = nil
	return z
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
// A NIL *Lease is disabled too, exactly like the zero one: a consumer that has
// not needed rows yet holds nothing, and every method here answers for that
// without the caller checking.
//
// The zero Lease is the DISABLED lease: it is what Acquire hands back when
// there is no terminal or no room, and every method on it is a safe no-op. That
// is what lets a caller write the same code for a TTY and a CI log.
type Lease struct {
	zone     *Zone
	rows     int
	band     []Line
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
	if err != nil || !fits(width, height, total+borderRows) {
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
//
// Takes the zone's lock because `released` is written under it by Release and
// Zone.Close, and Close runs from the process exit path - including the
// signal-driven one - while the run's own goroutines are still asking.
func (l *Lease) Enabled() bool {
	if l == nil || l.zone == nil {
		return false
	}
	l.zone.mu.Lock()
	defer l.zone.mu.Unlock()
	return !l.released
}

// Set replaces this lease's band and repaints the zone, reporting whether the
// rows actually reached the terminal.
//
// The bool is the point of the signature: only the caller knows what its content
// IS. A failure line is a RECORD and must be printed plainly when it cannot be
// pinned; a status line is a VIEW and must be dropped. Returning false keeps that
// judgment where the knowledge is.
//
// Fewer rows than the lease holds leaves the remainder blank, which is what
// lets an entry disappear; more are dropped.
func (l *Lease) Set(rows []Line) (rendered bool, err error) {
	if l == nil || l.zone == nil {
		return false, nil
	}
	l.zone.mu.Lock()
	defer l.zone.mu.Unlock()
	if l.released {
		return false, nil
	}
	l.band = rows
	return l.zone.repaint()
}

// Release gives this lease's rows back and repaints without them. Idempotent
// and safe to defer. Releasing the last lease hands the terminal back
// entirely: margins reset, rows returned.
func (l *Lease) Release() error {
	if l == nil || l.zone == nil {
		return nil
	}
	z := l.zone
	z.mu.Lock()
	defer z.mu.Unlock()
	if l.released {
		return nil
	}
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
// The zone already knows where every band sits because it put them there, so a
// caller passes Row from an [Event] and never computes a row itself - there is no
// second copy of the layout to drift.
//
// Reports false for any row outside the reserved zone, including all the ordinary
// scrolling output above it: a click there belongs to the terminal's selection.
func (z *Zone) HitTest(row int) (lease *Lease, index int, ok bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.region == nil || !z.region.isEnabled() || !z.region.open {
		return nil, 0, false
	}
	// The border occupies the region's first row and belongs to no lease, so a
	// click on it resolves to nothing rather than to the top lease's first row.
	offset := row - z.region.firstRow() - borderRowsPerEdge
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

// SetTitle captions the zone's top rule, costing no row.
//
// On the Zone rather than a Lease because the rule is the zone's - there is one
// box however many leases stack inside it, so a caption is a property of the
// whole, and two leases both claiming it would fight. The run's status line is
// the intended caller: it describes the band, not any one band's rows.
func (z *Zone) SetTitle(left, right string) (rendered bool, err error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.titleL == left && z.titleR == right {
		return z.region != nil && z.region.isEnabled(), nil
	}
	z.titleL, z.titleR = left, right
	if z.region != nil {
		z.region.setTitle(left, right)
	}
	return z.repaint()
}

// Width reports the columns a row of this lease can use, or 0 when it cannot
// draw. It is what a caller needs to know whether its text FITS - the notifier
// scrolls a message only when it does not.
func (l *Lease) Width() int {
	if l == nil || l.zone == nil {
		return 0
	}
	l.zone.mu.Lock()
	defer l.zone.mu.Unlock()
	if l.released || l.zone.region == nil || !l.zone.region.isEnabled() {
		return 0
	}
	return l.zone.region.innerWidth()
}

// HitSpan reports which keyed span a click at (row, col) landed on.
//
// Keyed rather than positional: the caller labels the span it draws and gets that
// label back, so a hint cannot be drawn in one place and hit-tested in another.
//
// Extents are recomputed from the stored line rather than cached at paint time. A
// click is a human-speed event, so the arithmetic is free, and a cache would need
// invalidating on every repaint and resize.
func (z *Zone) HitSpan(row, col int) (key string, ok bool) {
	lease, index, ok := z.HitTest(row)
	if !ok {
		return "", false
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	// Re-checked under THIS lock, not carried over from HitTest's: the two are
	// separate acquisitions (the mutex is not reentrant, so they cannot be one),
	// and the band can be released or repainted in between.
	if z.region == nil || lease.released || index >= len(lease.band) {
		return "", false
	}
	// Against the INNER width and shifted past the left edge: the row on screen
	// is framed, so a column the reader clicked is that many columns further
	// right than the same column in the caller's own line.
	_, extents, _ := layoutExtents(lease.band[index].spans(), z.region.innerWidth())
	col -= borderCols
	for _, e := range extents {
		if col >= e.from && col <= e.to {
			return e.key, true
		}
	}
	return "", false
}

// Grow enlarges this lease to rows.
//
// It returns only an error, deliberately. It used to return a bool as well, in
// the same position as [Lease.Set]'s and meaning something else - Set's reports
// whether the paint reached the terminal, Grow's reported whether the model
// changed - so a caller reading the two alike was wrong at one of them. A
// caller that needs to know whether it actually got rows asks [Lease.Rows],
// which is unambiguous.
//
// A refusal is not an error: released, already large enough, no descriptor and
// no room all leave the lease the size it was and report nil. That is the
// arbitration rule, not a failure.
//
// Growth ONLY: a band that shrank again would reflow the zone every time its
// content came and went, and a reflow moves the whole screen. Growing on demand
// lets a consumer claim one row for one notification rather than reserving for its
// worst case and charging every run for the possibility.
func (l *Lease) Grow(rows int) error {
	if l == nil || l.zone == nil {
		return nil
	}
	z := l.zone
	z.mu.Lock()
	defer z.mu.Unlock()
	if l.released || rows <= l.rows {
		return nil
	}

	total := rows - l.rows
	for _, other := range z.leases {
		total += other.rows
	}
	fd, ok := Fd(z.w)
	if !ok {
		return nil
	}
	width, height, sizeErr := z.probe.Size(fd)
	if sizeErr != nil || !fits(width, height, total+borderRows) {
		return nil
	}
	// Committed before the repaint, so a repaint error leaves the lease holding
	// rows the zone never painted. Reported rather than swallowed: the caller
	// decides whether a terminal that refused the write is worth acting on.
	l.rows = rows
	_, err := z.repaint()
	return err
}

// Rows reports how many rows this lease holds, for a caller bounds-checking an
// index it got from [Zone.HitTest] against its own content. A released lease
// holds none.
func (l *Lease) Rows() int {
	if l == nil || l.zone == nil {
		return 0
	}
	l.zone.mu.Lock()
	defer l.zone.mu.Unlock()
	if l.released {
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
	_, err := z.repaint()
	return err
}

// repaint composites every lease's band into one slice and drives the region.
// The caller holds z.mu.
//
// The region is rebuilt whenever the leased total changes because its height is
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
		err := z.region.release()
		z.region = nil
		return false, err
	}
	if z.region == nil || z.region.height != total+borderRows {
		var relErr error
		if z.region != nil {
			relErr = z.region.release()
		}
		z.region = newRegion(z.w, total+borderRows, z.probe)
		z.region.setTitle(z.titleL, z.titleR)
		if relErr != nil {
			return false, relErr
		}
	}
	if !z.region.isEnabled() {
		return false, nil
	}

	// Content only: the region wraps these in its own box, because the width the
	// edges have to fill is its geometry and not the zone's.
	rows := make([]Line, 0, total)
	for _, l := range z.leases {
		for i := range l.rows {
			if i < len(l.band) {
				rows = append(rows, l.band[i])
				continue
			}
			rows = append(rows, Line{})
		}
	}
	if err := z.region.render(rows); err != nil {
		return false, err
	}
	// Render can disable the region on the way through when the window has
	// shrunk past what the leases need, so the answer is read AFTER the paint,
	// not before it.
	return z.region.isEnabled(), nil
}
