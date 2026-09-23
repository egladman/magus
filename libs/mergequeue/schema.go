package mergequeue

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Schema names. Each document carries its own under "schema"; a reader refuses any
// other, so a format change is a new name rather than a silent misread.
const (
	SchemaChanges = "mergequeue.changes/v1"
	SchemaPlan    = "mergequeue.plan/v1"
	SchemaVerdict = "mergequeue.verdict/v1"
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
	// DecisionMerge: validated green; merge once every change beneath it has.
	DecisionMerge Decision = "merge"
	// DecisionKick: a real conflict, a red gate, or a refusal; kick back with the report.
	DecisionKick Decision = "kick"
	// DecisionWait: not approved, or held behind a conflicting change; retried next run.
	DecisionWait Decision = "wait"
)

// Plan is what validation stages and an [Applier] merges against.
type Plan struct {
	Schema     string `json:"schema"`
	Base       string `json:"base"`
	BaseCommit string `json:"base_commit"` // tip of Base every stage is built on
	// Depth is how many stages of one partition validate at once.
	Depth int `json:"depth"`
	// Partitions hold the admitted changes in queue order. Changes in one partition
	// stack; separate partitions share no affected unit and never wait on each other.
	Partitions [][]Change `json:"partitions"`
	// Verdicts are the changes planning settled without validating them.
	Verdicts []Verdict `json:"verdicts,omitempty"`
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

// Verdict is what the queue decided for one change. Validation writes one per admitted
// change as a document of its own; planning's ride in [Plan.Verdicts].
type Verdict struct {
	Schema     string   `json:"schema,omitempty"`
	BaseCommit string   `json:"base_commit,omitempty"`
	Change     Change   `json:"change"`
	Decision   Decision `json:"decision"`
	Reason     string   `json:"reason,omitempty"`
	Report     string   `json:"report,omitempty"` // kick-back body
	// After is the change validated beneath this one, empty at the bottom of its
	// partition. An Applier holds a change whose After did not merge.
	After string `json:"after,omitempty"`
	// Onto is the commit the stage was built onto: BaseCommit at the bottom of a
	// partition, else After's stage.
	Onto string `json:"onto,omitempty"`
	// Stage is the validated staging commit: base plus every change beneath this one
	// plus this one, derived files regenerated.
	Stage   string `json:"stage,omitempty"`
	Message string `json:"message,omitempty"` // squash body: the change's own commits
	// Depth is the stage's speculation depth when its gate started: 1 ran on validated
	// commits alone, 2 on top of one unvalidated stage, and so on.
	Depth      int   `json:"depth,omitempty"`
	DurationMS int64 `json:"duration_ms,omitempty"` // gate wall time

	// StageFile is the file holding Stage's commits, set by whoever read the verdict.
	StageFile string `json:"-"`
}

// ReadChanges decodes and checks a [Changes] document.
func ReadChanges(r io.Reader) (Changes, error) {
	var c Changes
	if err := decode(r, SchemaChanges, &c, &c.Schema); err != nil {
		return Changes{}, err
	}
	if err := c.check(); err != nil {
		return Changes{}, fmt.Errorf("%s: %w", SchemaChanges, err)
	}
	return c, nil
}

func (c Changes) check() error {
	if err := CheckBranch(c.Base); err != nil {
		return fmt.Errorf("base: %w", err)
	}
	seen := make(map[string]bool, len(c.Changes))
	for i, ch := range c.Changes {
		if err := ch.Check(); err != nil {
			return fmt.Errorf("changes[%d]: %w", i, err)
		}
		if seen[ch.ID] {
			return fmt.Errorf("changes[%d]: id %q appears twice", i, ch.ID)
		}
		seen[ch.ID] = true
	}
	return nil
}

// ReadPlan decodes and checks a [Plan] document.
func ReadPlan(r io.Reader) (Plan, error) {
	var p Plan
	if err := decode(r, SchemaPlan, &p, &p.Schema); err != nil {
		return Plan{}, err
	}
	if err := p.check(); err != nil {
		return Plan{}, fmt.Errorf("%s: %w", SchemaPlan, err)
	}
	return p, nil
}

func (p Plan) check() error {
	if err := CheckBranch(p.Base); err != nil {
		return fmt.Errorf("base: %w", err)
	}
	if !isObjectID(p.BaseCommit) {
		return fmt.Errorf("base_commit %q is not a commit id", p.BaseCommit)
	}
	if p.Depth < 1 {
		return fmt.Errorf("depth %d is below 1", p.Depth)
	}
	seen := map[string]bool{}
	add := func(c Change) error {
		if err := c.Check(); err != nil {
			return err
		}
		if seen[c.ID] {
			return fmt.Errorf("change %q appears twice", c.ID)
		}
		seen[c.ID] = true
		return nil
	}
	for _, g := range p.Partitions {
		for _, c := range g {
			if err := add(c); err != nil {
				return err
			}
		}
	}
	for _, v := range p.Verdicts {
		if err := add(v.Change); err != nil {
			return err
		}
		if v.Decision != DecisionKick && v.Decision != DecisionWait {
			return fmt.Errorf("planning decides %q for %s; it only kicks back or waits", v.Decision, v.Change.Label())
		}
	}
	return nil
}

// ReadVerdict decodes and checks a [Verdict] document.
func ReadVerdict(r io.Reader) (Verdict, error) {
	var v Verdict
	if err := decode(r, SchemaVerdict, &v, &v.Schema); err != nil {
		return Verdict{}, err
	}
	if err := v.Change.Check(); err != nil {
		return Verdict{}, fmt.Errorf("%s: %w", SchemaVerdict, err)
	}
	switch v.Decision {
	case DecisionMerge, DecisionKick, DecisionWait:
	default:
		return Verdict{}, fmt.Errorf("%s: unknown decision %q", SchemaVerdict, v.Decision)
	}
	return v, nil
}

// WriteChanges encodes c on one line, stamping its schema.
func WriteChanges(w io.Writer, c Changes) error {
	c.Schema = SchemaChanges
	return json.NewEncoder(w).Encode(c)
}

// WritePlan encodes p, stamping its schema.
func WritePlan(w io.Writer, p Plan) error {
	p.Schema = SchemaPlan
	if p.Partitions == nil {
		p.Partitions = [][]Change{} // "partitions": [] rather than null for a reader iterating it
	}
	return encodeIndented(w, p)
}

// WriteVerdict encodes v, stamping its schema.
func WriteVerdict(w io.Writer, v Verdict) error {
	v.Schema = SchemaVerdict
	return encodeIndented(w, v)
}

func encodeIndented(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func decode(r io.Reader, schema string, v any, got *string) error {
	if err := json.NewDecoder(r).Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", schema, err)
	}
	if *got != schema {
		return fmt.Errorf("document is %q, want %q", *got, schema)
	}
	return nil
}

// Check reports whether c can be handed to a version control system and used as a
// directory name: an id [CheckID] accepts, a full commit id as head, and refs and
// branches in the strict grammar [CheckBranch] names. Every field reaches a VCS command
// line, so a value that could read as an option or a refspec is refused here rather than
// quoted there.
func (c Change) Check() error {
	if err := CheckID(c.ID); err != nil {
		return err
	}
	if !isObjectID(c.Head) {
		return fmt.Errorf("%s: head %q is not a full commit id", c.Label(), c.Head)
	}
	if c.Ref != "" {
		if !strings.HasPrefix(c.Ref, "refs/") {
			return fmt.Errorf("%s: ref %q does not start with refs/", c.Label(), c.Ref)
		}
		if err := checkRefName(c.Ref); err != nil {
			return fmt.Errorf("%s: ref: %w", c.Label(), err)
		}
	}
	for _, b := range []struct{ name, v string }{{"branch", c.Branch}, {"base", c.Base}} {
		if b.v == "" {
			continue
		}
		if err := CheckBranch(b.v); err != nil {
			return fmt.Errorf("%s: %s: %w", c.Label(), b.name, err)
		}
	}
	return nil
}

// CheckID accepts 1 to 128 ASCII letters, digits, '.', '_' and '-', not starting with
// '.' or '-'. An id names a directory and an artifact, so "..", a separator, or a
// leading dot (which marks an entry in progress) would escape or hide it.
func CheckID(id string) error {
	if id == "" || len(id) > 128 || id[0] == '.' || id[0] == '-' {
		return fmt.Errorf("change id %q is empty, too long, or starts with '.' or '-'", id)
	}
	for i := range len(id) {
		b := id[i]
		if !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '.' || b == '_' || b == '-') {
			return fmt.Errorf("change id %q holds %q; ids are letters, digits, '.', '_' and '-'", id, b)
		}
	}
	return nil
}

