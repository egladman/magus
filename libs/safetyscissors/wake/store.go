package wake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// Log is where a [Store] keeps its records, one line per entry under a key. The caller
// supplies it; the package names no path.
//
// Append is durable when it returns nil. Read yields lines oldest first, and may already
// have dropped a line a later line of the same key replaced. One Store writes at a time.
type Log interface {
	Append(ctx context.Context, key string, line []byte) error
	Read(ctx context.Context) ([][]byte, error)
}

// MemoryLog is a [Log] held in memory, usable as its zero value and safe for concurrent
// use. It keeps every line it is given.
type MemoryLog struct {
	mu    sync.Mutex
	lines [][]byte
}

// Append keeps a copy of line. The key goes unused, since Read drops nothing.
func (m *MemoryLog) Append(ctx context.Context, _ string, line []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lines = append(m.lines, bytes.Clone(line))
	return nil
}

// Read returns a copy of every line, oldest first.
func (m *MemoryLog) Read(ctx context.Context) ([][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.lines))
	for i, l := range m.lines {
		out[i] = bytes.Clone(l)
	}
	return out, nil
}

// Option sets one of a Store's seams at [Open]. An option given nil makes Open fail with
// an [*OptionError].
type Option func(*options)

type options struct {
	clock  func() time.Time
	pricer budget.Pricer
	errs   []error
}

// OptionError is an [Option] given a value it cannot use.
type OptionError struct {
	Option  string
	Problem string
}

func (e *OptionError) Error() string {
	return fmt.Sprintf("wake: option %s: %s", e.Option, e.Problem)
}

// WithClock sets the clock the store stamps records by, time.Now unless set.
func WithClock(clock func() time.Time) Option {
	return func(o *options) {
		if clock == nil {
			o.errs = append(o.errs, &OptionError{Option: "WithClock", Problem: "the clock is nil"})
			return
		}
		o.clock = clock
	}
}

// WithPricer sets the pricer the store prices reports by. A wake with a spend bound
// needs one; with none, [Store.Begin] refuses terms that set MaxUSD.
func WithPricer(p budget.Pricer) Option {
	return func(o *options) {
		if p == nil {
			o.errs = append(o.errs, &OptionError{Option: "WithPricer", Problem: "the pricer is nil"})
			return
		}
		o.pricer = p
	}
}

// Store keeps wakes in a [Log] and refuses any write past their terms. Every call reads
// the log and folds it, so a Store holds no wake between calls. It is safe for concurrent
// use: its calls are serialized.
type Store struct {
	mu     sync.Mutex
	log    Log
	clock  func() time.Time
	pricer budget.Pricer
}

// Open returns a Store over log. It does no I/O. Every refused option is reported at
// once, joined.
func Open(log Log, opts ...Option) (*Store, error) {
	o := options{clock: time.Now}
	for _, opt := range opts {
		opt(&o)
	}
	if log == nil {
		o.errs = append(o.errs, errors.New("wake: Open needs a Log, and was given nil"))
	}
	if err := errors.Join(o.errs...); err != nil {
		return nil, err
	}
	return &Store{log: log, clock: o.clock, pricer: o.pricer}, nil
}

func (s *Store) now() int64 { return s.clock().Unix() }

// Begin records t as a new wake and returns it. The store sets t.At. Every problem with
// t is a [*TermsError], joined; an ID already begun is [ErrDuplicateWake].
func (s *Store) Begin(ctx context.Context, t Terms) (Wake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if err := s.checkTerms(t, now); err != nil {
		return Wake{}, err
	}
	wakes, err := s.fold(ctx)
	if err != nil {
		return Wake{}, err
	}
	if _, ok := wakes.get(t.ID); ok {
		return Wake{}, fmt.Errorf("%w: %s", ErrDuplicateWake, t.ID)
	}
	t.At = now
	if err := s.append(ctx, t.key(), t); err != nil {
		return Wake{}, err
	}
	return Wake{Terms: t}, nil
}

func (s *Store) checkTerms(t Terms, now int64) error {
	var errs []error
	refuse := func(field, problem string) {
		errs = append(errs, &TermsError{Field: field, Problem: problem})
	}
	if !validID(t.ID) {
		refuse("ID", fmt.Sprintf("%q is empty or holds whitespace", t.ID))
	}
	if t.At != 0 {
		refuse("At", "the store sets it")
	}
	switch {
	case t.Until == 0:
		refuse("Until", "is required")
	case t.Until <= now:
		refuse("Until", fmt.Sprintf("%d is not after now, %d", t.Until, now))
	}
	if t.MaxWakes <= 0 {
		refuse("MaxWakes", "is required and above 0")
	}
	if t.MaxIdle <= 0 {
		refuse("MaxIdle", "is required and above 0")
	}
	if t.MinGap <= 0 {
		refuse("MinGap", "is required and above 0")
	}
	switch {
	case t.MaxUSD < 0:
		refuse("MaxUSD", "is below 0, and 0 means no spend bound")
	case t.MaxUSD > 0 && s.pricer == nil:
		refuse("MaxUSD", "a spend bound needs a pricer, and the store was opened without WithPricer")
	}
	return errors.Join(errs...)
}

