package symbols

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/scip-code/scip/bindings/go/scip"
)

// Shard is one unit's part of a symbol index: the documents of one Go package, or of one
// directory for an indexer that has no finer unit. Ranges are SCIP's packed form
// ([startLine, startChar, endChar] or [startLine, startChar, endLine, endChar], 0-based),
// so a shard converts to SCIP without reshaping.
//
// Gaps names each file of the unit the indexer could not index, with why ("a.sh: line 3:
// unclosed quote"): a file that does not parse is a gap, never a document with no symbols.
type Shard struct {
	Unit       string
	Documents  []Document
	MethodSets []MethodSet
	Gaps       []string
}

// Document is one source file's occurrences and the symbols it defines. Path is relative
// to the indexer's root, as a SCIP document path is.
type Document struct {
	Path        string
	Language    string
	Occurrences []Occurrence
	Symbols     []SymbolInfo
}

// Occurrence is one site of a symbol. Enclosing is empty when the indexer recorded no
// body for a definition.
type Occurrence struct {
	Symbol    string
	Range     []int32
	Roles     int32
	Enclosing []int32
}

// SymbolInfo is what a document records about a symbol it defines. Kind holds a
// scip.SymbolInformation_Kind.
type SymbolInfo struct {
	Symbol        string
	DisplayName   string
	Kind          int32
	Documentation []string
	Signature     string
	Relationships []Relationship
}

// Relationship mirrors scip.Relationship.
type Relationship struct {
	Symbol         string
	Implementation bool
	Reference      bool
	TypeDefinition bool
	Definition     bool
}

// MethodSet is a named type's methods, by name and a signature string comparable across
// packages. Implementation relationships are derived from method sets when a pack is read
// rather than stored on the implementer, so an interface added in one unit relates to
// implementers in units that were not re-indexed.
type MethodSet struct {
	Symbol    string
	Interface bool
	Methods   []Method
}

// Method is one entry of a MethodSet. Symbol is "" for a method the type promotes from
// an embedded type it does not declare.
type Method struct {
	Name      string
	Signature string
	Symbol    string
}

// Defined is the sorted set of global symbols s defines.
func (s *Shard) Defined() []string {
	seen := map[string]bool{}
	for _, doc := range s.Documents {
		for _, occ := range doc.Occurrences {
			if occ.Roles&int32(scip.SymbolRole_Definition) != 0 && !scip.IsLocalSymbol(occ.Symbol) {
				seen[occ.Symbol] = true
			}
		}
	}
	return sortedKeys(seen)
}

// References reports whether s holds a reference to any symbol in set.
func (s *Shard) References(set map[string]bool) bool {
	for _, doc := range s.Documents {
		for _, occ := range doc.Occurrences {
			if occ.Roles&int32(scip.SymbolRole_Definition) == 0 && set[occ.Symbol] {
				return true
			}
		}
	}
	return false
}

// Unit is what an indexer declares about one unit before indexing it: the digest of every
// input that decides its shard, and the units its shard's references resolve into.
type Unit struct {
	ID     string
	Digest string
	Deps   []string
}

// UnitIndexer is an indexer that works per unit. UpdatePack asks it for every unit, then
// hands Index only the units whose digest moved, and later any dependents whose
// references a removed symbol invalidated.
type UnitIndexer interface {
	// Version names the indexer and what shapes its output; a pack written by another
	// version is rebuilt whole.
	Version() string
	Units(ctx context.Context) ([]Unit, error)
	// Index returns one shard per unit asked for, in any order.
	Index(ctx context.Context, units []Unit) ([]Shard, error)
}

// The pack is magus's own index layout: shards one after another, each its own gob
// stream, then a table of contents and a fixed trailer holding the table's offset and
// length. Reading one shard costs the trailer, the table and that shard's bytes; nothing
// decodes the whole index. Rewriting copies unchanged shards' bytes without decoding
// them. gob because a pack is a cache entry read back only by a binary whose version
// matches packMagic and the indexer's.
const (
	packMagic   = "magus-symbol-pack 1\n"
	trailerSize = 16
)

// ErrIndexFormat marks an index file that does not decode: not a pack this binary reads,
// or not SCIP protobuf.
var ErrIndexFormat = errors.New("not a symbol index this binary reads")

// packEntry is one unit's row in a pack's table of contents. Gaps copies the shard's, so
// listing every gap reads the table and no shard.
type packEntry struct {
	Unit   string
	Digest string
	Deps   []string
	Gaps   []string
	Offset int64
	Length int64
}

