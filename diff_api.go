package magus

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// diffConfig carries what a review compares against beyond the working tree.
type diffConfig struct {
	baseline      *types.KnowledgeGraphOutput
	baselineLabel string
	patch         string
	patchGiven    bool
	minCohort     int
	minShare      float64
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
	return m.DiffWith(ctx, paths, types.DiffOptions{Baseline: &base, BaselineLabel: label})
}

// DiffWith is [Magus.Diff] with the options [types.DiffOptions] describes: the changeset's own
// patch, a baseline to compare against, and the conformance checks' silence gates. A review of
// anything but the working tree passes its patch, since without a baseline the patch is what
// says which symbols the change adds; with neither, the working tree's patch is read.
func (m *Magus) DiffWith(ctx context.Context, paths []string, opts types.DiffOptions) (types.Diff, error) {
	return m.diff(ctx, paths, diffConfig{
		baseline: opts.Baseline, baselineLabel: opts.BaselineLabel,
		patch: opts.Patch, patchGiven: opts.Patch != "",
		minCohort: opts.MinCohort, minShare: opts.MinShare,
	})
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
// DiffChange by ID, public or not, and the base's nodes for the symbols the changed files no
// longer define; both are nil when the baseline could not be compared.
func attachAPIDelta(out *types.Diff, byPath map[string]*types.DiffFile, head *knowledge.Graph, cfg diffConfig, externals externalsFunc) (map[string]string, []types.KnowledgeNode) {
	baseOut := *cfg.baseline
	if baseOut.SchemaVersion < 14 {
		out.Notes = append(out.Notes, fmt.Sprintf(
			"API delta skipped: the baseline %s is knowledge schema %d, which predates recorded signatures; export it again with this magus",
			cfg.baselineLabel, baseOut.SchemaVersion))
		return nil, nil
	}
	baseDefs := definedSymbols(baseOut.Nodes)
	if len(baseDefs) == 0 {
		out.Notes = append(out.Notes, "API delta skipped: the baseline "+cfg.baselineLabel+
			" carries no symbols; export it with `magus graph export --symbols`")
		return nil, nil
	}
	var removed []types.KnowledgeNode
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
		removed = append(removed, def.node)
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
	return classified, removed
}

// conformanceInput is what attachConformance reads beyond the head graph.
type conformanceInput struct {
	// changes and removed are attachAPIDelta's answer, nil without a baseline.
	changes map[string]string
	removed []types.KnowledgeNode
	// patch is the changeset's unified diff, which says what is new when there is no baseline.
	patch     string
	generated map[string]bool
	read      func(path string) (string, bool)
	minCohort int
	minShare  float64
}

// attachConformance runs the conformance checks over the symbols the change adds, renames or
// re-signs, and sets Checks on each symbol with a finding, appending it to its file when the
// review did not list it: a new helper has no referents yet, and it is exactly what is worth a
// second look.
//
// With a baseline, attachAPIDelta's classification says what is new, and a removed symbol pairs
// with an added one in the same file as a rename when its rendered signature, renamed, is the
// new one's. Without one the patch says it: a symbol is new when its definition line is an
// added line and no removed line in the patch names it (a moved or re-signed symbol is not new),
// and it was renamed from the name a removed line of its file spells where it now reads its own.
func attachConformance(byPath map[string]*types.DiffFile, head *knowledge.Graph, in conformanceInput) {
	defs := definedSymbols(head.Nodes())
	var subjects []string
	introduced := map[string]bool{}
	renamed := map[string]string{}
	var pf patchFacts
	if in.changes == nil {
		pf = readPatchFacts(in.patch)
	}
	for _, id := range slices.Sorted(maps.Keys(defs)) {
		def := defs[id]
		f, ok := byPath[def.path]
		if !ok || f.Generated() {
			continue
		}
		if in.changes != nil {
			switch in.changes[id] {
			case types.DiffChangeAdded:
				introduced[id] = true
				subjects = append(subjects, id)
			case types.DiffChangeSignature:
				subjects = append(subjects, id)
			}
			continue
		}
		label := def.node.Label
		text, added := pf.added[def.path][sourceLine(def.node.Source)]
		if !added || knowledge.IndexIdentifier(pf.removedText, label) >= 0 {
			continue
		}
		introduced[id] = true
		subjects = append(subjects, id)
		if old := knowledge.RenamedFrom(text, label, pf.removed[def.path]); old != "" {
			renamed[id] = old
		}
	}
	if in.changes != nil {
		pairRenames(defs, in.changes, in.removed, renamed)
	}
	found := head.Conformance(knowledge.ConformanceChange{
		Subjects: subjects, Introduced: introduced, Generated: in.generated, Renamed: renamed,
		Read: in.read, MinCohort: in.minCohort, MinShare: in.minShare,
	})
	for _, id := range slices.Sorted(maps.Keys(found)) {
		def, ok := defs[id]
		f := byPath[def.path]
		if !ok || f == nil {
			continue
		}
		i := slices.IndexFunc(f.Symbols, func(s types.DiffSymbol) bool { return s.ID == id })
		if i < 0 {
			change := in.changes[id]
			if in.changes == nil && introduced[id] {
				change = types.DiffChangeAdded
			}
			f.Symbols = append(f.Symbols, types.DiffSymbol{
				ID: id, Label: def.node.Label, Change: change, Qualified: qualifiedName(id, def.node.Label),
				Signature: def.node.Attrs[knowledge.AttrSignature],
			})
			i = len(f.Symbols) - 1
		}
		f.Symbols[i].Checks = found[id]
	}
}

// pairRenames records, for each symbol a baseline classified as removed, the added symbol in the
// same file that is it renamed: its ID and rendered signature with the old name swapped for the
// new one. An ambiguous pairing is left out rather than guessed.
func pairRenames(defs map[string]symbolDef, changes map[string]string, removed []types.KnowledgeNode, renamed map[string]string) {
	for _, r := range removed {
		i := strings.LastIndex(r.ID, r.Label)
		if i < 0 {
			continue
		}
		var match string
		for id, def := range defs {
			if changes[id] != types.DiffChangeAdded || id != r.ID[:i]+def.node.Label+r.ID[i+len(r.Label):] {
				continue
			}
			sig, baseSig := def.node.Attrs[knowledge.AttrSignature], r.Attrs[knowledge.AttrSignature]
			if strings.ReplaceAll(baseSig, r.Label, def.node.Label) != sig {
				continue
			}
			if match != "" {
				match = ""
				break
			}
			match = id
		}
		if match != "" {
			renamed[match] = r.Label
		}
	}
}

// patchFacts is what a unified diff says about which lines are new.
type patchFacts struct {
	// added maps a file to its added lines, by new-side line number.
	added map[string]map[int]string
	// removed holds each file's removed lines; removedText is every removed line of the patch.
	removed     map[string][]string
	removedText string
}

func readPatchFacts(patch string) patchFacts {
	pf := patchFacts{added: map[string]map[int]string{}, removed: map[string][]string{}}
	var all strings.Builder
	for _, f := range changeset.Parse(patch) {
		for _, h := range f.Hunks {
			for _, r := range h.Rows {
				switch {
				case r.Kind == changeset.KindAdd && r.NewLine != nil:
					if pf.added[f.Path] == nil {
						pf.added[f.Path] = map[int]string{}
					}
					pf.added[f.Path][*r.NewLine] = r.Text
				case r.Kind == changeset.KindDel:
					pf.removed[f.Path] = append(pf.removed[f.Path], r.Text)
					all.WriteString(r.Text)
					all.WriteByte('\n')
				}
			}
		}
	}
	pf.removedText = all.String()
	return pf
}

// sourceLine is the line of a "<path>:<line>" source, or 0.
func sourceLine(source string) int {
	i := strings.LastIndexByte(source, ':')
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(source[i+1:])
	if err != nil {
		return 0
	}
	return n
}

// generatedFiles classifies every file the head graph defines a symbol in, once, and returns
// the ones that are declared target outputs.
func (m *Magus) generatedFiles(ctx context.Context, head *knowledge.Graph) (map[string]bool, error) {
	defs := definedSymbols(head.Nodes())
	paths := make([]string, 0, len(defs))
	for _, def := range defs {
		paths = append(paths, def.path)
	}
	slices.Sort(paths)
	entries, err := m.ClassifyFiles(ctx, slices.Compact(paths))
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range entries {
		if e.Role == types.DiffRoleOutput {
			out[e.Path] = true
		}
	}
	return out, nil
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
