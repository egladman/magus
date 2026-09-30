package buzz

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
)

// BytecodeStore keeps a compiled chunk for source a session has already
// compiled. The blob's tail is the .bo Chunk.Marshal writes; the prefix is
// the files that chunk was compiled against, so a later process can tell
// that those files are unchanged before it runs the chunk.
//
// Load returns an error wrapping fs.ErrNotExist when key is absent. The
// session treats any Load error as "compile this source".
type BytecodeStore interface {
	Load(key string) ([]byte, error)
	Store(key string, blob []byte) error
}

// DiskBytecodeStore reads and writes one file per key under a directory. A
// missing file is an absent key. Store writes a temporary file and renames it
// into place, so a reader never observes a partial blob.
//
// Nothing in a blob is authenticated: whoever can write the directory chooses
// the code a session runs. Keep it where only the session's own user writes.
type DiskBytecodeStore struct{ dir string }

// NewDiskBytecodeStore returns a store under dir. dir is created on the first Store.
func NewDiskBytecodeStore(dir string) *DiskBytecodeStore { return &DiskBytecodeStore{dir: dir} }

func (d *DiskBytecodeStore) Load(key string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.dir, key))
}

func (d *DiskBytecodeStore) Store(key string, blob []byte) error {
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(d.dir, ".partial-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(blob)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp)
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Rename(tmp, filepath.Join(d.dir, key)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// bytecodeFile is one import a compiled chunk depends on: the spelling it was
// resolved from, the directories searched ahead of the search path when it was
// resolved, and the file it found. An empty path records that no file matched.
//
// via is relative to the chunk being recorded: it is appended to the directory
// stack the chunk is loaded under. An isolated file was resolved in a
// sub-session that inherits no stack, so via is the whole stack.
type bytecodeFile struct {
	importPath string
	via        []string
	isolated   bool
	path       string
	sum        [32]byte
}

// bytecodeImport is one import statement of a compiled chunk, plus the files
// that import loaded. files is empty for a native module.
type bytecodeImport struct {
	path  string
	alias string
	only  []string
	files []bytecodeFile
}

// bytecodeFrame is what one compile in progress depends on: the imports it
// resolved, and the files its entry filter read to decide what to keep.
type bytecodeFrame struct {
	imports     []bytecodeImport
	filterReads []bytecodeFile
}

// importClosure is the files notes loaded, for the importer to record under
// its own import. A change to any of them means the importer's chunk, which
// was type-checked against those files, is stale even when the direct
// import's own source is unchanged.
func importClosure(notes []bytecodeImport) []bytecodeFile {
	var out []bytecodeFile
	for _, n := range notes {
		out = append(out, n.files...)
	}
	return out
}

// nestedClosure re-bases a module's closure onto its importer: the module's
// own directory was on the stack when those files were resolved.
func nestedClosure(files []bytecodeFile, moduleDir string) []bytecodeFile {
	out := make([]bytecodeFile, len(files))
	for i, f := range files {
		if !f.isolated {
			f.via = append([]string{moduleDir}, f.via...)
		}
		out[i] = f
	}
	return out
}

// isolatedClosure marks files resolved in a sub-session that did not inherit
// the importer's directory stack.
func isolatedClosure(files []bytecodeFile) []bytecodeFile {
	out := make([]bytecodeFile, len(files))
	for i, f := range files {
		f.isolated = true
		out[i] = f
	}
	return out
}

const bytecodeRecordVersion uint16 = 2

var bytecodeRecordMagic = [4]byte{'B', 'Z', 'G', 'R'}

// bytecodeKey names the chunk code compiles to under this session's compile
// flags and entry filter. A filter drops statements before compile, so its
// chunk is not the one a full compile of the same source produces, and the .bo
// version alone cannot tell them apart.
func (s *Session) bytecodeKey(code string) string {
	h := sha256.New()
	var ver [2]byte
	binary.LittleEndian.PutUint16(ver[:], vmpackage.BytecodeVersion)
	h.Write(ver[:])
	h.Write([]byte{s.bytecodeFlags()})
	writeKeyString(h, s.entryFilterID)
	writeKeyString(h, s.sourceFile)
	h.Write([]byte(code))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Session) bytecodeFlags() byte {
	var f byte
	if s.promoteTopLevel {
		f |= 1
	}
	if s.embedded {
		f |= 2
	}
	return f
}

func writeKeyString(w io.Writer, v string) {
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(v)))
	_, _ = w.Write(n[:])
	_, _ = w.Write([]byte(v))
}

// loadCachedChunk returns the chunk saved for code when every file that chunk
// depends on still resolves to the bytes it had at compile time, and the
// imports it replayed. hit false with a nil error means compile code; the
// session has not bound anything yet. An error means a replay already bound
// imports and then failed, and compiling again in that environment would
// define those names twice.
func (s *Session) loadCachedChunk(ctx context.Context, code string) (*vmpackage.Chunk, []bytecodeImport, bool, error) {
	if s.lineCover != nil || s.repl {
		return nil, nil, false, nil
	}
	blob, err := s.bytecodeStore.Load(s.bytecodeKey(code))
	if err != nil {
		return nil, nil, false, nil
	}
	rec, err := decodeBytecodeRecord(blob)
	if err != nil {
		return nil, nil, false, nil
	}
	chunk, err := rec.chunk(s.sourceFile)
	if err != nil {
		return nil, nil, false, nil
	}
	if !s.closureFresh(importClosure(rec.imports)) || !s.closureFresh(rec.filterReads) {
		return nil, nil, false, nil
	}
	prev := s.replaying
	s.replaying = true
	defer func() { s.replaying = prev }()
	for _, n := range rec.imports {
		imp := &ast.ImportStmt{Path: n.path, Alias: n.alias, Only: n.only}
		if _, err := s.resolveImport(ctx, imp); err != nil {
			return nil, nil, false, err
		}
	}
	return chunk, rec.imports, true, nil
}

// closureFresh reports whether each file still resolves to the path it was
// compiled against and that path still holds the same bytes.
func (s *Session) closureFresh(files []bytecodeFile) bool {
	for _, f := range files {
		got := s.resolveRecorded(f)
		if got == "" || f.path == "" {
			if got != f.path {
				return false
			}
			continue
		}
		if !sameFile(got, f.path) {
			return false
		}
		data, err := s.readImportSource(f.path)
		if err != nil || sha256.Sum256(data) != f.sum {
			return false
		}
	}
	return true
}

// resolveRecorded repeats the file search that found f, under the directory
// stack it was found under.
func (s *Session) resolveRecorded(f bytecodeFile) string {
	saved := s.importingDirs
	if f.isolated {
		s.importingDirs = slices.Clone(f.via)
	} else {
		s.importingDirs = append(slices.Clip(saved), f.via...)
	}
	defer func() { s.importingDirs = saved }()
	return s.findIncludeFile(f.importPath)
}

func sameFile(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return aa == bb
}

// saveCachedChunk writes the chunk and what it was compiled against. A marshal
// or store failure leaves the load that just ran alone: the next exec compiles
// again.
func (s *Session) saveCachedChunk(code string, frame bytecodeFrame, chunk *vmpackage.Chunk) {
	if s.lineCover != nil || s.repl {
		return
	}
	bo, err := chunk.Marshal()
	if err != nil {
		return
	}
	debug, err := chunk.Marshal(vmpackage.DebugOnly())
	if err != nil {
		return
	}
	rec := bytecodeRecord{
		imports:     frame.imports,
		filterReads: frame.filterReads,
		private:     chunk.Private,
		docs:        chunkDocs(chunk, nil),
		debug:       debug,
		bo:          bo,
	}
	_ = s.bytecodeStore.Store(s.bytecodeKey(code), rec.encode())
}

// bytecodeRecord is one stored chunk. The .bo carries what runs; the rest is
// what a compile leaves on the chunk that the .bo format does not: the names
// it keeps private from an importer's checker, doc comments, and source lines.
type bytecodeRecord struct {
	imports     []bytecodeImport
	filterReads []bytecodeFile
	private     []string
	docs        []string
	debug       []byte
	bo          []byte
}

// chunk rebuilds the chunk the record was saved from. sourceFile is part of
// the record's key, so stamping it restores what the compile set.
func (r bytecodeRecord) chunk(sourceFile string) (*vmpackage.Chunk, error) {
	chunk, err := vmpackage.UnmarshalChunk(r.bo)
	if err != nil {
		return nil, err
	}
	if err := chunk.AttachDebug(r.debug); err != nil {
		return nil, err
	}
	chunk.Private = r.private
	rest, ok := restoreChunkTree(chunk, r.docs, sourceFile)
	if !ok || len(rest) != 0 {
		return nil, errors.New("buzz: bytecode record: doc table does not match the chunk")
	}
	return chunk, nil
}

func chunkDocs(c *vmpackage.Chunk, out []string) []string {
	out = append(out, c.Doc)
	for _, f := range c.Funs {
		out = chunkDocs(f, out)
	}
	return out
}

func restoreChunkTree(c *vmpackage.Chunk, docs []string, sourceFile string) ([]string, bool) {
	if len(docs) == 0 {
		return nil, false
	}
	c.Doc = docs[0]
	c.SourceFile = sourceFile
	docs = docs[1:]
	for _, f := range c.Funs {
		var ok bool
		if docs, ok = restoreChunkTree(f, docs, sourceFile); !ok {
			return nil, false
		}
	}
	return docs, true
}

func (r bytecodeRecord) encode() []byte {
	buf := make([]byte, 0, 64+len(r.debug)+len(r.bo))
	buf = append(buf, bytecodeRecordMagic[:]...)
	buf = binary.LittleEndian.AppendUint16(buf, bytecodeRecordVersion)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(r.imports)))
	for _, n := range r.imports {
		buf = appendString(buf, n.path)
		buf = appendString(buf, n.alias)
		buf = appendStrings(buf, n.only)
		buf = appendFiles(buf, n.files)
	}
	buf = appendFiles(buf, r.filterReads)
	buf = appendStrings(buf, r.private)
	buf = appendStrings(buf, r.docs)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(r.debug)))
	buf = append(buf, r.debug...)
	return append(buf, r.bo...)
}

