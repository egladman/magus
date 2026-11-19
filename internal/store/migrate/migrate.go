package migrate

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
)

// versionMember is the header member that records a store's version: the api_version the
// last migration applied to it converted it to.
const versionMember = "api_version"

// numberedMember is the header member a store recorded its version in before versions
// were api versions: how many migrations had been applied to it.
//
// compat(until: the next release): [Store.At] reads it as the version of the migration it
// counts to, and [Store.Apply] rewrites the header without it.
const numberedMember = "migration"

// Numbered reports whether header counts its migrations rather than naming its version.
// [Store.Apply] rewrites such a header even when no migration is due.
//
// compat(until: the next release): see [numberedMember].
func Numbered(header Entry) bool {
	_, ok := header[numberedMember]
	return ok
}

// Entry is one stored line as its JSON members, before the entry type decodes it.
type Entry map[string]codec.RawMessage

// Migration converts every entry of a store to the next api_version, the way a Kubernetes
// conversion takes an object from one version of its group to the next.
type Migration struct {
	// Name says what the migration changes, as the notice of a migration names it.
	Name string
	// To is the api_version the migration converts the store to. Within a group each
	// migration's is newer than the one before it by [apiversion.APIVersion.Compare].
	To apiversion.APIVersion
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
	// version. [Store.Apply] refuses a file with more than one. Every other line is an
	// entry.
	Header func(Entry) bool
	// Migrations are the store's migrations in order: each converts the store from the
	// To of the one before it to its own.
	Migrations []Migration
	// Stamp rewrites the header of a rewrite once the migrations have run, as a writer
	// records itself there; nil leaves the header as the migrations made it. The version
	// member is set after it.
	Stamp func(Entry) (Entry, error)
}

// Version is the api_version this build writes: the last migration's To, "" with none.
func (s Store) Version() apiversion.APIVersion {
	if len(s.Migrations) == 0 {
		return ""
	}
	return s.Migrations[len(s.Migrations)-1].To
}

// NewerError is a store whose version is past every migration this build knows. Nothing
// reads it: an entry written under a later migration means something this build cannot
// tell.
type NewerError struct {
	Store, Path  string
	Found, Known apiversion.APIVersion
}

func (e *NewerError) Error() string {
	return fmt.Sprintf("%s %s is at %s, newer than this magus knows (%s): update magus",
		e.Store, e.Path, e.Found, e.Known)
}

// unknownVersionError is a store header naming an api_version in the group of a migration
// that no migration converts to and that is not newer than [Store.Version]. Nothing was
// read: no migration says what its entries mean.
type unknownVersionError struct {
	Store, Path string
	Found       apiversion.APIVersion
	// Known are the versions the migrations convert to, in order.
	Known []apiversion.APIVersion
}

func (e *unknownVersionError) Error() string {
	known := make([]string, len(e.Known))
	for i, v := range e.Known {
		known[i] = string(v)
	}
	return fmt.Sprintf("%s %s is at %s, which no migration this magus knows converts to (%s):"+
		" run the magus that wrote it, or move it aside with `mv %s %s.damaged`",
		e.Store, e.Path, e.Found, strings.Join(known, ", "), e.Path, e.Path)
}

// At is how many of s's migrations the store whose header is header has been through.
//
// A header naming the To of a migration is at that migration. One naming no api_version,
// or one in a group no migration converts to, was written before the store's first
// migration and is at none. A version newer than [Store.Version] in its group is a
// [*NewerError], and any other a [*unknownVersionError].
func (s Store) At(header Entry) (int, error) {
	if err := s.checkMigrations(); err != nil {
		return 0, err
	}
	if raw, ok := header[numberedMember]; ok {
		return s.numbered(raw)
	}
	raw, ok := header[versionMember]
	if !ok {
		return 0, nil
	}
	var found apiversion.APIVersion
	if err := codec.Unmarshal(raw, &found); err != nil {
		return 0, fmt.Errorf("%s: %s: header member %q: %w", s.Name, s.Path, versionMember, err)
	}
	inGroup := false
	for i, m := range s.Migrations {
		if m.To == found {
			return i + 1, nil
		}
		inGroup = inGroup || m.To.Group() == found.Group()
	}
	if !inGroup {
		return 0, nil
	}
	if head := s.Version(); head.Group() == found.Group() && found.Compare(head) > 0 {
		return 0, &NewerError{Store: s.Name, Path: s.Path, Found: found, Known: head}
	}
	known := make([]apiversion.APIVersion, len(s.Migrations))
	for i, m := range s.Migrations {
		known[i] = m.To
	}
	return 0, &unknownVersionError{Store: s.Name, Path: s.Path, Found: found, Known: known}
}

// numbered is how many migrations the numbered header member raw counts.
//
// compat(until: the next release): see [numberedMember].
func (s Store) numbered(raw codec.RawMessage) (int, error) {
	var n int
	if err := codec.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("%s: %s: header member %q: %w", s.Name, s.Path, numberedMember, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s: %s: header member %q is negative", s.Name, s.Path, numberedMember)
	}
	if n > len(s.Migrations) {
		return 0, fmt.Errorf("%s %s is at migration %d, newer than this magus knows (%d, which is %s): update magus",
			s.Name, s.Path, n, len(s.Migrations), cmp.Or(string(s.Version()), "unversioned"))
	}
	return n, nil
}