// Start records the next turn of wake id as begun. It refuses an ended wake with an
// [*EndedError], a turn still open with an [*OpenError], and a start before
// [Next.NotBefore] with a [*TooSoonError].
func (s *Store) Start(ctx context.Context, id string) (Wake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.get(ctx, id)
	if err != nil {
		return Wake{}, err
	}
	now := s.now()
	next := Decide(w, now)
	switch {
	case next.End != "":
		return Wake{}, &EndedError{Wake: id, End: next.End, At: next.EndedAt}
	case next.Open != 0:
		return Wake{}, &OpenError{Wake: id, N: next.Open}
	case now < next.NotBefore:
		return Wake{}, &TooSoonError{Wake: id, Now: now, NotBefore: next.NotBefore}
	}
	st := Start{Wake: id, N: w.N + 1, At: now}
	if err := s.append(ctx, st.key(), st); err != nil {
		return Wake{}, err
	}
	w.Open = st
	return w, nil
}

// RecordWake files r as the open turn's report and returns the wake to ask [Decide]
// about. The store sets r.Started, r.Ended and r.USD.
//
// An ended wake is an [*EndedError], and a report for another turn a [*NotNextError].
// A malformed report is [*ReportError]s joined. Usage the pricer cannot price is an
// [*UnpricedError].
func (s *Store) RecordWake(ctx context.Context, r Report) (Wake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.get(ctx, r.Wake)
	if err != nil {
		return Wake{}, err
	}
	now := s.now()
	if next := Decide(w, now); next.End != "" {
		return Wake{}, &EndedError{Wake: r.Wake, End: next.End, At: next.EndedAt}
	}
	if w.Open.N == 0 || r.N != w.Open.N {
		return Wake{}, &NotNextError{Wake: r.Wake, Want: w.Open.N, Got: r.N}
	}
	spendBound := w.Terms.MaxUSD > 0
	if err := checkReport(r, spendBound); err != nil {
		return Wake{}, err
	}
	if spendBound {
		if r.USD, err = price(s.pricer, r.Model, r.Calls); err != nil {
			return Wake{}, err
		}
	}
	r.Started, r.Ended = w.Open.At, now
	if err := s.append(ctx, r.key(), r); err != nil {
		return Wake{}, err
	}
	w.applyReport(r)
	return w, nil
}

func checkReport(r Report, spendBound bool) error {
	var errs []error
	refuse := func(field, problem string) {
		errs = append(errs, &ReportError{Wake: r.Wake, N: r.N, Field: field, Problem: problem})
	}
	if r.Started != 0 {
		refuse("Started", "the store sets it")
	}
	if r.Ended != 0 {
		refuse("Ended", "the store sets it")
	}
	if r.USD != 0 {
		refuse("USD", "the store prices it")
	}
	if r.Acted < 0 {
		refuse("Acted", "is below 0")
	}
	if r.Pending < 0 {
		refuse("Pending", "is below 0")
	}
	if slices.ContainsFunc(r.Calls, Usage.negative) {
		refuse("Calls", "a token count is below 0")
	}
	if spendBound {
		if r.Model == "" {
			refuse("Model", "a wake with a spend bound prices each report by its model")
		}
		if len(r.Calls) == 0 {
			refuse("Calls", "a wake with a spend bound needs the usage of every model call")
		}
	}
	return errors.Join(errs...)
}

func (u Usage) negative() bool {
	return u.Input < 0 || u.CacheWrite5m < 0 || u.CacheWrite1h < 0 || u.CacheRead < 0 || u.Output < 0
}

type priced struct {
	class  string
	tokens int64
	rate   budget.USDPerMTok
}

func price(p budget.Pricer, model string, calls []Usage) (float64, error) {
	rates, ok := p.Price(model)
	if !ok {
		return 0, &UnpricedError{Model: model}
	}
	var usd float64
	for _, c := range calls {
		var sum float64
		for _, k := range []priced{
			{"input", c.Input, rates.Input},
			{"cache_write_5m", c.CacheWrite5m, rates.CacheWrite5m},
			{"cache_write_1h", c.CacheWrite1h, rates.CacheWrite1h},
			{"cache_read", c.CacheRead, rates.CacheRead},
			{"output", c.Output, rates.Output},
		} {
			if k.tokens == 0 {
				continue
			}
			if k.rate == 0 {
				return 0, &UnpricedError{Model: model, Class: k.class}
			}
			sum += float64(k.tokens) * float64(k.rate) / 1e6
		}
		usd += sum * rates.Multiplier(c.Input+c.CacheWrite5m+c.CacheWrite1h+c.CacheRead)
	}
	return usd, nil
}

