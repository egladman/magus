package rankeval

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"slices"

	"github.com/egladman/magus/internal/json"
)

// QueryShape tags a judgment by the kind of text its query holds, so a gain on one
// shape cannot hide a loss on another.
type QueryShape string

const (
	ShapeIdentifier QueryShape = "identifier"
	ShapePath       QueryShape = "path"
	ShapeProse      QueryShape = "prose"
	ShapeField      QueryShape = "field"
)

var (
	shapes = []QueryShape{ShapeIdentifier, ShapePath, ShapeProse, ShapeField}
	verbs  = []string{"query", "refs", "explain"}
	splits = []string{"dev", "holdout"}
)

// Judgment is one retrieval call paired with what the agent used next. Gold maps a
// graph node id to its grade: 3 written, 2 read with a line window or through
// refs --source, 1 explained, read whole or run as a target.
type Judgment struct {
	ID     string         `json:"id"`
	Query  string         `json:"query"`
	Verb   string         `json:"verb"`
	Date   string         `json:"date"`
	Source string         `json:"source"`
	Split  string         `json:"split"`
	Shape  QueryShape     `json:"shape"`
	Gold   map[string]int `json:"gold"`
}

// ReadJudgments reads JSON Lines, one Judgment per non-blank line. It is strict: an
// unknown field, an empty id, query or gold, a grade outside 1-3, or an unknown verb,
// shape or split is an error naming the line.
func ReadJudgments(r io.Reader) ([]Judgment, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out []Judgment
	for line := 1; sc.Scan(); line++ {
		text := bytes.TrimSpace(sc.Bytes())
		if len(text) == 0 {
			continue
		}
		var j Judgment
		if err := json.UnmarshalStrict(text, &j); err != nil {
			return nil, fmt.Errorf("judgments line %d: %w", line, err)
		}
		if err := j.validate(); err != nil {
			return nil, fmt.Errorf("judgments line %d: %w", line, err)
		}
		out = append(out, j)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read judgments: %w", err)
	}
	return out, nil
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
	case !slices.Contains(verbs, j.Verb):
		return fmt.Errorf("judgment %s: verb %q is not one of %v", j.ID, j.Verb, verbs)
	case !slices.Contains(shapes, j.Shape):
		return fmt.Errorf("judgment %s: shape %q is not one of %v", j.ID, j.Shape, shapes)
	case !slices.Contains(splits, j.Split):
		return fmt.Errorf("judgment %s: split %q is not one of %v", j.ID, j.Split, splits)
	case len(j.Gold) == 0:
		return fmt.Errorf("judgment %s: empty gold", j.ID)
	}
	for id, grade := range j.Gold {
		if grade < 1 || grade > 3 {
			return fmt.Errorf("judgment %s: gold %s has grade %d, want 1-3", j.ID, id, grade)
		}
	}
	return nil
}