func appendString(buf []byte, v string) []byte {
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(v)))
	return append(buf, v...)
}

func appendStrings(buf []byte, vs []string) []byte {
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(vs)))
	for _, v := range vs {
		buf = appendString(buf, v)
	}
	return buf
}

func appendFiles(buf []byte, files []bytecodeFile) []byte {
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(files)))
	for _, f := range files {
		var flags byte
		if f.isolated {
			flags = 1
		}
		buf = append(buf, flags)
		buf = appendString(buf, f.importPath)
		buf = appendStrings(buf, f.via)
		buf = appendString(buf, f.path)
		buf = append(buf, f.sum[:]...)
	}
	return buf
}

// Encoded sizes with every string empty. A count larger than the bytes left
// could hold is corrupt, and is rejected before anything is allocated for it.
const (
	minStringSize = 4
	minImportSize = 4 * minStringSize
	minFileSize   = 1 + 3*minStringSize + sha256.Size
)

var errTruncatedRecord = errors.New("buzz: bytecode record: truncated")

func decodeBytecodeRecord(blob []byte) (bytecodeRecord, error) {
	var rec bytecodeRecord
	if len(blob) < 4+2 || [4]byte(blob[:4]) != bytecodeRecordMagic {
		return rec, errors.New("buzz: bytecode record: bad magic")
	}
	if binary.LittleEndian.Uint16(blob[4:6]) != bytecodeRecordVersion {
		return rec, errors.New("buzz: bytecode record: bad version")
	}
	n, rest, err := takeCount(blob[6:], minImportSize)
	if err != nil {
		return rec, err
	}
	rec.imports = make([]bytecodeImport, 0, n)
	for range n {
		var note bytecodeImport
		if note.path, rest, err = takeString(rest); err != nil {
			return rec, err
		}
		if note.alias, rest, err = takeString(rest); err != nil {
			return rec, err
		}
		if note.only, rest, err = takeStrings(rest); err != nil {
			return rec, err
		}
		if note.files, rest, err = takeFiles(rest); err != nil {
			return rec, err
		}
		rec.imports = append(rec.imports, note)
	}
	if rec.filterReads, rest, err = takeFiles(rest); err != nil {
		return rec, err
	}
	if rec.private, rest, err = takeStrings(rest); err != nil {
		return rec, err
	}
	if rec.docs, rest, err = takeStrings(rest); err != nil {
		return rec, err
	}
	var debugLen uint32
	if debugLen, rest, err = takeU32(rest); err != nil {
		return rec, err
	}
	if uint32(len(rest)) < debugLen {
		return rec, errTruncatedRecord
	}
	rec.debug, rest = rest[:debugLen], rest[debugLen:]
	if len(rest) == 0 {
		return rec, errors.New("buzz: bytecode record: missing chunk")
	}
	rec.bo = rest
	return rec, nil
}