// checkMigrations refuses migrations whose To is malformed or, within one group, not
// newer than the To before it: the order of versions is the order of migrations.
func (s Store) checkMigrations() error {
	for i, m := range s.Migrations {
		if !m.To.Valid() {
			return fmt.Errorf("%s: migration %d (%s) converts to %q, which is no api_version", s.Name, i+1, m.Name, m.To)
		}
		if i == 0 {
			continue
		}
		if prev := s.Migrations[i-1].To; prev.Group() == m.To.Group() && m.To.Compare(prev) <= 0 {
			return fmt.Errorf("%s: migration %d (%s) converts to %s, which is not newer than migration %d's %s",
				s.Name, i+1, m.Name, m.To, i, prev)
		}
	}
	return nil
}

// result is what [Store.Apply] did.
type result struct {
	Store, Path string
	// From and To are the versions the file was converted from and to, From "" for a file
	// written before the store's first migration.
	From, To apiversion.APIVersion
	// Entries is how many lines the migrations rewrote, superseded versions of an entry
	// included.
	Entries int
	// Numbered says the header counted its migrations, which the rewrite respelled as
	// its api_version.
	//
	// compat(until: the next release): see [numberedMember].
	Numbered bool
	// Unfinished says the file ended in a line no writer finished, which the rewrite dropped.
	Unfinished bool
}

// Migrated reports whether Apply rewrote the file.
func (r result) Migrated() bool { return r.From != r.To || r.Numbered }

func (r result) String() string {
	s := fmt.Sprintf("migrated %s %s from %s to %s (%d entries)", r.Store, r.Path, cmp.Or(string(r.From), "unversioned"), r.To, r.Entries)
	if r.Numbered {
		s += fmt.Sprintf("; the header's %q member became its %q", numberedMember, versionMember)
	}
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
// as it was. A newer one is a [*NewerError], and one at a version no migration converts
// to an [*unknownVersionError].
func (s Store) Apply() (result, error) {
	res := result{Store: s.Name, Path: s.Path, To: s.Version()}
	read, err := codec.ReadLines(s.Path)
	if err != nil {
		return res, fmt.Errorf("%s: %w", s.Name, err)
	}
	header, entries, err := s.split(read.Lines)
	if err != nil {
		return res, err
	}
	at, err := s.At(header)
	if err != nil {
		return res, err
	}
	if at > 0 {
		res.From = s.Migrations[at-1].To
	}
	res.Numbered = Numbered(header)
	if len(read.Lines) == 0 {
		res.To, res.Numbered = res.From, false
	}
	if !res.Migrated() {
		return res, nil
	}
	for i := at; i < len(s.Migrations); i++ {
		if header, err = s.upHeader(i, header); err != nil {
			return res, err
		}
		if err := s.up(i, entries); err != nil {
			return res, err
		}
	}
	if s.Stamp != nil {
		if header, err = s.Stamp(header); err == nil && header == nil {
			err = errors.New("returned no header")
		}
		if err != nil {
			return res, fmt.Errorf("%s: %s: stamping the header: %w", s.Name, s.Path, err)
		}
	}
	delete(header, numberedMember)
	if header[versionMember], err = codec.Marshal(res.To); err != nil {
		return res, fmt.Errorf("%s: %s: %w", s.Name, s.Path, err)
	}
	if err := s.rewrite(append([]Entry{header}, entries...)); err != nil {
		return res, err
	}
	res.Entries, res.Unfinished = len(entries), read.Partial
	return res, nil
}

// split decodes lines into the store's header, empty when it has none, and its entries in
// file order. More than one header line is refused: a reader that takes entries for headers
// sees several, and the rewrite would keep only the last.
func (s Store) split(lines []codec.Line) (header Entry, entries []Entry, err error) {
	header = Entry{}
	var at []string
	for _, line := range lines {
		var e Entry
		if err := codec.Unmarshal(line.Text, &e); err != nil {
			return nil, nil, fmt.Errorf("%s: %s:%d: %w", s.Name, s.Path, line.N, err)
		}
		if s.Header(e) {
			header = e
			at = append(at, strconv.Itoa(line.N))
			continue
		}
		entries = append(entries, e)
	}
	if len(at) > 1 {
		return nil, nil, fmt.Errorf("%s: %s: lines %s are each a header line, and a store has one:"+
			" rewriting it would keep the last and drop the rest, so nothing was migrated", s.Name, s.Path, strings.Join(at, ", "))
	}
	return header, entries, nil
}

// up applies the i-th migration to every entry, in place.
func (s Store) up(i int, entries []Entry) error {
	m := s.Migrations[i]
	for j, e := range entries {
		next, err := m.Up(e)
		if err == nil && next == nil {
			err = errors.New("returned no entry")
		}
		if err != nil {
			return fmt.Errorf("%s: %s: migration %d (%s): %w", s.Name, s.Path, i+1, m.Name, err)
		}
		entries[j] = next
	}
	return nil
}

// upHeader applies the i-th migration's header rewrite.
func (s Store) upHeader(i int, header Entry) (Entry, error) {
	m := s.Migrations[i]
	if m.Header == nil {
		return header, nil
	}
	next, err := m.Header(header)
	if err == nil && next == nil {
		err = errors.New("returned no header")
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %s: migration %d (%s): header: %w", s.Name, s.Path, i+1, m.Name, err)
	}
	return next, nil
}

// rewrite replaces the file with entries, one line each.
func (s Store) rewrite(entries []Entry) error {
	lines := make([][]byte, 0, len(entries))
	for _, e := range entries {
		b, err := codec.Marshal(e)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", s.Name, s.Path, err)
		}
		lines = append(lines, b)
	}
	if err := codec.RewriteLines(s.Path, lines, 0o644); err != nil {
		return fmt.Errorf("%s: %w", s.Name, err)
	}
	return nil
}