type packTOC struct {
	Indexer string
	Entries []packEntry
}

// packFile is an open pack file. Not safe for concurrent use; Close releases the file.
type packFile struct {
	f   *os.File
	toc packTOC
	at  map[string]int
}

// openPack opens the pack at path. A missing file is an fs.ErrNotExist error; any other
// file there is ErrIndexFormat.
func openPack(path string) (*packFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	p, err := readPack(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

func readPack(f *os.File) (*packFile, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < int64(len(packMagic))+trailerSize {
		return nil, ErrIndexFormat
	}
	head := make([]byte, len(packMagic))
	if _, err := f.ReadAt(head, 0); err != nil || string(head) != packMagic {
		return nil, ErrIndexFormat
	}
	var trailer [trailerSize]byte
	if _, err := f.ReadAt(trailer[:], info.Size()-trailerSize); err != nil {
		return nil, err
	}
	off, n := int64(binary.BigEndian.Uint64(trailer[:8])), int64(binary.BigEndian.Uint64(trailer[8:]))
	if off < int64(len(packMagic)) || n < 0 || off+n > info.Size()-trailerSize {
		return nil, ErrIndexFormat
	}
	p := &packFile{f: f, at: map[string]int{}}
	if err := gob.NewDecoder(io.NewSectionReader(f, off, n)).Decode(&p.toc); err != nil {
		return nil, fmt.Errorf("%w: table of contents: %w", ErrIndexFormat, err)
	}
	for i, e := range p.toc.Entries {
		p.at[e.Unit] = i
	}
	return p, nil
}

// Close closes the pack's file.
func (p *packFile) Close() error { return p.f.Close() }

// Indexer is the version of the indexer that wrote p.
func (p *packFile) Indexer() string { return p.toc.Indexer }

// Entries is p's table of contents, sorted by unit.
func (p *packFile) Entries() []packEntry { return p.toc.Entries }

// Entry is unit's row, and false when p holds no such unit.
func (p *packFile) Entry(unit string) (packEntry, bool) {
	i, ok := p.at[unit]
	if !ok {
		return packEntry{}, false
	}
	return p.toc.Entries[i], true
}

// Shard reads and decodes one unit's shard. An absent unit is an fs.ErrNotExist error.
func (p *packFile) Shard(unit string) (Shard, error) {
	e, ok := p.Entry(unit)
	if !ok {
		return Shard{}, fmt.Errorf("unit %q: %w", unit, fs.ErrNotExist)
	}
	var s Shard
	if err := gob.NewDecoder(bufio.NewReader(io.NewSectionReader(p.f, e.Offset, e.Length))).Decode(&s); err != nil {
		return Shard{}, fmt.Errorf("unit %q: %w", unit, err)
	}
	return s, nil
}

// Shards decodes every shard, in table order.
func (p *packFile) Shards() ([]Shard, error) {
	out := make([]Shard, 0, len(p.toc.Entries))
	for _, e := range p.toc.Entries {
		s, err := p.Shard(e.Unit)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// raw is unit's encoded bytes, copied without a decode.
func (p *packFile) raw(e packEntry) ([]byte, error) {
	buf := make([]byte, e.Length)
	_, err := p.f.ReadAt(buf, e.Offset)
	return buf, err
}

// packWriter writes a pack to a temporary file beside its destination and renames it into
// place on commit, so a reader never sees a half-written pack.
type packWriter struct {
	f       *os.File
	w       *bufio.Writer
	off     int64
	entries []packEntry
}

func newPackWriter(path string) (*packWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), fmt.Sprintf(".%s.*", filepath.Base(path)))
	if err != nil {
		return nil, err
	}
	pw := &packWriter{f: f, w: bufio.NewWriterSize(f, 1<<20)}
	if err := pw.write([]byte(packMagic)); err != nil {
		return nil, errors.Join(err, pw.abort())
	}
	return pw, nil
}

func (pw *packWriter) write(b []byte) error {
	n, err := pw.w.Write(b)
	pw.off += int64(n)
	return err
}

func (pw *packWriter) add(unit Unit, gaps []string, encoded []byte) error {
	pw.entries = append(pw.entries, packEntry{Unit: unit.ID, Digest: unit.Digest, Deps: unit.Deps, Gaps: gaps, Offset: pw.off, Length: int64(len(encoded))})
	return pw.write(encoded)
}

func (pw *packWriter) commit(path, indexer string) error {
	slices.SortFunc(pw.entries, func(a, b packEntry) int { return cmp.Compare(a.Unit, b.Unit) })
	var toc bytes.Buffer
	if err := gob.NewEncoder(&toc).Encode(packTOC{Indexer: indexer, Entries: pw.entries}); err != nil {
		return errors.Join(err, pw.abort())
	}
	tocOff := pw.off
	if err := pw.write(toc.Bytes()); err != nil {
		return errors.Join(err, pw.abort())
	}
	var trailer [trailerSize]byte
	binary.BigEndian.PutUint64(trailer[:8], uint64(tocOff))
	binary.BigEndian.PutUint64(trailer[8:], uint64(toc.Len()))
	if err := pw.write(trailer[:]); err != nil {
		return errors.Join(err, pw.abort())
	}
	if err := pw.w.Flush(); err != nil {
		return errors.Join(err, pw.abort())
	}
	if err := pw.f.Close(); err != nil {
		return errors.Join(err, os.Remove(pw.f.Name()))
	}
	if err := os.Chmod(pw.f.Name(), 0o644); err != nil {
		return errors.Join(err, os.Remove(pw.f.Name()))
	}
	if err := os.Rename(pw.f.Name(), path); err != nil {
		return errors.Join(err, os.Remove(pw.f.Name()))
	}
	return nil
}

// abort closes and removes the temporary pack, returning what failed in doing so.
func (pw *packWriter) abort() error {
	return errors.Join(pw.f.Close(), os.Remove(pw.f.Name()))
}

func encodeShard(s Shard) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		return nil, fmt.Errorf("unit %q: %w", s.Unit, err)
	}
	return buf.Bytes(), nil
}