func takeU32(b []byte) (uint32, []byte, error) {
	if len(b) < 4 {
		return 0, nil, errTruncatedRecord
	}
	return binary.LittleEndian.Uint32(b[:4]), b[4:], nil
}

// takeCount reads a count of items each at least minSize bytes long.
func takeCount(b []byte, minSize int) (uint32, []byte, error) {
	n, rest, err := takeU32(b)
	if err != nil {
		return 0, nil, err
	}
	if uint64(n)*uint64(minSize) > uint64(len(rest)) {
		return 0, nil, errTruncatedRecord
	}
	return n, rest, nil
}

func takeString(b []byte) (string, []byte, error) {
	n, rest, err := takeU32(b)
	if err != nil {
		return "", nil, err
	}
	if uint32(len(rest)) < n {
		return "", nil, errTruncatedRecord
	}
	return string(rest[:n]), rest[n:], nil
}

func takeStrings(b []byte) ([]string, []byte, error) {
	n, rest, err := takeCount(b, minStringSize)
	if err != nil || n == 0 {
		return nil, rest, err
	}
	out := make([]string, 0, n)
	for range n {
		var v string
		if v, rest, err = takeString(rest); err != nil {
			return nil, nil, err
		}
		out = append(out, v)
	}
	return out, rest, nil
}

func takeFiles(b []byte) ([]bytecodeFile, []byte, error) {
	n, rest, err := takeCount(b, minFileSize)
	if err != nil || n == 0 {
		return nil, rest, err
	}
	out := make([]bytecodeFile, 0, n)
	for range n {
		var f bytecodeFile
		if len(rest) < 1 {
			return nil, nil, errTruncatedRecord
		}
		f.isolated = rest[0]&1 != 0
		rest = rest[1:]
		if f.importPath, rest, err = takeString(rest); err != nil {
			return nil, nil, err
		}
		if f.via, rest, err = takeStrings(rest); err != nil {
			return nil, nil, err
		}
		if f.path, rest, err = takeString(rest); err != nil {
			return nil, nil, err
		}
		if len(rest) < sha256.Size {
			return nil, nil, errTruncatedRecord
		}
		copy(f.sum[:], rest[:sha256.Size])
		rest = rest[sha256.Size:]
		out = append(out, f)
	}
	return out, rest, nil
}