// CheckBranch accepts a branch name in git's check-ref-format grammar, which the queue
// holds every version control system to.
func CheckBranch(name string) error {
	if name == "" {
		return errors.New("empty branch name")
	}
	if strings.HasPrefix(name, "refs/") {
		return fmt.Errorf("branch %q is a full ref; name the branch alone", name)
	}
	return checkRefName(name)
}

// checkRefName applies git check-ref-format's rules, plus no leading '-'.
func checkRefName(name string) error {
	bad := func(why string) error { return fmt.Errorf("%q %s", name, why) }
	switch {
	case strings.HasPrefix(name, "-"):
		return bad("starts with '-'")
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//"):
		return bad("has an empty component")
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock"):
		return bad("ends with '.' or '.lock'")
	case strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "/."):
		return bad("holds '..', '@{' or a component starting with '.'")
	case name == "@":
		return bad("is '@'")
	}
	for _, r := range name {
		if r < ' ' || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return bad(fmt.Sprintf("holds %q", r))
		}
	}
	return nil
}

// isObjectID reports whether s is a full SHA-1 or SHA-256 object id in lowercase hex.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := range len(s) {
		if b := s[i]; !('0' <= b && b <= '9' || 'a' <= b && b <= 'f') {
			return false
		}
	}
	return true
}