// writePack writes shards as a whole new pack at path, each under the Unit of the same ID.
func writePack(path, indexer string, units []Unit, shards []Shard) error {
	byID := make(map[string]Shard, len(shards))
	for _, s := range shards {
		byID[s.Unit] = s
	}
	pw, err := newPackWriter(path)
	if err != nil {
		return err
	}
	for _, u := range units {
		s, ok := byID[u.ID]
		if !ok {
			return errors.Join(fmt.Errorf("unit %q has no shard", u.ID), pw.abort())
		}
		enc, err := encodeShard(s)
		if err != nil {
			return errors.Join(err, pw.abort())
		}
		if err := pw.add(u, s.Gaps, enc); err != nil {
			return errors.Join(err, pw.abort())
		}
	}
	return pw.commit(path, indexer)
}

// UpdateResult says what UpdatePack did. Indexed holds the units whose digest moved,
// Dependents the units re-indexed because a removed symbol was referenced from them, and
// Kept the count of shards carried over unchanged. Gaps is every gap the updated pack
// holds, kept shards' included, so a file that still does not parse is named on every run.
type UpdateResult struct {
	Indexed    []string
	Dependents []string
	Dropped    []string
	Kept       int
	Rebuilt    bool
	Gaps       []string
}

// UpdatePack brings the pack at path up to date with ix: it indexes each unit whose
// digest moved or that the pack lacks, drops units ix no longer declares, and copies
// every other shard's bytes across. When a re-indexed unit no longer defines a symbol it
// did, every unit depending on it whose shard references that symbol is re-indexed too,
// until no removal is left unresolved. A missing pack, one in another format, or one
// written by another indexer version is rebuilt whole, and Rebuilt says so.
func UpdatePack(ctx context.Context, path string, ix UnitIndexer) (UpdateResult, error) {
	var res UpdateResult
	old, ok, err := openPrior(path, ix.Version())
	if err != nil {
		return res, err
	}
	if ok {
		defer old.Close()
	}
	res.Rebuilt = !ok
	units, err := ix.Units(ctx)
	if err != nil {
		return res, err
	}
	dirty := dirtyUnits(old, units)
	for _, u := range dirty {
		res.Indexed = append(res.Indexed, u.ID)
	}
	removed, err := droppedDefinitions(old, units, &res)
	if err != nil {
		return res, err
	}
	fresh, err := reindex(ctx, ix, old, units, dirty, removed, &res)
	if err != nil {
		return res, err
	}
	err = writeUpdated(path, ix.Version(), old, units, fresh, &res)
	return res, err
}

// openPrior opens the pack at path for an update by the indexer at version. ok is false
// for a pack to rebuild whole: missing, in another format, or written by another indexer
// version.
func openPrior(path, version string) (old *packFile, ok bool, err error) {
	old, err = openPack(path)
	switch {
	case err == nil && old.Indexer() != version:
		return nil, false, old.Close()
	case err == nil:
		return old, true, nil
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, ErrIndexFormat):
		return nil, false, nil
	}
	return nil, false, err
}

