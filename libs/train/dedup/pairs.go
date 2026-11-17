package dedup

import (
	"cmp"
	"slices"
)

// Doc is one file the dedup reads: its path, language and text.
type Doc struct {
	Path     string
	Language string
	Text     string
}

// Pair is two files that repeat each other, A before B by path. Kind is "exact" when
// their normalized bytes are equal and "near" when their shingle Jaccard reaches
// Threshold.
type Pair struct {
	A       string  `json:"a"`
	B       string  `json:"b"`
	Kind    string  `json:"kind"`
	Jaccard float64 `json:"jaccard"`
}

// Report counts what Find saw, per language.
type Report struct {
	Docs     map[string]int `json:"docs"`
	Exact    map[string]int `json:"exact"`
	Near     map[string]int `json:"near"`
	Unlexed  map[string]int `json:"unlexed"`
	TooShort map[string]int `json:"too_short"`
}

// Find returns every exact and near pair among docs, sorted by A then B. Files of
// different languages never pair. Exact copies are chained to the first path of their
// run, not paired all-to-all; near pairs are every candidate whose Jaccard reaches
// Threshold, tested exactly on the shingle sets. A file that fails to lex counts under
// Unlexed and takes part in exact dedup only.
func Find(docs []Doc) ([]Pair, Report) {
	rep := Report{Docs: map[string]int{}, Exact: map[string]int{}, Near: map[string]int{},
		Unlexed: map[string]int{}, TooShort: map[string]int{}}
	sorted := slices.Clone(docs)
	slices.SortFunc(sorted, func(a, b Doc) int { return cmp.Compare(a.Path, b.Path) })

	var pairs []Pair
	exact := map[string]string{}
	type entry struct {
		doc   Doc
		shing []uint64
	}
	var keep []entry
	for _, d := range sorted {
		rep.Docs[d.Language]++
		key := d.Language + "\x00" + ExactKey(d.Text)
		if first, ok := exact[key]; ok {
			pairs = append(pairs, Pair{A: first, B: d.Path, Kind: "exact", Jaccard: 1})
			rep.Exact[d.Language]++
			continue
		}
		exact[key] = d.Path
		toks, err := Tokens(d.Language, d.Text)
		if err != nil {
			rep.Unlexed[d.Language]++
			continue
		}
		sh := Shingles(toks)
		if len(sh) == 0 {
			rep.TooShort[d.Language]++
			continue
		}
		keep = append(keep, entry{d, sh})
	}

	type bucket struct {
		band int
		key  uint64
	}
	buckets := map[bucket][]int{}
	for i, e := range keep {
		for b, k := range BandKeys(Signature(e.shing)) {
			buckets[bucket{b, k}] = append(buckets[bucket{b, k}], i)
		}
	}
	seen := map[[2]int]bool{}
	for _, members := range buckets {
		for x := 0; x < len(members); x++ {
			for y := x + 1; y < len(members); y++ {
				i, j := members[x], members[y]
				if i > j {
					i, j = j, i
				}
				if keep[i].doc.Language != keep[j].doc.Language || seen[[2]int{i, j}] {
					continue
				}
				seen[[2]int{i, j}] = true
				if jac := Jaccard(keep[i].shing, keep[j].shing); jac >= Threshold {
					pairs = append(pairs, Pair{A: keep[i].doc.Path, B: keep[j].doc.Path, Kind: "near", Jaccard: jac})
					rep.Near[keep[i].doc.Language]++
				}
			}
		}
	}
	slices.SortFunc(pairs, func(a, b Pair) int {
		return cmp.Or(cmp.Compare(a.A, b.A), cmp.Compare(a.B, b.B), cmp.Compare(a.Kind, b.Kind))
	})
	return pairs, rep
}
