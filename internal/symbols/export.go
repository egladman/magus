package symbols

import (
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/file"
)

// PackIndex assembles shards into one in-memory SCIP index, adding the implementation
// relationships their method sets imply. Nothing is protobuf-decoded: the result feeds
// ParseDecoded and IndexOccurrences directly, and ExportSCIP encodes it.
func PackIndex(shards []Shard) *scip.Index {
	idx := &scip.Index{Metadata: &scip.Metadata{
		ToolInfo:             &scip.ToolInfo{Name: "magus"},
		TextDocumentEncoding: scip.TextEncoding_UTF8,
	}}
	infos := map[string]*scip.SymbolInformation{}
	for _, s := range shards {
		for _, d := range s.Documents {
			doc := &scip.Document{
				RelativePath:     d.Path,
				Language:         d.Language,
				PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
				Occurrences:      make([]*scip.Occurrence, 0, len(d.Occurrences)),
				Symbols:          make([]*scip.SymbolInformation, 0, len(d.Symbols)),
			}
			for _, o := range d.Occurrences {
				doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{Symbol: o.Symbol, Range: o.Range, SymbolRoles: o.Roles, EnclosingRange: o.Enclosing})
			}
			for _, si := range d.Symbols {
				info := &scip.SymbolInformation{
					Symbol:        si.Symbol,
					DisplayName:   si.DisplayName,
					Kind:          scip.SymbolInformation_Kind(si.Kind),
					Documentation: si.Documentation,
				}
				if si.Signature != "" {
					info.SignatureDocumentation = &scip.Signature{Text: si.Signature, Language: d.Language}
				}
				for _, r := range si.Relationships {
					info.Relationships = append(info.Relationships, &scip.Relationship{
						Symbol: r.Symbol, IsImplementation: r.Implementation, IsReference: r.Reference,
						IsTypeDefinition: r.TypeDefinition, IsDefinition: r.Definition,
					})
				}
				doc.Symbols = append(doc.Symbols, info)
				if _, seen := infos[si.Symbol]; !seen {
					infos[si.Symbol] = info
				}
			}
			idx.Documents = append(idx.Documents, doc)
		}
	}
	for _, rel := range implementations(shards) {
		if info := infos[rel.from]; info != nil {
			info.Relationships = append(info.Relationships, &scip.Relationship{Symbol: rel.to, IsImplementation: true})
		}
	}
	return idx
}

type implementation struct{ from, to string }

// implementations is every concrete type whose method set covers an interface's, and each
// of its own methods that satisfies one of that interface's, across all shards. An
// interface with no methods relates to nothing: everything satisfies it.
func implementations(shards []Shard) []implementation {
	var ifaces []MethodSet
	byMethod := map[Method][]int{}
	for _, s := range shards {
		for _, ms := range s.MethodSets {
			if !ms.Interface || len(ms.Methods) == 0 {
				continue
			}
			for _, m := range ms.Methods {
				k := Method{Name: m.Name, Signature: m.Signature}
				byMethod[k] = append(byMethod[k], len(ifaces))
			}
			ifaces = append(ifaces, ms)
		}
	}
	var out []implementation
	matched := make([]int, len(ifaces))
	var touched []int
	for _, s := range shards {
		for _, ms := range s.MethodSets {
			if ms.Interface {
				continue
			}
			for _, m := range ms.Methods {
				for _, i := range byMethod[Method{Name: m.Name, Signature: m.Signature}] {
					if matched[i] == 0 {
						touched = append(touched, i)
					}
					matched[i]++
				}
			}
			slices.Sort(touched)
			for _, i := range touched {
				if matched[i] == len(ifaces[i].Methods) {
					out = append(out, implementation{ms.Symbol, ifaces[i].Symbol})
					out = append(out, methodImplementations(ms, ifaces[i])...)
				}
				matched[i] = 0
			}
			touched = touched[:0]
		}
	}
	return out
}

