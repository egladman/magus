package migrate

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
)

// versionMember is the header member that records a store's version: the number of
// migrations applied to it. An absent member is version 0.
const versionMember = "migration"

// Entry is one stored line as its JSON members, before the entry type decodes it.
type Entry map[string]json.RawMessage

// Migration takes every entry of a store from one version to the next.
type Migration struct {
	// Name says what the migration changes, as the notice of a migration names it.
	Name string
	// Up rewrites one entry. It reads nothing but the entry, so a migration applied to
	// the same file twice writes the same bytes.
	Up func(Entry) (Entry, error)
	// Header rewrites the header the same way, nil to leave it as it is. The
	// version member is set after it, so it never decides the version.
	Header func(Entry) (Entry, error)
}

// Store is what a JSON Lines store declares to be migrated.
type Store struct {
	// Name is how messages name the store, such as "job store".
	Name string
	Path string
	// Header reports whether a line is the store's header, the one line that records its
	// version. The last header line is the header, as a store reads it. Every other line is
	// an entry.
	Header func(Entry) bool
	// Migrations are the store's migrations in order: the i-th takes version i to i+1.
	Migrations []Migration
}

// Version is the version this build writes: one per migration it knows.
func (s Store) Version() int { return len(s.Migrations) }

// NewerError is a store whose version is past every migration this build knows. Nothing
// reads it: an entry written under a later migration means something this build cannot
// tell.
type NewerError struct {
	Store, Path  string
	Found, Known int
}

func (e *NewerError) Error() string {
	return fmt.Sprintf("%s %s is at migration %d, newer than this magus knows (%d): update magus",
		e.Store, e.Path, e.Found, e.Known)
}

// Check refuses version found when it is newer than s knows.
func (s Store) Check(found int) error {
	if found > s.Version() {
		return &NewerError{Store: s.Name, Path: s.Path, Found: found, Known: s.Version()}
	}
	return nil
}

// result is what [Store.Apply] did.
type result struct {
	Store, Path string
	From, To    int
	// Entries is how many lines the migrations rewrote, superseded versions of an entry
	// included.
	Entries int
	// Unfinished says the file ended in a line no writer finished, which the rewrite dropped.
	Unfinished bool
}

// Migrated reports whether Apply rewrote the file.
func (r result) Migrated() bool { return r.From < r.To }

func (r result) String() string {
	s := fmt.Sprintf("migrated %s %s from %d to %d (%d entries)", r.Store, r.Path, r.From, r.To, r.Entries)
	if r.Unfinished {
		s += "; dropped a last line no writer finished"
	}
	return s
}

// Apply migrates the file at s.Path to s.Version, once. The caller holds the store's
// write lock: Apply reads the whole file and replaces it.
//
// A file at the current version, or absent, is left alone. An older one is rewritten
// through a temporary file renamed over it, the header first and carrying the new
// version, then every entry in file order; a failing migration or write leaves the file
// as it was. A newer one is a [*NewerError].
func (s Store) Apply() (result, error) {
	res := result{Store: s.Name, Path: s.Path, To: s.Version()}
	read, err := file.ReadLines(s.Path)
	if err != nil {
		return res, fmt.Errorf("%s: %w", s.Name, err)
	}
	header, entries, err := s.split(read.Lines)
	if err != nil {
		return res, err
	}
	if res.From, err = version(header); err != nil {
		return res, fmt.Errorf("%s: %s: %w", s.Name, s.Path, err)
	}
	if err := s.Check(res.From); err != nil {
		return res, err
	}
	if len(read.Lines) == 0 {
		res.To = res.From
	}
	if !res.Migrated() {
		return res, nil
	}
	for v := res.From; v < res.To; v++ {
		if header, err = s.upHeader(v, header); err != nil {
			return res, err
		}
		if err := s.up(v, entries); err != nil {
			return res, err
		}
	}
	header[versionMember] = json.RawMessage(strconv.Itoa(res.To))
	if err := s.rewrite(append([]Entry{header}, entries...)); err != nil {
		return res, err
	}
	res.Entries, res.Unfinished = len(entries), read.Partial
	return res, nil
}

// split decodes lines into the store's header, empty when it has none, and its entries in
// file order.
func (s Store) split(lines []file.Line) (header Entry, entries []Entry, err error) {
	header = Entry{}
	for _, line := range lines {
		var e Entry
		if err := json.Unmarshal(line.Text, &e); err != nil {
			return nil, nil, fmt.Errorf("%s: %s:%d: %w", s.Name, s.Path, line.N, err)
		}
		if s.Header(e) {
			header = e
			continue
		}
		entries = append(entries, e)
	}
	return header, entries, nil
}

// up applies the migration that takes version v to v+1 to every entry, in place.
func (s Store) up(v int, entries []Entry) error {
	m := s.Migrations[v]
	for i, e := range entries {
		next, err := m.Up(e)
		if err == nil && next == nil {
			err = errors.New("returned no entry")
		}
		if err != nil {
			return fmt.Errorf("%s: %s: migration %d (%s): %w", s.Name, s.Path, v+1, m.Name, err)
		}
		entries[i] = next
	}
	return nil
}

// upHeader applies migration v's header rewrite, which takes v to v+1.
func (s Store) upHeader(v int, header Entry) (Entry, error) {
	m := s.Migrations[v]
	if m.Header == nil {
		return header, nil
	}
	next, err := m.Header(header)
	if err == nil && next == nil {
		err = errors.New("returned no header")
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %s: migration %d (%s): header: %w", s.Name, s.Path, v+1, m.Name, err)
	}
	return next, nil
}

// rewrite replaces the file with entries, one line each.
func (s Store) rewrite(entries []Entry) error {
	lines := make([][]byte, 0, len(entries))
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", s.Name, s.Path, err)
		}
		lines = append(lines, b)
	}
	if err := file.RewriteLines(s.Path, lines, 0o644); err != nil {
		return fmt.Errorf("%s: %w", s.Name, err)
	}
	return nil
}

func version(header Entry) (int, error) {
	raw, ok := header[versionMember]
	if !ok {
		return 0, nil
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, fmt.Errorf("header member %q: %w", versionMember, err)
	}
	if v < 0 {
		return 0, fmt.Errorf("header member %q is negative", versionMember)
	}
	return v, nil
}
