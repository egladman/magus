package textembed

import (
	"fmt"
	"slices"
)

type externalText struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type externalSubset struct {
	name    string
	queries []externalText
	docs    []externalText
	qrels   map[string]map[string]int
	notes   []string
}

// dedupeDocuments collapses docs with identical text onto the smallest id among
// them before indexing, and maps the qrels onto it at the highest grade. Collapsing
// after ranking instead would shrink a top-100 list below 100 unique docs.
func dedupeDocuments(s externalSubset) externalSubset {
	canonical, first := map[string]string{}, map[string]string{}
	var docs []externalText
	for _, d := range s.docs {
		if id, ok := first[d.Text]; ok {
			canonical[d.ID] = id
			continue
		}
		first[d.Text], canonical[d.ID] = d.ID, d.ID
		docs = append(docs, d)
	}
	qrels := map[string]map[string]int{}
	for q, gold := range s.qrels {
		for d, g := range gold {
			addQrel(qrels, q, canonical[d], g)
		}
	}
	return externalSubset{
		name: s.name + "-dedup", queries: s.queries, docs: docs, qrels: qrels,
		notes: append(slices.Clone(s.notes), fmt.Sprintf("%d docs collapsed onto %d distinct texts.", len(s.docs), len(docs))),
	}
}

func addQrel(qrels map[string]map[string]int, q, d string, grade int) {
	if grade <= 0 {
		return
	}
	if qrels[q] == nil {
		qrels[q] = map[string]int{}
	}
	qrels[q][d] = max(qrels[q][d], grade)
}
