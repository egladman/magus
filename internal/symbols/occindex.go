package symbols

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"

	"github.com/scip-code/scip/bindings/go/scip"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// KeyOccurrences is what ParseOccurrences returns for one symbol key: every occurrence,
// grouped by file, and the spellings an occurrence may hold.
type KeyOccurrences struct {
	Files []types.SymbolOccurrenceFile
	Names []string
}

// IndexOccurrences walks a decoded index once and returns, for every symbol key it names,
// exactly what ParseOccurrences returns for that key alone. A key absent from the map has
// no occurrence and no display name in the index, and ParseOccurrences would return nothing
// for it either.
//
// One walk for every key is what lets the result be persisted: ParseOccurrences decodes the
// whole index to answer one key, and a lookup that read a stored answer instead would never
// decode it at all.
func IndexOccurrences(ctx context.Context, idx *scip.Index, projectPath string) (map[string]KeyOccurrences, error) {
	type acc struct {
		ident, display string
		byFile         map[string][]types.SymbolOccurrence
	}
	keys := map[string]*acc{}
	get := func(key string) *acc {
		a := keys[key]
		if a == nil {
			a = &acc{byFile: map[string][]types.SymbolOccurrence{}}
			keys[key] = a
		}
		return a
	}
	for _, doc := range idx.Documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Display names count from every document, including those outside the workspace,
		// and the first one wins, as in ParseOccurrences.
		for _, si := range doc.Symbols {
			if info, ok := parseMoniker(si.Symbol); ok && si.DisplayName != "" {
				if a := get(info.Key); a.display == "" {
					a.display = si.DisplayName
				}
			}
		}
		docPath, ok := workspacePath(projectPath, doc.RelativePath)
		if !ok {
			continue
		}
		for _, occ := range doc.Occurrences {
			if occ.Symbol == "" || scip.IsLocalSymbol(occ.Symbol) {
				continue
			}
			info, ok := parseMoniker(occ.Symbol)
			if !ok {
				continue
			}
			a := get(info.Key)
			if a.ident == "" {
				a.ident = info.Label
			}
			r, ok := occ.SourceRange()
			if !ok || r.Start.Line < 0 || r.Start.Character < 0 {
				continue
			}
			a.byFile[docPath] = append(a.byFile[docPath], types.SymbolOccurrence{
				Line:       int(r.Start.Line) + 1,
				Column:     int(r.Start.Character) + 1,
				EndLine:    int(r.End.Line) + 1,
				EndColumn:  int(r.End.Character) + 1,
				Definition: occ.SymbolRoles&int32(scip.SymbolRole_Definition) != 0,
			})
		}
	}
	out := make(map[string]KeyOccurrences, len(keys))
	for key, a := range keys {
		out[key] = KeyOccurrences{Files: sortedOccurrenceFiles(a.byFile), Names: spellings(a.ident, a.display)}
	}
	return out, nil
}

// An occurrence file holds one IndexOccurrences result so a lookup reads one key's record
// and nothing else:
//
//	magic | stamp length (uint32) | stamp | directory length (uint64) | directory | records
//
// The directory maps each key to its record's offset and length within the records, and a
// record is one key's KeyOccurrences in the compact form below. The stamp is the caller's
// identity for the index the file was built from; a file whose stamp differs is not read.
const occurrenceFileMagic = "magus-occurrences/1\n"

// occurrenceRecord is KeyOccurrences on disk: each site as [line, column, end line, end
// column, definition], which keeps a hot symbol's record a fraction of its JSON size.
type occurrenceRecord struct {
	Files []occurrenceFileRecord `json:"f,omitempty"`
	Names []string               `json:"n,omitempty"`
}

type occurrenceFileRecord struct {
	Path  string   `json:"p"`
	Sites [][5]int `json:"s"`
}