// dirtyUnits is the units old lacks or holds at another digest; every unit when old is nil.
func dirtyUnits(old *packFile, units []Unit) []Unit {
	if old == nil {
		return slices.Clone(units)
	}
	var dirty []Unit
	for _, u := range units {
		if e, ok := old.Entry(u.ID); !ok || e.Digest != u.Digest {
			dirty = append(dirty, u)
		}
	}
	return dirty
}

// droppedDefinitions records in res the units old holds that units no longer declares, and
// returns every symbol they defined.
func droppedDefinitions(old *packFile, units []Unit, res *UpdateResult) (map[string]bool, error) {
	removed := map[string]bool{}
	if old == nil {
		return removed, nil
	}
	declared := make(map[string]bool, len(units))
	for _, u := range units {
		declared[u.ID] = true
	}
	for _, e := range old.Entries() {
		if declared[e.Unit] {
			continue
		}
		res.Dropped = append(res.Dropped, e.Unit)
		s, err := old.Shard(e.Unit)
		if err != nil {
			return nil, err
		}
		for _, sym := range s.Defined() {
			removed[sym] = true
		}
	}
	return removed, nil
}

// reindex indexes dirty, then each round's dependents that reference a symbol the round
// removed, until a round removes none; it returns every shard it indexed by unit.
func reindex(ctx context.Context, ix UnitIndexer, old *packFile, units, dirty []Unit, removed map[string]bool, res *UpdateResult) (map[string]Shard, error) {
	fresh := map[string]Shard{}
	for pending := dirty; len(pending) > 0; clear(removed) {
		shards, err := ix.Index(ctx, pending)
		if err != nil {
			return nil, err
		}
		changed := map[string]bool{}
		for _, id := range res.Dropped {
			changed[id] = true
		}
		for _, s := range shards {
			fresh[s.Unit] = s
			if err := noteRemoved(old, s, removed, changed); err != nil {
				return nil, err
			}
		}
		pending, err = dependentsReferencing(old, units, fresh, changed, removed)
		if err != nil {
			return nil, err
		}
		for _, u := range pending {
			res.Dependents = append(res.Dependents, u.ID)
		}
	}
	return fresh, nil
}

// noteRemoved adds to removed each symbol old's shard for s's unit defined and s does not,
// and marks the unit changed when there is one.
func noteRemoved(old *packFile, s Shard, removed, changed map[string]bool) error {
	if old == nil {
		return nil
	}
	prev, err := old.Shard(s.Unit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	now := s.Defined()
	for _, sym := range prev.Defined() {
		if _, found := slices.BinarySearch(now, sym); !found {
			removed[sym] = true
			changed[s.Unit] = true
		}
	}
	return nil
}

// writeUpdated writes units to a new pack at path: each fresh shard encoded, every other
// one copied from old as stored, which res counts as Kept.
func writeUpdated(path, version string, old *packFile, units []Unit, fresh map[string]Shard, res *UpdateResult) error {
	pw, err := newPackWriter(path)
	if err != nil {
		return err
	}
	for _, u := range units {
		var enc []byte
		var gaps []string
		if s, ok := fresh[u.ID]; ok {
			enc, err = encodeShard(s)
			gaps = s.Gaps
		} else {
			e, _ := old.Entry(u.ID)
			enc, err = old.raw(e)
			gaps = e.Gaps
			res.Kept++
		}
		res.Gaps = append(res.Gaps, gaps...)
		if err == nil {
			err = pw.add(u, gaps, enc)
		}
		if err != nil {
			return errors.Join(err, pw.abort())
		}
	}
	return pw.commit(path, version)
}

// dependentsReferencing is the units not yet re-indexed that depend on a changed unit and
// whose stored shard references a removed symbol.
func dependentsReferencing(old *packFile, units []Unit, fresh map[string]Shard, changed, removed map[string]bool) ([]Unit, error) {
	if old == nil || len(removed) == 0 {
		return nil, nil
	}
	var out []Unit
	for _, u := range units {
		if _, done := fresh[u.ID]; done {
			continue
		}
		if !slices.ContainsFunc(u.Deps, func(d string) bool { return changed[d] }) {
			continue
		}
		s, err := old.Shard(u.ID)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if s.References(removed) {
			out = append(out, u)
		}
	}
	return out, nil
}
