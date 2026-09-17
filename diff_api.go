package magus

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// diffConfig carries what a review compares against beyond the working tree.
type diffConfig struct {
	baseline      *types.KnowledgeGraphOutput
	baselineLabel string
}

// DiffAgainst is [Magus.Diff] compared against base, a `magus graph export --symbols` of the
// revision the change started from: it also fills DiffSymbol.Change and Diff.API. label
// names base for the reader.
//
// base must carry symbol nodes from the same indexers the head was built with: signatures
// are compared as text, so two indexer versions rendering one declaration differently read
// as a changed signature. A base with no symbol nodes, one exported before signatures were
// recorded, or a head with no symbol index is reported in Diff.Notes and leaves API nil.
func (m *Magus) DiffAgainst(ctx context.Context, paths []string, base types.KnowledgeGraphOutput, label string) (types.Diff, error) {
	return m.diff(ctx, paths, diffConfig{baseline: &base, baselineLabel: label})
}

// symbolDef is a symbol node that a file in the workspace defines.
type symbolDef struct {
	node types.KnowledgeNode
	path string
}

// definedSymbols indexes the symbol nodes that carry a definition site, by node ID. A symbol
// seen only through references (a dependency's) has no Source and is not the workspace's to
// add or remove.
func definedSymbols(nodes []types.KnowledgeNode) map[string]symbolDef {
	out := map[string]symbolDef{}
	for _, n := range nodes {
		if n.Kind != types.KindSymbol || n.Source == "" {
			continue
		}
		path := n.Source
		if i := strings.LastIndexByte(path, ':'); i >= 0 {
			path = path[:i]
		}
		out[n.ID] = symbolDef{node: n, path: path}
	}
	return out
}

// hasMembers reports whether any other defined symbol is declared inside id: a type's fields
// and methods, not its parameters. A container's rendered declaration can spell out its
// members (scip-go renders a struct with every field), so its signature moves whenever a
// member does, and each member is classified on its own instead.
func hasMembers(id string, sides ...map[string]symbolDef) bool {
	for _, defs := range sides {
		for other := range defs {
			if len(other) > len(id) && strings.HasPrefix(other, id) && !strings.ContainsRune("([", rune(other[len(id)])) {
				return true
			}
		}
	}
	return false
}

// attachAPIDelta classifies every symbol the changeset's files define on either side against
// base, attaches the public ones to their files, and sets out.API.
func (m *Magus) attachAPIDelta(out *types.Diff, byPath map[string]*types.DiffFile, head *knowledge.Graph, cfg diffConfig) {
	baseOut := *cfg.baseline
	if baseOut.SchemaVersion < 14 {
		out.Notes = append(out.Notes, fmt.Sprintf(
			"API delta skipped: the baseline %s is knowledge schema %d, which predates recorded signatures; export it again with this magus",
			cfg.baselineLabel, baseOut.SchemaVersion))
		return
	}
	baseDefs := definedSymbols(baseOut.Nodes)
	if len(baseDefs) == 0 {
		out.Notes = append(out.Notes, "API delta skipped: the baseline "+cfg.baselineLabel+
			" carries no symbols; export it with `magus graph export --symbols`")
		return
	}
	base := knowledge.NewGraph()
	base.Merge(baseOut.Nodes, baseOut.Links)
	headDefs := definedSymbols(head.Nodes())

	api := types.DiffAPI{Base: cfg.baselineLabel}
	anyChange := false
	unmeasured := 0
	record := func(f *types.DiffFile, graph *knowledge.Graph, def symbolDef, change, sig, baseSig string) {
		anyChange = true
		label := def.node.Label
		exported := exportedFromModule(def.path, label, def.node.ID)
		external, externalFiles := m.externalReferents(graph, def.node.ID, f.Project)
		public := exported || len(external) > 0
		listed := slices.IndexFunc(f.Symbols, func(s types.DiffSymbol) bool { return s.ID == def.node.ID })
		qualified := qualifiedName(def.node.ID, label)
		switch {
		case listed >= 0:
			s := &f.Symbols[listed]
			s.Change, s.Qualified, s.Signature, s.BaseSignature = change, qualified, sig, baseSig
			public = public || s.ModuleAPI || len(s.ExternalProjects) > 0
		case public:
			f.Symbols = append(f.Symbols, types.DiffSymbol{
				ID: def.node.ID, Label: label, ModuleAPI: exported,
				ExternalProjects: external, ExternalFileCount: externalFiles,
				Change: change, Qualified: qualified, Signature: sig, BaseSignature: baseSig,
			})
		}
		if !public {
			return
		}
		f.Surface = types.DiffSurfacePublic
		switch change {
		case types.DiffChangeAdded:
			api.Added++
		case types.DiffChangeRemoved:
			api.Removed++
		case types.DiffChangeSignature:
			api.Signature++
		case types.DiffChangeBody:
			api.Body++
		}
	}

	for _, id := range slices.Sorted(maps.Keys(headDefs)) {
		def := headDefs[id]
		f, ok := byPath[def.path]
		if !ok {
			continue
		}
		sig := def.node.Attrs[knowledge.AttrSignature]
		prior, inBase := baseDefs[id]
		if !inBase {
			record(f, head, def, types.DiffChangeAdded, sig, "")
			continue
		}
		baseSig := prior.node.Attrs[knowledge.AttrSignature]
		if sig != "" && baseSig != "" && sig != baseSig && !hasMembers(id, headDefs, baseDefs) {
			record(f, head, def, types.DiffChangeSignature, sig, baseSig)
			continue
		}
		digest, baseDigest := def.node.Attrs[knowledge.AttrBodyDigest], prior.node.Attrs[knowledge.AttrBodyDigest]
		switch {
		case digest == "" || baseDigest == "":
			unmeasured++
		case digest != baseDigest:
			record(f, head, def, types.DiffChangeBody, sig, baseSig)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(baseDefs)) {
		def := baseDefs[id]
		f, ok := byPath[def.path]
		if !ok {
			continue
		}
		if _, still := headDefs[id]; still {
			continue
		}
		record(f, base, def, types.DiffChangeRemoved, "", def.node.Attrs[knowledge.AttrSignature])
	}

	api.Floor = types.DiffBumpNone
	switch {
	case api.Removed > 0:
		api.Floor = types.DiffBumpMajor
	case api.Added > 0:
		api.Floor = types.DiffBumpMinor
	case anyChange:
		api.Floor = types.DiffBumpPatch
	}
	api.Likely = api.Floor
	if api.Signature > 0 {
		api.Likely = types.DiffBumpMajor
	}
	out.API = &api
	if unmeasured > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf(
			"API delta: %d symbol(s) in changed files had no readable definition lines on one side, so an edit to their bodies is not counted",
			unmeasured))
	}
}

