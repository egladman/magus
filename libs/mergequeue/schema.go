package mergequeue

import (
	"fmt"
	"io"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/mergequeue/types"
)

// ReadChanges decodes and checks a [types.Changes] document.
func ReadChanges(r io.Reader) (types.Changes, error) {
	var c types.Changes
	if err := decode(r, types.SchemaChanges, &c, &c.Schema); err != nil {
		return types.Changes{}, err
	}
	if err := c.Check(); err != nil {
		return types.Changes{}, fmt.Errorf("%s: %w", types.SchemaChanges, err)
	}
	return c, nil
}

// ReadPlan decodes and checks a [types.Plan] document.
func ReadPlan(r io.Reader) (types.Plan, error) {
	var p types.Plan
	if err := decode(r, types.SchemaPlan, &p, &p.Schema); err != nil {
		return types.Plan{}, err
	}
	if err := p.Check(); err != nil {
		return types.Plan{}, fmt.Errorf("%s: %w", types.SchemaPlan, err)
	}
	return p, nil
}

// readVerdict decodes and checks a [types.Verdict] document.
func readVerdict(r io.Reader) (types.Verdict, error) {
	var v types.Verdict
	if err := decode(r, types.SchemaVerdict, &v, &v.Schema); err != nil {
		return types.Verdict{}, err
	}
	if err := v.Check(); err != nil {
		return types.Verdict{}, fmt.Errorf("%s: %w", types.SchemaVerdict, err)
	}
	return v, nil
}

// WriteChanges checks c and encodes it on one line, stamping its schema.
func WriteChanges(w io.Writer, c types.Changes) error {
	if err := c.Check(); err != nil {
		return fmt.Errorf("%s: %w", types.SchemaChanges, err)
	}
	c.Schema = types.SchemaChanges
	return json.NewEncoder(w).Encode(c)
}

// capabilitiesDoc is a provider's capabilities on one base, as a document.
type capabilitiesDoc struct {
	Schema       string              `json:"schema"`
	Base         string              `json:"base"`
	StackMerge   types.StackMerge    `json:"stack_merge"`
	LinearStacks bool                `json:"linear_stacks"`
	Methods      []types.MergeMethod `json:"methods"`
	// Never omitted: zero is the fact a reader most needs to see.
	RequiredApprovals int          `json:"required_approvals"`
	QueueLabel        string       `json:"queue_label,omitempty"`
	Committer         *personDoc   `json:"committer,omitempty"`
	Setup             *types.Setup `json:"setup,omitempty"`
}

type personDoc struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// WriteCapabilities checks c and encodes what the provider supports on base, on one
// line.
func WriteCapabilities(w io.Writer, base string, c types.Capabilities) error {
	if err := c.Check(); err != nil {
		return fmt.Errorf("%s: %w", types.SchemaCapabilities, err)
	}
	doc := capabilitiesDoc{Schema: types.SchemaCapabilities, Base: base, StackMerge: c.StackMerge,
		LinearStacks: c.LinearStacks, Methods: c.Methods, RequiredApprovals: c.RequiredApprovals, QueueLabel: c.QueueLabel}
	if c.Setup != nil {
		s := *c.Setup
		// [] rather than null for a reader iterating them.
		if s.RequiredChecks == nil {
			s.RequiredChecks = []types.RequiredCheck{}
		}
		if s.Steps == nil {
			s.Steps = []types.SetupStep{}
		}
		doc.Setup = &s
	}
	if c.Committer.Name != "" {
		doc.Committer = &personDoc{Name: c.Committer.Name, Email: c.Committer.Email}
	}
	return json.NewEncoder(w).Encode(doc)
}

// WritePlan checks p and encodes it, stamping its schema.
func WritePlan(w io.Writer, p types.Plan) error {
	if err := p.Check(); err != nil {
		return fmt.Errorf("%s: %w", types.SchemaPlan, err)
	}
	p.Schema = types.SchemaPlan
	if p.Partitions == nil {
		p.Partitions = [][]types.Change{} // "partitions": [] rather than null for a reader iterating it
	}
	return encodeIndented(w, p)
}

// writeVerdict checks v and encodes it, stamping its schema.
func writeVerdict(w io.Writer, v types.Verdict) error {
	if err := v.Check(); err != nil {
		return fmt.Errorf("%s: %w", types.SchemaVerdict, err)
	}
	v.Schema = types.SchemaVerdict
	return encodeIndented(w, v)
}

func encodeIndented(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
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

// stackRef is a change whose head another may carry.
type stackRef struct {
	id, head string
	unqueued bool
}

// stackRefs is every change a change of p may carry the head of.
func stackRefs(p types.Plan) []stackRef {
	var out []stackRef
	for _, m := range p.Merged {
		out = append(out, stackRef{id: m.ID, head: m.Head})
	}
	for _, g := range p.Partitions {
		for _, c := range g {
			out = append(out, stackRef{id: c.ID, head: c.Head})
		}
	}
	for _, v := range p.Verdicts {
		if v.Decision != types.DecisionMerged {
			out = append(out, stackRef{id: v.Change.ID, head: v.Change.Head})
		}
	}
	for _, u := range p.Unqueued {
		out = append(out, stackRef{id: u.ID, head: u.Head, unqueued: true})
	}
	return out
}

// find returns the partition of p holding id and its position there.
func find(p types.Plan, id string) (partition, pos int, ok bool) {
	for gi, g := range p.Partitions {
		for i, c := range g {
			if c.ID == id {
				return gi, i, true
			}
		}
	}
	return 0, 0, false
}

// kickOf is the kick-back v decides.
func kickOf(v types.Verdict) types.Kick {
	return types.Kick{Code: v.Code, Report: v.Report, Paths: v.Paths, With: v.With, CandidateCommit: v.CandidateCommit}
}

// proven reports whether c's affected set bounds everything it can reach.
func proven(c types.Change) bool { return c.Affected != nil && c.UnboundedBy == "" }

// regenerationProven reports whether regenerating, as g accounts for it, runs none of the
// change's code.
func regenerationProven(g types.Generation) bool {
	return len(g.Units) > 0 && len(g.Code) == 0 && g.Unbounded == ""
}