// Stop ends wake id as a person's act, by whom and why. An ended wake is refused with an
// [*EndedError], so a stop never replaces an end already reached.
func (s *Store) Stop(ctx context.Context, id string, by job.Origin, why string) (Wake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.get(ctx, id)
	if err != nil {
		return Wake{}, err
	}
	now := s.now()
	if next := Decide(w, now); next.End != "" {
		return Wake{}, &EndedError{Wake: id, End: next.End, At: next.EndedAt}
	}
	st := Stop{Wake: id, At: now, By: by, Why: why}
	if err := s.append(ctx, st.key(), st); err != nil {
		return Wake{}, err
	}
	w.Stopped = st
	return w, nil
}

// Get returns wake id, or [ErrUnknownWake].
func (s *Store) Get(ctx context.Context, id string) (Wake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(ctx, id)
}

// Read returns every wake, in the order they were begun.
func (s *Store) Read(ctx context.Context) ([]Wake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wakes, err := s.fold(ctx)
	if err != nil {
		return nil, err
	}
	return wakes.list, nil
}

func (s *Store) get(ctx context.Context, id string) (Wake, error) {
	wakes, err := s.fold(ctx)
	if err != nil {
		return Wake{}, err
	}
	w, ok := wakes.get(id)
	if !ok {
		return Wake{}, fmt.Errorf("%w: %s", ErrUnknownWake, id)
	}
	return w, nil
}

func (s *Store) append(ctx context.Context, key string, entry any) error {
	line, err := codec.Marshal(entry)
	if err != nil {
		return fmt.Errorf("wake: encode %s: %w", key, err)
	}
	if err := s.log.Append(ctx, key, line); err != nil {
		return fmt.Errorf("wake: append %s: %w", key, err)
	}
	return nil
}

type folded struct {
	list  []Wake
	index map[string]int
}

func (f folded) get(id string) (Wake, bool) {
	i, ok := f.index[id]
	if !ok {
		return Wake{}, false
	}
	return f.list[i], true
}

// entry is one decoded log line: exactly one of its pointers is set.
type entry struct {
	key, wake string
	terms     *Terms
	start     *Start
	report    *Report
	stop      *Stop
}

func (s *Store) fold(ctx context.Context) (folded, error) {
	lines, err := s.log.Read(ctx)
	if err != nil {
		return folded{}, fmt.Errorf("wake: read the log: %w", err)
	}
	entries := make([]entry, len(lines))
	last := make(map[string]int, len(lines))
	for i, line := range lines {
		e, err := decodeEntry(line)
		if err != nil {
			return folded{}, &CorruptError{Line: i + 1, Err: err}
		}
		entries[i], last[e.key] = e, i
	}
	f := folded{index: make(map[string]int)}
	for i, e := range entries {
		if last[e.key] != i {
			continue
		}
		if err := f.apply(e); err != nil {
			return folded{}, &CorruptError{Line: i + 1, Err: err}
		}
	}
	return f, nil
}

func decodeEntry(line []byte) (entry, error) {
	var probe apiversion.TypeMeta
	if err := codec.Unmarshal(line, &probe); err != nil {
		return entry{}, err
	}
	switch probe {
	case header(KindTerms):
		var t Terms
		err := codec.Unmarshal(line, &t)
		return entry{key: t.key(), wake: t.ID, terms: &t}, err
	case header(KindStart):
		var st Start
		err := codec.Unmarshal(line, &st)
		return entry{key: st.key(), wake: st.Wake, start: &st}, err
	case header(KindReport):
		var r Report
		err := codec.Unmarshal(line, &r)
		return entry{key: r.key(), wake: r.Wake, report: &r}, err
	case header(KindStop):
		var st Stop
		err := codec.Unmarshal(line, &st)
		return entry{key: st.key(), wake: st.Wake, stop: &st}, err
	}
	return entry{}, &apiversion.RefusedError{Want: header(KindTerms), Got: probe}
}

func (f *folded) apply(e entry) error {
	if e.terms != nil {
		if !validID(e.wake) {
			return fmt.Errorf("terms ID %q is empty or holds whitespace", e.wake)
		}
		f.index[e.wake] = len(f.list)
		f.list = append(f.list, Wake{Terms: *e.terms})
		return nil
	}
	i, ok := f.index[e.wake]
	if !ok {
		return fmt.Errorf("%s names a wake with no terms before it", e.key)
	}
	w := &f.list[i]
	switch {
	case e.start != nil:
		if e.start.N != w.N+1 {
			return fmt.Errorf("%s follows report %d", e.key, w.N)
		}
		w.Open = *e.start
	case e.report != nil:
		if e.report.N != w.N+1 {
			return fmt.Errorf("%s follows report %d", e.key, w.N)
		}
		w.applyReport(*e.report)
	case e.stop != nil:
		w.Stopped = *e.stop
	}
	return nil
}

func (w *Wake) applyReport(r Report) {
	w.N, w.Last = r.N, r
	w.SpentUSD += r.USD
	w.Open = Start{}
	if r.Acted == 0 {
		w.Idle++
	} else {
		w.Idle = 0
	}
}