// qualifiedName joins the descriptor names after a SCIP symbol ID's package (or file) with
// dots, `Magus#Diff().` reading as `Magus.Diff`. It falls back to label for an ID with no
// backquoted namespace to read past.
func qualifiedName(id, label string) string {
	i := strings.LastIndex(id, "`/")
	if i < 0 {
		return label
	}
	rest := id[i+2:]
	var names []string
	for rest != "" {
		if rest[0] == '`' {
			j := strings.IndexByte(rest[1:], '`')
			if j < 0 {
				return label
			}
			names = append(names, rest[1:1+j])
			rest = rest[2+j:]
		} else {
			j := strings.IndexAny(rest, "#.:!/([")
			switch {
			case j < 0:
				return label
			case j > 0:
				names = append(names, rest[:j])
			}
			rest = rest[j:]
		}
		if rest == "" {
			break
		}
		if rest[0] == '(' || rest[0] == '[' {
			j := strings.IndexAny(rest, ")]")
			if j < 0 {
				return label
			}
			rest = rest[j+1:]
			if rest == "" {
				break
			}
		}
		rest = rest[1:]
	}
	if len(names) == 0 {
		return label
	}
	return strings.Join(names, ".")
}

// goDescriptorsExported reports whether every named descriptor after the package in a
// scip-go symbol ID is exported, so a method on an unexported type, or any parameter, is not
// module API. ok is false when the ID has no backquoted package descriptor to read past.
func goDescriptorsExported(id string) (exported, ok bool) {
	i := strings.LastIndex(id, "`/")
	if i < 0 {
		return false, false
	}
	rest := id[i+2:]
	if rest == "" {
		return false, true
	}
	for rest != "" {
		if rest[0] == '(' || rest[0] == '[' {
			return false, true
		}
		var name string
		if rest[0] == '`' {
			j := strings.IndexByte(rest[1:], '`')
			if j < 0 {
				return false, false
			}
			name, rest = rest[1:1+j], rest[2+j:]
		} else {
			j := strings.IndexAny(rest, "#.:!/([")
			if j <= 0 {
				return false, false
			}
			name, rest = rest[:j], rest[j:]
		}
		if name[0] < 'A' || name[0] > 'Z' {
			return false, true
		}
		if strings.HasPrefix(rest, "(") {
			j := strings.IndexByte(rest, ')')
			if j < 0 {
				return false, false
			}
			rest = rest[j+1:]
		}
		if rest != "" {
			rest = rest[1:]
		}
	}
	return true, true
}
