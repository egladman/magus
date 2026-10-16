package rankeval

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/json"
)

// QueryShape tags a query by the kind of text it holds, so a gain on one shape cannot
// hide a loss on another.
type QueryShape string

const (
	ShapeIdentifier QueryShape = "identifier"
	ShapePath       QueryShape = "path"
	ShapeProse      QueryShape = "prose"
	ShapeField      QueryShape = "field"
)

// EvalSet names one set of queries with its gold. A score is only ever reported per
// set: no two sets are pooled, because each answers a different question. Each set's
// comment names the sets it is disjoint from and how.
type EvalSet string

const (
	// SetJudgedDev holds the queries.jsonl rows marked judged-dev, graded 0-2 from a
	// pooled judgment. Every tuning and selection reads it. It is disjoint from
	// judged-holdout by query id and by query text: the split hashes the query id.
	SetJudgedDev EvalSet = "judged-dev"
	// SetJudgedHoldout holds the queries.jsonl rows marked judged-holdout. It is read
	// once, through the holdout ledger, for a frozen candidate. It is disjoint from
	// judged-dev as above, and from definition-lookup by query text.
	SetJudgedHoldout EvalSet = "judged-holdout"
	// SetDefinitionLookup asks for harvested identifiers through query under
	// kind=symbol, since a bare identifier never reaches the symbol layer, with gold
	// the one file the symbol index says defines each. It is the identifier guard. It
	// drops every identifier queries.jsonl asks, so it is disjoint from judged-dev and
	// judged-holdout by query text. It shares its identifiers with agent-agreement by
	// construction: they are harvested there, but its gold is not.
	SetDefinitionLookup EvalSet = "definition-lookup"
	// SetDocmaskGo asks for a documented Go declaration by its doc's first sentence,
	// with the doc blanked in both indexes. It draws from the provenance-group holdout,
	// so it is disjoint from every training group by package.
	SetDocmaskGo EvalSet = "docmask-go"
	// SetDocmaskBuzz is docmask-go over Buzz declarations, disjoint from training the
	// same way.
	SetDocmaskBuzz EvalSet = "docmask-buzz"
	// SetBuzzIdent asks for Buzz identifiers, drawn from the provenance-group holdout
	// like docmask-buzz and disjoint from its queries by text.
	SetBuzzIdent EvalSet = "buzz-ident"
	// SetAgentAgreement holds harvested transcript rows: a retrieval call and the files
	// an agent touched after it. It measures agreement with what agents used, never
	// relevance, and is report-only: it never gates or selects. Its gold came from the
	// old ranker's output, so it is biased toward that ranker. It is disjoint from no
	// set; definition-lookup reuses its identifiers.
	SetAgentAgreement EvalSet = "agent-agreement"
	// SetFigures asks a figure's node-suggest for its depicted nodes. Report-only. It
	// is disjoint from training by figure fold.
	SetFigures EvalSet = "figures"
)

// EvalSets is every named set in report order: the gate sets first, then the guards,
// then the report-only sets.
var EvalSets = []EvalSet{
	SetJudgedDev, SetJudgedHoldout, SetDefinitionLookup, SetDocmaskGo, SetDocmaskBuzz,
	SetBuzzIdent, SetAgentAgreement, SetFigures,
}

// ReportOnly reports whether s may never gate or select.
func (s EvalSet) ReportOnly() bool {
	return s == SetAgentAgreement || s == SetFigures
}

var (
	shapes = []QueryShape{ShapeIdentifier, ShapePath, ShapeProse, ShapeField}
	verbs  = []string{"query", "refs", "explain"}
)

// Judgment is one query of one eval set with its gold: a graph node id mapped to its
// grade. A grade of 0 is a judged non-relevant file. An agent-agreement row grades
// 3 written, 2 read with a line window and 1 read whole, explained or run as a
// target, and names the transcript session it came from, since rows of one session
// are not independent.
type Judgment struct {
	ID      string         `json:"id"`
	Query   string         `json:"query"`
	Verb    string         `json:"verb"`
	Date    string         `json:"date"`
	Source  string         `json:"source"`
	Session string         `json:"session,omitempty"`
	Set     EvalSet        `json:"eval_set"`
	Shape   QueryShape     `json:"shape"`
	Gold    map[string]int `json:"gold"`
}