// WriteOccurrenceFile persists occ under stamp at path, replacing it atomically.
func WriteOccurrenceFile(path, stamp string, occ map[string]KeyOccurrences) error {
	var records bytes.Buffer
	dir := make(map[string][2]int64, len(occ))
	for _, key := range slices.Sorted(maps.Keys(occ)) {
		k := occ[key]
		rec := occurrenceRecord{Names: k.Names}
		for _, f := range k.Files {
			fr := occurrenceFileRecord{Path: f.File, Sites: make([][5]int, len(f.Occurrences))}
			for i, o := range f.Occurrences {
				def := 0
				if o.Definition {
					def = 1
				}
				fr.Sites[i] = [5]int{o.Line, o.Column, o.EndLine, o.EndColumn, def}
			}
			rec.Files = append(rec.Files, fr)
		}
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		dir[key] = [2]int64{int64(records.Len()), int64(len(b))}
		records.Write(b)
	}
	dirBytes, err := json.Marshal(dir)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	out.Grow(len(occurrenceFileMagic) + 4 + len(stamp) + 8 + len(dirBytes) + records.Len())
	out.WriteString(occurrenceFileMagic)
	out.Write(binary.BigEndian.AppendUint32(nil, uint32(len(stamp))))
	out.WriteString(stamp)
	out.Write(binary.BigEndian.AppendUint64(nil, uint64(len(dirBytes))))
	out.Write(dirBytes)
	out.Write(records.Bytes())
	return file.WriteFileAtomic(path, out.Bytes(), 0o644)
}

// ErrOccurrenceFileStale reports an occurrence file that is missing, unreadable, or built
// from an index other than the one stamp names; the caller rebuilds it.
var ErrOccurrenceFileStale = errors.New("symbols: occurrence file is not current")

// ReadKeyOccurrences reads key's record from the occurrence file at path. A key the file
// has no record for has no occurrences, and returns the zero KeyOccurrences.
func ReadKeyOccurrences(path, stamp, key string) (KeyOccurrences, error) {
	f, err := os.Open(path)
	if err != nil {
		return KeyOccurrences{}, ErrOccurrenceFileStale
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	head := make([]byte, len(occurrenceFileMagic)+4)
	if _, err := io.ReadFull(r, head); err != nil || string(head[:len(occurrenceFileMagic)]) != occurrenceFileMagic {
		return KeyOccurrences{}, ErrOccurrenceFileStale
	}
	got := make([]byte, binary.BigEndian.Uint32(head[len(occurrenceFileMagic):]))
	if _, err := io.ReadFull(r, got); err != nil || string(got) != stamp {
		return KeyOccurrences{}, ErrOccurrenceFileStale
	}
	var n [8]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return KeyOccurrences{}, ErrOccurrenceFileStale
	}
	dirLen := binary.BigEndian.Uint64(n[:])
	dirBytes := make([]byte, dirLen)
	if _, err := io.ReadFull(r, dirBytes); err != nil {
		return KeyOccurrences{}, ErrOccurrenceFileStale
	}
	var dir map[string][2]int64
	if err := json.Unmarshal(dirBytes, &dir); err != nil {
		return KeyOccurrences{}, ErrOccurrenceFileStale
	}
	at, ok := dir[key]
	if !ok {
		return KeyOccurrences{}, nil
	}
	base := int64(len(occurrenceFileMagic)+4+len(got)+8) + int64(dirLen)
	b := make([]byte, at[1])
	if _, err := f.ReadAt(b, base+at[0]); err != nil {
		return KeyOccurrences{}, fmt.Errorf("symbols: read occurrences of %q: %w", key, err)
	}
	var rec occurrenceRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return KeyOccurrences{}, fmt.Errorf("symbols: decode occurrences of %q: %w", key, err)
	}
	out := KeyOccurrences{Names: rec.Names, Files: make([]types.SymbolOccurrenceFile, 0, len(rec.Files))}
	for _, fr := range rec.Files {
		occs := make([]types.SymbolOccurrence, len(fr.Sites))
		for i, s := range fr.Sites {
			occs[i] = types.SymbolOccurrence{Line: s[0], Column: s[1], EndLine: s[2], EndColumn: s[3], Definition: s[4] == 1}
		}
		out.Files = append(out.Files, types.SymbolOccurrenceFile{File: fr.Path, Occurrences: occs})
	}
	return out, nil
}
