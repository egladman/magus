package knowledge

import (
	"cmp"
	"maps"
	"slices"

	"github.com/egladman/magus/types"
)

// The signature families count what a language's reader parses out of each function's
// declaration: its parameters' names, types and order, and its result classes. A family keyed
// by more than its language (a parameter type, a pair of names) yields one row per key that
// has a cohort to judge; a key with fewer carriers than the gate is no candidate and makes no
// row.
//
// No family keys a function's result class by the verb its name leads with. Measured on this
// tree at 3e29af6ff, the verbs split: parse returns a value and an error in 127 of 214 functions,
// new a bare value in 215 of 281, load 42 of 86, read 85 of 181. Only predicates (is, has)
// cleared the gate, and a rule that holds only where the answer is already bool says nothing.

// signatureCase is one function's case for a keyed family and the value it takes there.
type signatureCase struct {
	c     types.Case
	value string
}

// signatureTally collects keyed cases per language, in the order they are added.
type signatureTally map[[2]string][]signatureCase

func (t signatureTally) add(d *namingDecl, key, value string) {
	k := [2]string{d.language, key}
	t[k] = append(t[k], signatureCase{c: types.Case{Node: d.id, Source: d.source}, value: value})
}

// rows makes a row of each key with a cohort, keyed by the value most of its cases take; a
// tie goes to the value that sorts first, so the row is stable.
func (t signatureTally) rows(family types.PrecedentFamily, minCohort int, rowKey func(key, value string) types.PrecedentKey) []types.Precedent {
	var out []types.Precedent
	for _, k := range slices.SortedFunc(maps.Keys(t), func(a, b [2]string) int {
		return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]))
	}) {
		cases := t[k]
		if len(cases) < minCohort {
			continue
		}
		counts := map[string]int{}
		for _, sc := range cases {
			counts[sc.value]++
		}
		top := ""
		for v, n := range counts {
			if n > counts[top] || (n == counts[top] && v < top) {
				top = v
			}
		}
		row := types.Precedent{Family: family, Scope: types.PrecedentScope{Language: k[0]}, Key: rowKey(k[1], top)}
		for _, sc := range cases {
			countCase(&row, sc.c, sc.value == top)
		}
		out = append(out, row)
	}
	return out
}

// countCase adds c to row's cohort, as a follower or a departure.
func countCase(row *types.Precedent, c types.Case, follows bool) {
	row.Cohort++
	if !follows {
		row.Departures = append(row.Departures, c)
		return
	}
	row.Follow++
	if len(row.Cited) < precedentCited {
		row.Cited = append(row.Cited, c)
	}
}

// paramNameByType counts, for each parameter type a language's reader classes, the names
// functions give a parameter of it. A function taking the type twice has to name it two ways,
// so it says nothing about the name and is not counted for that type; nor is a parameter left
// unnamed or blank.
func (m precedentMiner) paramNameByType() []types.Precedent {
	t := signatureTally{}
	for _, d := range m.x.callables {
		seen := map[string]int{}
		named := map[string]string{}
		for _, p := range d.shape.Params {
			class := paramClass(d, p)
			if class == "" {
				continue
			}
			seen[class]++
			named[class] = p.Name
		}
		for _, class := range slices.Sorted(maps.Keys(seen)) {
			if name := named[class]; seen[class] == 1 && name != "" && name != "_" {
				t.add(d, class, name)
			}
		}
	}
	return t.rows(types.PrecedentParamNameByType, m.x.cohort, func(class, name string) types.PrecedentKey {
		return types.PrecedentKey{Type: class, Name: name}
	})
}

// paramOrder counts, for each pair of parameter names functions take together, which they
// take first. It is orderCheck's count over a language rather than one scope.
func (m precedentMiner) paramOrder() []types.Precedent {
	type pairKey struct{ lang, a, b string }
	carriers := map[pairKey][]*namingDecl{}
	names := map[*namingDecl][]string{}
	for _, d := range m.x.callables {
		ns := paramNames(d)
		names[d] = ns
		for i, a := range ns {
			for _, b := range ns[i+1:] {
				k := pairKey{d.language, min(a, b), max(a, b)}
				carriers[k] = append(carriers[k], d)
			}
		}
	}
	var out []types.Precedent
	for _, k := range slices.SortedFunc(maps.Keys(carriers), func(x, y pairKey) int {
		return cmp.Or(cmp.Compare(x.lang, y.lang), cmp.Compare(x.a, y.a), cmp.Compare(x.b, y.b))
	}) {
		ds := carriers[k]
		if len(ds) < m.x.cohort {
			continue
		}
		dsNames := make([][]string, len(ds))
		for i, d := range ds {
			dsNames[i] = names[d]
		}
		same, reversed := pairOrder(ds, dsNames, k.a, k.b)
		order := []string{k.a, k.b}
		if len(reversed) > len(same) {
			order = []string{k.b, k.a}
			same = reversed
		}
		row := types.Precedent{
			Family: types.PrecedentParamOrder, Scope: types.PrecedentScope{Language: k.lang},
			Key: types.PrecedentKey{Order: order},
		}
		for _, d := range ds {
			countCase(&row, types.Case{Node: d.id, Source: d.source}, slices.Contains(same, d))
		}
		out = append(out, row)
	}
	return out
}

// ctxFirst counts the functions taking a context by whether it is their first parameter.
func (m precedentMiner) ctxFirst() []types.Precedent {
	byLang := map[string]*types.Precedent{}
	for _, d := range m.x.callables {
		at := slices.IndexFunc(d.shape.Params, func(p namingParam) bool { return paramClass(d, p) == "ctx" })
		if at < 0 {
			continue
		}
		countCase(languageRow(byLang, types.PrecedentCtxFirst, d.language), types.Case{Node: d.id, Source: d.source}, at == 0)
	}
	return derefPrecedents(byLang)
}

// errorLast counts the functions returning an error by whether it is their last result. Only a
// language whose reader orders results is counted.
func (m precedentMiner) errorLast() []types.Precedent {
	byLang := map[string]*types.Precedent{}
	for _, d := range m.x.callables {
		at := slices.Index(d.shape.Results, "error")
		if at < 0 {
			continue
		}
		countCase(languageRow(byLang, types.PrecedentErrorLast, d.language), types.Case{Node: d.id, Source: d.source},
			at == len(d.shape.Results)-1)
	}
	return derefPrecedents(byLang)
}

// paramClass is p's type as d's language reader classes it, or "" for a language whose types
// no reader compares.
func paramClass(d *namingDecl, p namingParam) string {
	if r, ok := shapeReaders[d.language]; ok {
		return r.typeClass(p.Type)
	}
	return ""
}

func languageRow(byLang map[string]*types.Precedent, family types.PrecedentFamily, lang string) *types.Precedent {
	row := byLang[lang]
	if row == nil {
		row = &types.Precedent{Family: family, Scope: types.PrecedentScope{Language: lang}}
		byLang[lang] = row
	}
	return row
}