// MarshalJSON refuses a judgment that would not read back.
func (j Judgment) MarshalJSON() ([]byte, error) {
	if err := j.validate(); err != nil {
		return nil, err
	}
	type plain Judgment
	return json.Marshal(plain(j))
}

// UnmarshalJSON is strict: an unknown or repeated field is an error, as is any
// judgment MarshalJSON would refuse.
func (j *Judgment) UnmarshalJSON(b []byte) error {
	type plain Judgment
	return decodeStrict(b, j, func(p plain) Judgment { return Judgment(p) })
}

// ReadJudgments reads JSON Lines, one Judgment per non-blank line, and names the line
// of the first that does not decode.
func ReadJudgments(r io.Reader) ([]Judgment, error) {
	return readLines[Judgment](r, "judgments")
}

// Resolved returns j keeping only the gold ids known reports, and how many it dropped:
// a judgment harvested from an older tree can name a node the current graph lacks.
func (j Judgment) Resolved(known func(id string) bool) (Judgment, int) {
	gold := make(map[string]int, len(j.Gold))
	for id, grade := range j.Gold {
		if known(id) {
			gold[id] = grade
		}
	}
	dropped := len(j.Gold) - len(gold)
	j.Gold = gold
	return j, dropped
}

func (j Judgment) validate() error {
	switch {
	case j.ID == "":
		return fmt.Errorf("empty id")
	case j.Query == "":
		return fmt.Errorf("judgment %s: empty query", j.ID)
	case strings.Contains(j.Query, "$"):
		return fmt.Errorf("judgment %s: query %q holds an unexpanded shell variable", j.ID, j.Query)
	case !slices.Contains(verbs, j.Verb):
		return fmt.Errorf("judgment %s: verb %q is not one of %v", j.ID, j.Verb, verbs)
	case !slices.Contains(shapes, j.Shape):
		return fmt.Errorf("judgment %s: shape %q is not one of %v", j.ID, j.Shape, shapes)
	case !slices.Contains(EvalSets, j.Set):
		return fmt.Errorf("judgment %s: eval set %q is not one of %v", j.ID, j.Set, EvalSets)
	case len(j.Gold) == 0:
		return fmt.Errorf("judgment %s: empty gold", j.ID)
	}
	if _, err := time.Parse(time.DateOnly, j.Date); err != nil {
		return fmt.Errorf("judgment %s: date %q is not YYYY-MM-DD", j.ID, j.Date)
	}
	low, high := 0, 2
	if j.Set == SetAgentAgreement {
		if j.Session == "" {
			return fmt.Errorf("judgment %s: an agent-agreement row names no session", j.ID)
		}
		low, high = 1, 3
	}
	for id, grade := range j.Gold {
		if grade < low || grade > high {
			return fmt.Errorf("judgment %s: gold %s has grade %d, want %d-%d", j.ID, id, grade, low, high)
		}
		// Every root-level hit expands to dir:., so it is gold to any ranker.
		if id == "dir:." {
			return fmt.Errorf("judgment %s: dir:. is gold", j.ID)
		}
	}
	return nil
}

// invalid marks an error from a row's own validation, which readLines reports bare
// rather than under the decoder's "cannot unmarshal" wrapping.
type invalid struct{ error }

// decodeStrict is the UnmarshalJSON of every row type T: it decodes b strictly into
// P, T without its methods so the decode does not recurse, and sets *into only when
// validate accepts the row. An unknown or repeated field is an error.
func decodeStrict[P any, T interface{ validate() error }](b []byte, into *T, row func(P) T) error {
	var p P
	if err := json.UnmarshalStrict(b, &p); err != nil {
		return err
	}
	v := row(p)
	if err := v.validate(); err != nil {
		return invalid{err}
	}
	*into = v
	return nil
}

// readLines decodes JSON Lines into T, whose UnmarshalJSON carries the encoding,
// skipping blank lines and naming the line and file of the first error.
func readLines[T any](r io.Reader, file string) ([]T, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out []T
	for line := 1; sc.Scan(); line++ {
		text := bytes.TrimSpace(sc.Bytes())
		if len(text) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(text, &v); err != nil {
			if inv, ok := errors.AsType[invalid](err); ok {
				err = inv.error
			}
			return nil, fmt.Errorf("%s line %d: %w", file, line, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	return out, nil
}
