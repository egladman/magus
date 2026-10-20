package rankeval

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"time"
)

// judgedSets are the sets queries.jsonl may name.
var judgedSets = []EvalSet{SetJudgedDev, SetJudgedHoldout}

// Query is one judged query as queries.jsonl holds it. Source says where its text
// came from, such as a commit subject or a harvested row.
type Query struct {
	ID     string     `json:"query_id"`
	Query  string     `json:"query"`
	Source string     `json:"source"`
	Set    EvalSet    `json:"eval_set"`
	Shape  QueryShape `json:"shape"`
	Date   string     `json:"date"`
}

// MarshalJSON refuses a query that would not read back.
func (q Query) MarshalJSON() ([]byte, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	type plain Query
	return marshalJSON(plain(q))
}

// UnmarshalJSON is strict: an unknown or repeated field is an error, as is any query
// MarshalJSON would refuse.
func (q *Query) UnmarshalJSON(b []byte) error {
	type plain Query
	return decodeStrict(b, q, func(p plain) Query { return Query(p) })
}

func (q Query) validate() error {
	switch {
	case q.ID == "":
		return fmt.Errorf("empty query_id")
	case q.Query == "":
		return fmt.Errorf("query %s: empty query", q.ID)
	case q.Source == "":
		return fmt.Errorf("query %s: empty source", q.ID)
	case !slices.Contains(judgedSets, q.Set):
		return fmt.Errorf("query %s: eval set %q is not one of %v", q.ID, q.Set, judgedSets)
	case !slices.Contains(shapes, q.Shape):
		return fmt.Errorf("query %s: shape %q is not one of %v", q.ID, q.Shape, shapes)
	}
	if _, err := time.Parse(time.DateOnly, q.Date); err != nil {
		return fmt.Errorf("query %s: date %q is not YYYY-MM-DD", q.ID, q.Date)
	}
	return nil
}

// ReadQueries reads queries.jsonl and names the line of the first row that does not
// decode, and of a query_id read twice.
func ReadQueries(r io.Reader) ([]Query, error) {
	qs, err := readLines[Query](r, "queries")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(qs))
	for _, q := range qs {
		if seen[q.ID] {
			return nil, fmt.Errorf("queries: query_id %s appears twice", q.ID)
		}
		seen[q.ID] = true
	}
	return qs, nil
}

// Qrel is one relevance judgment of one file for one query, as qrels.jsonl holds it:
// grade 2 is where the asked-about behavior lives, 1 is related context and 0 is not
// useful. Guideline and Judge name the written guideline and the judge that applied
// it. Batch marks a probe or re-judge row, which never enters scoring.
type Qrel struct {
	QueryID   string `json:"query_id"`
	File      string `json:"file"`
	Grade     int    `json:"grade"`
	Guideline string `json:"guideline"`
	Judge     string `json:"judge"`
	Rationale string `json:"rationale,omitempty"`
	Batch     string `json:"batch,omitempty"`
}

// MarshalJSON refuses a judgment that would not read back.
func (q Qrel) MarshalJSON() ([]byte, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	type plain Qrel
	return marshalJSON(plain(q))
}

// UnmarshalJSON is strict: an unknown or repeated field is an error, as is any
// judgment MarshalJSON would refuse. A grade field left out is an error rather than
// a 0.
func (q *Qrel) UnmarshalJSON(b []byte) error {
	type plain Qrel
	p := plain{Grade: -1}
	if err := unmarshalStrictJSON(b, &p); err != nil {
		return err
	}
	if err := Qrel(p).validate(); err != nil {
		return invalid{err}
	}
	*q = Qrel(p)
	return nil
}

func (q Qrel) validate() error {
	switch {
	case q.QueryID == "":
		return fmt.Errorf("empty query_id")
	case q.File == "":
		return fmt.Errorf("qrel %s: empty file", q.QueryID)
	case q.Grade < 0 || q.Grade > 2:
		return fmt.Errorf("qrel %s %s: grade %d, want 0-2", q.QueryID, q.File, q.Grade)
	case q.Guideline == "":
		return fmt.Errorf("qrel %s %s: empty guideline", q.QueryID, q.File)
	case q.Judge == "":
		return fmt.Errorf("qrel %s %s: empty judge", q.QueryID, q.File)
	}
	return nil
}

// ReadQrels reads qrels.jsonl and names the line of the first row that does not
// decode. It keeps every row, re-judges included; JudgedGold settles which counts.
func ReadQrels(r io.Reader) ([]Qrel, error) {
	return readLines[Qrel](r, "qrels")
}

// JudgedGold turns qrels into file gold per query id. A row with a Batch is left
// out. Of rows sharing (query_id, file, guideline, judge) the last wins, so a
// re-judge appends rather than rewrites. A file still graded under two guidelines
// or by two judges is an error: scoring needs one grade, and choosing between them
// is the caller's decision, not a default.
func JudgedGold(qrels []Qrel) (map[string]map[string]int, error) {
	type key struct{ query, file string }
	type grader struct{ guideline, judge string }
	last := map[key]map[grader]int{}
	for _, q := range qrels {
		if q.Batch != "" {
			continue
		}
		k := key{q.QueryID, q.File}
		if last[k] == nil {
			last[k] = map[grader]int{}
		}
		last[k][grader{q.Guideline, q.Judge}] = q.Grade
	}
	gold := map[string]map[string]int{}
	for k, by := range last {
		if len(by) > 1 {
			return nil, fmt.Errorf("qrels: %s %s is graded under %d guideline/judge pairs, want one", k.query, k.file, len(by))
		}
		if gold[k.query] == nil {
			gold[k.query] = map[string]int{}
		}
		for _, grade := range by {
			gold[k.query][fileID(k.file)] = grade
		}
	}
	return gold, nil
}

// JudgedJudgments pairs every query of set with its gold as a Judgment asked
// through query. A query with no qrel is still returned, with empty gold, so it
// counts as unscorable rather than vanishing.
func JudgedJudgments(queries []Query, gold map[string]map[string]int, set EvalSet) []Judgment {
	var out []Judgment
	for _, q := range queries {
		if q.Set != set {
			continue
		}
		out = append(out, Judgment{
			ID: q.ID, Query: q.Query, Verb: "query", Date: q.Date, Source: q.Source,
			Set: set, Shape: q.Shape, Gold: gold[q.ID],
		})
	}
	return out
}

// DigestQueries is the hex sha256 of the queries of set, sorted by id, one encoded
// Query per line. It names a topic set in the holdout ledger. It covers the queries
// and not their qrels, so topping up judgments does not reopen a set already read.
func DigestQueries(queries []Query, set EvalSet) (string, error) {
	var of []Query
	for _, q := range queries {
		if q.Set == set {
			of = append(of, q)
		}
	}
	slices.SortFunc(of, func(a, b Query) int { return cmp.Compare(a.ID, b.ID) })
	h := sha256.New()
	for _, q := range of {
		line, err := marshalJSON(q)
		if err != nil {
			return "", err
		}
		_, _ = h.Write(append(line, '\n'))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileID(path string) string { return "file:" + path }
