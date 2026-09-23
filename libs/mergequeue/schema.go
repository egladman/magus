package mergequeue

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// Schema names. Each document carries its own under "schema"; a reader refuses any
// other, so a format change is a new name rather than a silent misread.
const (
	SchemaChanges = "mergequeue.changes/v1"
	SchemaPlan    = "mergequeue.plan/v1"
	SchemaStage   = "mergequeue.stage/v1"
	SchemaEvent   = "mergequeue.event/v1"
)

// Changes is the queue's input: the changes carrying merge intent, in queue order.
type Changes struct {
	Schema  string   `json:"schema"`
	Base    string   `json:"base"`             // branch the queue merges into
	Remote  string   `json:"remote,omitempty"` // remote the provider was asked about
	Changes []Change `json:"changes"`
}

// Decision is what the queue decided for one change.
type Decision string

const (
	// DecisionLand: validated green; land once every change beneath it has.
	DecisionLand Decision = "land"
	// DecisionKick: a real conflict, a red gate, or a refusal; kick back with the report.
	DecisionKick Decision = "kick"
	// DecisionWait: not approved, or held behind a conflicting change; retried next run.
	DecisionWait Decision = "wait"
)

// Decided is a change planning settled without validating it.
type Decided struct {
	Change   Change   `json:"change"`
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason,omitempty"`
	Report   string   `json:"report,omitempty"` // kick-back body
}

// Plan is what validation stages and landing lands against.
type Plan struct {
	Schema  string `json:"schema"`
	Base    string `json:"base"`
	BaseSHA string `json:"base_sha"` // tip every stage is built on
	Remote  string `json:"remote,omitempty"`
	// Depth is how many stages of one partition validate at once.
	Depth int `json:"depth"`
	// Partitions hold the admitted changes in queue order. Changes in one partition
	// stack; separate partitions share no affected unit and never wait on each other.
	Partitions [][]Change `json:"partitions"`
	Decided    []Decided  `json:"decided,omitempty"`
}

// Admitted returns every admitted change in partition order.
func (p Plan) Admitted() []Change {
	return slices.Concat(p.Partitions...)
}

// Find returns the partition holding id and its position there.
func (p Plan) Find(id string) (partition, pos int, ok bool) {
	for gi, g := range p.Partitions {
		for i, c := range g {
			if c.ID == id {
				return gi, i, true
			}
		}
	}
	return 0, 0, false
}

// StageResult is validation's verdict on one admitted change.
type StageResult struct {
	Schema   string   `json:"schema"`
	BaseSHA  string   `json:"base_sha"`
	Change   Change   `json:"change"`
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason,omitempty"`
	Report   string   `json:"report,omitempty"`
	// After is the change validated beneath this one, empty at the bottom of its
	// partition. Landing holds a change whose After did not land.
	After string `json:"after,omitempty"`
	// Stage is the validated staging commit: base plus every change beneath this one
	// plus this one, derived files regenerated.
	Stage   string `json:"stage,omitempty"`
	Message string `json:"message,omitempty"` // squash body: the change's own commits
	// Depth is the stage's speculation depth when its gate started: 1 ran on validated
	// commits alone, 2 on top of one unvalidated stage, and so on.
	Depth      int   `json:"depth,omitempty"`
	DurationMS int64 `json:"duration_ms,omitempty"` // gate wall time

	// Bundle is the file holding Stage's commits, set by whoever read the result.
	Bundle string `json:"-"`
}

// ReadChanges decodes a [Changes] document.
func ReadChanges(r io.Reader) (Changes, error) {
	var c Changes
	if err := decode(r, SchemaChanges, &c, &c.Schema); err != nil {
		return Changes{}, err
	}
	if c.Base == "" {
		return Changes{}, fmt.Errorf("mergequeue: %s names no base", SchemaChanges)
	}
	for i, ch := range c.Changes {
		if ch.ID == "" || ch.Head == "" {
			return Changes{}, fmt.Errorf("mergequeue: %s: changes[%d] needs both id and head", SchemaChanges, i)
		}
	}
	return c, nil
}

// ReadPlan loads a [Plan] from file.
func ReadPlan(file string) (Plan, error) {
	f, err := os.Open(file)
	if err != nil {
		return Plan{}, err
	}
	defer f.Close()
	var p Plan
	if err := decode(f, SchemaPlan, &p, &p.Schema); err != nil {
		return Plan{}, fmt.Errorf("%s: %w", file, err)
	}
	if p.Base == "" || p.BaseSHA == "" {
		return Plan{}, fmt.Errorf("%s: the plan names no base", file)
	}
	return p, nil
}

// ReadStageResult loads a [StageResult] from file.
func ReadStageResult(file string) (StageResult, error) {
	f, err := os.Open(file)
	if err != nil {
		return StageResult{}, err
	}
	defer f.Close()
	var r StageResult
	if err := decode(f, SchemaStage, &r, &r.Schema); err != nil {
		return StageResult{}, fmt.Errorf("%s: %w", file, err)
	}
	return r, nil
}

func decode(r io.Reader, schema string, v any, got *string) error {
	dec := json.NewDecoder(r)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("mergequeue: decode %s: %w", schema, err)
	}
	if *got != schema {
		return fmt.Errorf("mergequeue: document is %q, want %q", *got, schema)
	}
	return nil
}

// WriteJSON writes v to file through a temporary sibling, so a reader polling for file
// never sees half of it.
func WriteJSON(file string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
