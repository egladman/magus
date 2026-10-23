package knowledge

import (
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// packageReach counts, per package directory other than own, the distinct nodes at the far
// end of id's edges of relation rel whose file keep accepts: the files referencing id when
// in is set, else the symbols id points at. It is how a metric asks whether code crosses
// out of its package, and to where.
func (x *conformanceIndex) packageReach(id, own string, rel types.RelationID, in bool, keep func(file string) bool) map[string]int {
	edges := x.g.out[id]
	if in {
		edges = x.g.in[id]
	}
	seen := map[string]bool{}
	out := map[string]int{}
	for _, e := range edges {
		far := e.Target
		if in {
			far = e.Source
		}
		if e.Relation != rel || seen[far] {
			continue
		}
		seen[far] = true
		file, ok := strings.CutPrefix(far, types.KindFile+":")
		if !ok {
			file, _, ok = splitPathLine(x.g.nodes[far].Source)
		}
		if !ok || path.Dir(file) == own || !keep(file) {
			continue
		}
		out[path.Dir(file)]++
	}
	return out
}

func nonTest(file string) bool { return !types.IsTestPath(file) }

// topReach is the directory reach counts most, ties to the lowest name, or "" when empty.
func topReach(reach map[string]int) string {
	top := ""
	for _, dir := range slices.Sorted(maps.Keys(reach)) {
		if top == "" || reach[dir] > reach[top] {
			top = dir
		}
	}
	return top
}

// mainLogicSamples measures a Go main-package callable that computes rather than wires by
// its body tokens, zero for the rest; Home is the package its file references most.
// Statements would read closer, but only a local coverage profile records them.
func (x *conformanceIndex) mainLogicSamples(add conformanceAdd) {
	homes := map[string]string{}
	for _, s := range x.callables() {
		if s.pop != types.ConformancePopMain || s.node.Attrs[attrLanguage] != "go" || s.shape == "" {
			continue
		}
		file, _, _ := splitPathLine(s.node.Source)
		item := types.ConformanceItem{ID: s.itemID, Source: s.node.Source}
		if x.computes(s, file) {
			item.Measure = s.tokens
			home, ok := homes[file]
			if !ok {
				home = topReach(x.packageReach(fileID(file), path.Dir(file), types.RelationReferences, false, x.sourceFile))
				homes[file] = home
			}
			item.Home = home
		}
		add(types.ConformanceMainLogic, s.pop, "", item)
	}
}

// computes reports whether s, defined in file, is logic rather than wiring.
func (x *conformanceIndex) computes(s conformanceSymbol, file string) bool {
	if len(s.local) == 1 && (s.local[0].Name == "main" || s.local[0].Name == "init") {
		return false
	}
	ifaces := x.interfaceNames(file)
	params, _, _ := goFuncParts(s.node.Attrs[AttrSignature])
	for _, p := range params {
		typ := strings.TrimLeft(p.Type, "*[].")
		if p.Name == "args" && p.Type == "[]string" || typ == "io.Writer" {
			return false
		}
		if _, name, ok := strings.Cut(typ, "."); ok && ifaces[name] || ifaces[typ] {
			return false
		}
	}
	return len(x.packageReach(s.id, path.Dir(file), types.RelationCalls, false, nonTest)) == 0
}

// interfaceNames are the names of the workspace interfaces file defines or references.
func (x *conformanceIndex) interfaceNames(file string) map[string]bool {
	out := map[string]bool{}
	for _, e := range x.g.out[fileID(file)] {
		if n := x.g.nodes[e.Target]; n.Kind == types.KindSymbol && n.Attrs[attrSymbolKind] == "Interface" {
			out[n.Label] = true
		}
	}
	return out
}

func (x *conformanceIndex) sourceFile(file string) bool {
	pop, ok := x.population(file)
	return ok && pop == types.ConformancePopSource
}

// storePathSuffixes end the name of a symbol that says where something lives on disk.
var storePathSuffixes = []string{"Path", "Dir", "File", "FileName", "Ext"}

// stateWriters are the internal/file writers a store goes through; a package whose code
// references one keeps state. Writes made with os directly are invisible: the index holds
// no standard library symbols.
var stateWriters = map[string]bool{
	"WriteFileAtomic": true, "WriteFrom": true, "ReplaceFile": true, "AppendLines": true, "RewriteLines": true, "JSONL": true,
}

// storePathSamples measures each exported path of a package that keeps state by how many
// measured non-test files outside its package reference it: only a store's owner may know
// where it lives. Home is that owner.
func (x *conformanceIndex) storePathSamples(add conformanceAdd) {
	stores := x.stateDirs()
	for _, s := range x.symbols() {
		if s.pop == types.ConformancePopTest || s.node.Attrs[attrLanguage] != "go" || len(s.local) != 1 ||
			!exportedName(s.local[0].Name) || !storePath(s.node, s.local[0].Name) {
			continue
		}
		file, _, _ := splitPathLine(s.node.Source)
		own := path.Dir(file)
		if !stores[own] {
			continue
		}
		n := 0
		for _, c := range x.packageReach(s.id, own, types.RelationReferences, true, x.measuredCode) {
			n += c
		}
		add(types.ConformanceStorePathExport, s.pop, "", types.ConformanceItem{ID: s.itemID, Source: s.node.Source, Measure: n, Home: own})
	}
}

// storePath reports whether n, named name, says where something lives: a string constant
// or variable, or a function returning a string, named with a storePathSuffix.
func storePath(n types.KnowledgeNode, name string) bool {
	if !slices.ContainsFunc(storePathSuffixes, func(suffix string) bool { return strings.HasSuffix(name, suffix) }) {
		return false
	}
	sig := n.Attrs[AttrSignature]
	switch n.Attrs[attrSymbolKind] {
	case "Constant", "Variable":
		_, typ, _ := strings.Cut(sig, " "+name+" ")
		return strings.HasPrefix(strings.TrimPrefix(typ, "untyped "), "string")
	case "Function":
		_, results, ok := goFuncParts(sig)
		return ok && (results == "string" || results == "(string, error)")
	}
	return false
}

func (x *conformanceIndex) measuredCode(file string) bool {
	pop, ok := x.population(file)
	return ok && pop != types.ConformancePopTest
}

// stateDirs are the package directories whose non-test files reference a stateWriter of a
// package ending in internal/file.
func (x *conformanceIndex) stateDirs() map[string]bool {
	out := map[string]bool{}
	for id, f := range x.g.nodes {
		if f.Kind != types.KindFile || types.IsTestPath(f.Source) {
			continue
		}
		for _, e := range x.g.out[id] {
			if e.Relation != types.RelationReferences {
				continue
			}
			ns := x.g.nodes[e.Target].Attrs[attrNamespace]
			local := x.local[e.Target]
			if len(local) == 1 && stateWriters[local[0].Name] && strings.HasSuffix(ns, "/internal/file`/") {
				out[path.Dir(f.Source)] = true
				break
			}
		}
	}
	return out
}
