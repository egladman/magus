package magus

import (
	"maps"
	"slices"
	"strconv"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// changeClasses sorts the symbols a change defines. An added or re-signed
// symbol is a subject; one whose body changed is touched. change names each
// one's DiffChange.
type changeClasses struct {
	subjects []string
	touched  []string
	renamed  map[string]string
	change   map[string]string
}

// introduced is the symbols the change adds.
func (c changeClasses) introduced() map[string]bool {
	out := map[string]bool{}
	for id, change := range c.change {
		if change == types.DiffChangeAdded {
			out[id] = true
		}
	}
	return out
}

// classifyChanges is the classification attachConformance and
// touchedFunctions share. With a baseline it reads in.changes; without one it
// reads the patch, where only an added or renamed symbol shows.
func classifyChanges(defs map[string]symbolDef, byPath map[string]*types.DiffFile, in conformanceInput) changeClasses {
	c := changeClasses{renamed: map[string]string{}, change: map[string]string{}}
	pf := baselineOrPatchFacts(in)
	for _, id := range slices.Sorted(maps.Keys(defs)) {
		if f, ok := byPath[defs[id].path]; ok && !f.Generated() {
			c.classify(id, defs[id], in, pf)
		}
	}
	if in.changes != nil {
		pairRenames(defs, in.changes, in.removed, c.renamed)
	}
	return c
}

// baselineOrPatchFacts reads the patch only when there is no baseline to
// classify against.
func baselineOrPatchFacts(in conformanceInput) patchFacts {
	if in.changes != nil {
		return patchFacts{}
	}
	return readPatchFacts(in.patch)
}

func (c *changeClasses) classify(id string, def symbolDef, in conformanceInput, pf patchFacts) {
	if in.changes != nil {
		c.fromBaseline(id, in.changes[id])
		return
	}
	c.fromPatch(id, def, pf)
}

func (c *changeClasses) fromBaseline(id, change string) {
	switch change {
	case types.DiffChangeAdded:
		c.subjects = append(c.subjects, id)
	case types.DiffChangeSignature:
		c.subjects = append(c.subjects, id)
	case types.DiffChangeBody:
		c.touched = append(c.touched, id)
	default:
		return
	}
	c.change[id] = change
}

// fromPatch calls a symbol new when its definition line is an added line.
// A removed line naming it means it moved or was re-signed instead.
func (c *changeClasses) fromPatch(id string, def symbolDef, pf patchFacts) {
	label := def.node.Label
	text, added := pf.added[def.path][sourceLine(def.node.Source)]
	if !added || knowledge.IndexIdentifier(pf.removedText, label) >= 0 {
		return
	}
	c.subjects = append(c.subjects, id)
	c.change[id] = types.DiffChangeAdded
	if old := knowledge.PreviousName(text, label, pf.removed[def.path]); old != "" {
		c.renamed[id] = old
	}
}

// touchedFunction is a function a change adds, re-signs or edits the body of.
// EndLine is 0 when the indexer emitted no extent.
type touchedFunction struct {
	Symbol   string
	Label    string
	Path     string
	Language string
	Digest   string
	Change   string
	Line     int
	EndLine  int
}

// functionKinds are the SCIP kinds with a body CRAP can score.
var functionKinds = map[string]bool{"Function": true, "Method": true, "Constructor": true}

// touchedFunctions names the functions the change touches, in symbol ID
// order. Tests and generated files are never touched.
func touchedFunctions(defs map[string]symbolDef, byPath map[string]*types.DiffFile, in conformanceInput) []touchedFunction {
	c := classifyChanges(defs, byPath, in)
	var out []touchedFunction
	for _, id := range slices.Sorted(maps.Keys(c.change)) {
		if fn, ok := asTouchedFunction(id, defs[id], c.change[id]); ok {
			out = append(out, fn)
		}
	}
	return out
}

func asTouchedFunction(id string, def symbolDef, change string) (touchedFunction, bool) {
	attrs := def.node.Attrs
	if !functionKinds[attrs[attrSymbolKind]] || types.IsTestPath(def.path) {
		return touchedFunction{}, false
	}
	end, _ := strconv.Atoi(attrs[attrDefEndLine])
	return touchedFunction{
		Symbol: id, Label: def.node.Label, Path: def.path, Language: attrs[knowledge.AttrLanguage],
		Digest: attrs[knowledge.AttrBodyDigest], Change: change,
		Line: sourceLine(def.node.Source), EndLine: end,
	}, true
}