func methodImplementations(concrete, iface MethodSet) []implementation {
	var out []implementation
	for _, m := range concrete.Methods {
		if m.Symbol == "" || !strings.HasPrefix(m.Symbol, concrete.Symbol) {
			continue
		}
		for _, im := range iface.Methods {
			if im.Name == m.Name && im.Signature == m.Signature && im.Symbol != "" {
				out = append(out, implementation{m.Symbol, im.Symbol})
			}
		}
	}
	return out
}

// ReadIndex reads the index at path whole: a pack, assembled from every shard, or SCIP
// protobuf when pack is false. A file that does not decode is an ErrIndexFormat error.
func ReadIndex(path string, pack bool) (*scip.Index, error) {
	if !pack {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		idx, err := DecodeIndex(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w: %w", path, ErrIndexFormat, err)
		}
		return idx, nil
	}
	p, err := OpenPack(path)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	shards, err := p.Shards()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", path, ErrIndexFormat, err)
	}
	return PackIndex(shards), nil
}

// ExportSCIP writes the pack at packPath to dest as a SCIP protobuf index, the format
// other code-intelligence tools read. The pack stays magus's storage; this is the
// interchange copy.
func ExportSCIP(packPath, dest string) error {
	p, err := OpenPack(packPath)
	if err != nil {
		return err
	}
	defer p.Close()
	shards, err := p.Shards()
	if err != nil {
		return err
	}
	data, err := proto.Marshal(PackIndex(shards))
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(dest, data, 0o644)
}

// SCIPShards splits a SCIP index an external indexer wrote into one shard per directory,
// so its documents are stored and read like any other pack.
func SCIPShards(idx *scip.Index) ([]Unit, []Shard) {
	byDir := map[string]*Shard{}
	for _, doc := range idx.Documents {
		dir := path.Dir(doc.RelativePath)
		s := byDir[dir]
		if s == nil {
			s = &Shard{Unit: dir}
			byDir[dir] = s
		}
		d := Document{Path: doc.RelativePath, Language: doc.Language}
		for _, o := range doc.Occurrences {
			occ := Occurrence{Symbol: o.Symbol, Roles: o.SymbolRoles}
			if r, ok := o.SourceRange(); ok {
				occ.Range = r.SCIPRange()
			}
			if r, ok := o.EnclosingSourceRange(); ok {
				occ.Enclosing = r.SCIPRange()
			}
			d.Occurrences = append(d.Occurrences, occ)
		}
		for _, si := range doc.Symbols {
			info := SymbolInfo{
				Symbol: si.Symbol, DisplayName: si.DisplayName, Kind: int32(si.Kind),
				Documentation: si.Documentation, Signature: si.GetSignatureDocumentation().GetText(),
			}
			for _, r := range si.Relationships {
				info.Relationships = append(info.Relationships, Relationship{
					Symbol: r.Symbol, Implementation: r.IsImplementation, Reference: r.IsReference,
					TypeDefinition: r.IsTypeDefinition, Definition: r.IsDefinition,
				})
			}
			d.Symbols = append(d.Symbols, info)
		}
		s.Documents = append(s.Documents, d)
	}
	dirs := sortedKeys(byDir)
	units := make([]Unit, 0, len(dirs))
	shards := make([]Shard, 0, len(dirs))
	for _, dir := range dirs {
		units = append(units, Unit{ID: dir})
		shards = append(shards, *byDir[dir])
	}
	return units, shards
}

// PackFromSCIP writes the SCIP index at src as a pack at dest, recorded as indexer's.
func PackFromSCIP(src, dest, indexer string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	idx, err := DecodeIndex(data)
	if err != nil {
		return fmt.Errorf("%s: %w: %w", src, ErrIndexFormat, err)
	}
	return PackSCIP(dest, indexer, idx)
}

// PackSCIP writes a decoded SCIP index as a pack at dest, one shard per directory.
func PackSCIP(dest, indexer string, idx *scip.Index) error {
	units, shards := SCIPShards(idx)
	return WritePack(dest, indexer, units, shards)
}
