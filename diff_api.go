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

// externalsFunc answers which other projects reference a symbol, and in how many files:
// [Magus.externalReferents] in a review, a stub in a test.
type externalsFunc func(g *knowledge.Graph, symbolID, owner string) ([]string, int)

// attachAPIDelta classifies every symbol the changeset's files define on either side against
// base, attaches the public ones to their files, and sets out.API. It returns every symbol's
// DiffChange by ID, public or not, or nil when the baseline could not be compared.
func attachAPIDelta(out *types.Diff, byPath map[string]*types.DiffFile, head *knowledge.Graph, cfg diffConfig, externals externalsFunc) map[string]string {
	baseOut := *cfg.baseline
	if baseOut.SchemaVersion < 14 {
		out.Notes = append(out.Notes, fmt.Sprintf(
			"API delta skipped: the baseline %s is knowledge schema %d, which predates recorded signatures; export it again with this magus",
			cfg.baselineLabel, baseOut.SchemaVersion))
		return nil
	}
	baseDefs := definedSymbols(baseOut.Nodes)
	if len(baseDefs) == 0 {
		out.Notes = append(out.Notes, "API delta skipped: the baseline "+cfg.baselineLabel+
			" carries no symbols; export it with `magus graph export --symbols`")
		return nil
	}
	base := knowledge.NewGraph()
	base.Merge(baseOut.Nodes, baseOut.Links)
	headDefs := definedSymbols(head.Nodes())

	api := types.DiffAPI{Base: cfg.baselineLabel}
	anyChange := false
	unmeasured := 0
	classified := map[string]string{}
	record := func(f *types.DiffFile, graph *knowledge.Graph, def symbolDef, change, sig, baseSig string) {
		anyChange = true
		classified[def.node.ID] = change
		label := def.node.Label
		exported := exportedFromModule(def.path, label, def.node.ID)
		external, externalFiles := externals(graph, def.node.ID, f.Project)
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
	return classified
}

// attachNaming sets Naming on each symbol the change adds or re-signs, appending the symbol to
// its file when the review did not list it: a new helper has no referents yet, and its name is
// exactly what is worth a second look.
//
// changes is attachAPIDelta's classification. Without one, a symbol counts as added when the
// committed copy of its file never mentions its name, and a re-signed symbol goes undetected.
// A range review whose head is already committed therefore finds nothing new: silence, never a
// finding about an established name.
func attachNaming(byPath map[string]*types.DiffFile, head *knowledge.Graph, changes map[string]string,
	committed func(path string) (string, bool), generated func(path string) bool,
) {
	defs := definedSymbols(head.Nodes())
	type blob struct {
		text string
		ok   bool
	}
	blobs := map[string]blob{}
	var subjects []string
	introduced := map[string]bool{}
	for _, id := range slices.Sorted(maps.Keys(defs)) {
		def := defs[id]
		f, ok := byPath[def.path]
		if !ok || f.Generated() {
			continue
		}
		change := changes[id]
		if changes == nil {
			b, seen := blobs[def.path]
			if !seen {
				b.text, b.ok = committed(def.path)
				blobs[def.path] = b
			}
			if !b.ok || !mentions(b.text, def.node.Label) {
				change = types.DiffChangeAdded
			}
		}
		switch change {
		case types.DiffChangeAdded:
			introduced[id] = true
			subjects = append(subjects, id)
		case types.DiffChangeSignature:
			subjects = append(subjects, id)
		}
	}
	found := head.Naming(knowledge.NamingChange{
		Subjects:   subjects,
		Introduced: func(id string) bool { return introduced[id] },
		Generated:  generated,
	})
	for _, id := range slices.Sorted(maps.Keys(found)) {
		def := defs[id]
		f := byPath[def.path]
		i := slices.IndexFunc(f.Symbols, func(s types.DiffSymbol) bool { return s.ID == id })
		if i < 0 {
			f.Symbols = append(f.Symbols, types.DiffSymbol{
				ID: id, Label: def.node.Label, Change: changes[id], Qualified: qualifiedName(id, def.node.Label),
				Signature: def.node.Attrs[knowledge.AttrSignature],
			})
			i = len(f.Symbols) - 1
		}
		f.Symbols[i].Naming = found[id]
	}
}

// declaredOutput returns a memoized test for whether a path is a declared target output, the
// language-neutral mark of generated code.
func (m *Magus) declaredOutput(ctx context.Context) func(path string) bool {
	memo := map[string]bool{}
	return func(path string) bool {
		if v, ok := memo[path]; ok {
			return v
		}
		entries, err := m.ClassifyFiles(ctx, []string{path})
		v := err == nil && len(entries) == 1 && entries[0].Role == types.DiffRoleOutput
		memo[path] = v
		return v
	}
}

// mentions reports whether text holds name as a whole identifier.
func mentions(text, name string) bool {
	if name == "" {
		return false
	}
	identByte := func(b byte) bool {
		return b == '_' || b == '$' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	for from := 0; ; {
		i := strings.Index(text[from:], name)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(name)
		if (start == 0 || !identByte(text[start-1])) && (end == len(text) || !identByte(text[end])) {
			return true
		}
		from = start + 1
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
