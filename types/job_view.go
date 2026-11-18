package types

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// The job module keeps a check as opaque JSON. These views are the module's records with
// each check read as the [LeaseCheck] magus writes there, which is what a Buzz script and
// a printed row show. Each embeds the module record and declares again only the members
// that carry a check: the shallower field wins, in JSON and in the generated bindings.

// Job is a job row with its checks read as lease checks.
type Job struct {
	job.Job
	// Check is the one check this lease runs; nil is none.
	Check *LeaseCheck `json:"check,omitzero" yaml:"check,omitempty"`
	// Goals are the job's definition of done that the store grades, each check goal's
	// check read as a lease check.
	Goals []Goal `json:"goals,omitempty" yaml:"goals,omitempty"`
}

// Goal is a job goal with its check read as a lease check.
type Goal struct {
	job.Goal
	// Check is the run a GoalKindCheck goal examines. Zero on every other kind.
	Check LeaseCheck `json:"check,omitzero" yaml:"check,omitempty"`
}

// JobList is a listing of the ledger with each row's checks read as lease checks.
type JobList struct {
	job.List
	Jobs []Job `json:"jobs" yaml:"jobs"`
}

// Declaration is the typed INPUT for one lease: the fields a caller DECLARES,
// and nothing the store computes.
//
// It is the module's [job.Declaration] with its checks read as lease checks, so the
// published schema spells a check out. It has its own JSON methods because the module
// record's would otherwise be promoted and drop Check and Goals.
type Declaration struct {
	job.Declaration
	// Check is the one check this lease runs, and acceptance binds a worker's evidence to it.
	Check *LeaseCheck `json:"check,omitzero"`
	// Goals are what done means, graded by [Store.Grade]. See [Job.Goals].
	Goals []Goal `json:"goals,omitempty"`
}

// ErrNoLeaseCheck is a stored check that declares none: nil, empty or JSON null.
var ErrNoLeaseCheck = errors.New("declares no check")

// CheckDecodeError is a stored check that does not read as a [LeaseCheck].
type CheckDecodeError struct {
	// Job is the row the check belongs to and Field where it sits: "check" or
	// "goals[<id>].check". Either is empty when the check was read on its own.
	Job, Field string
	Err        error
}

func (e *CheckDecodeError) Error() string {
	at := e.Field
	if at == "" {
		at = "check"
	}
	if e.Job != "" {
		at = e.Job + "'s " + at
	}
	return fmt.Sprintf("%s does not read as a lease check: %v", at, e.Err)
}

func (e *CheckDecodeError) Unwrap() error { return e.Err }

// DecodeLeaseCheck reads a stored check strictly, refusing a member LeaseCheck does not
// declare, and one that declares no check with [ErrNoLeaseCheck].
func DecodeLeaseCheck(raw codec.RawMessage) (LeaseCheck, error) {
	if !job.HasCheck(raw) {
		return LeaseCheck{}, &CheckDecodeError{Err: ErrNoLeaseCheck}
	}
	var c LeaseCheck
	if err := codec.UnmarshalStrict(raw, &c); err != nil {
		return LeaseCheck{}, &CheckDecodeError{Err: err}
	}
	return c, nil
}

// EncodeLeaseCheck is c as the job module stores it.
func EncodeLeaseCheck(c LeaseCheck) (codec.RawMessage, error) {
	raw, err := codec.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encode lease check: %w", err)
	}
	return raw, nil
}

// declaresCheck reports whether c names anything. A zero LeaseCheck is a goal with no
// check, which the module records as nil.
func declaresCheck(c LeaseCheck) bool {
	return c.Target != "" || c.Script != "" || c.Project != "" || len(c.Args) > 0 || c.NoDefaultCharms || c.Test
}

// decodeAt reads raw as DecodeLeaseCheck does, naming where it sits on a refusal.
func decodeAt(raw codec.RawMessage, id, field string) (LeaseCheck, error) {
	c, err := DecodeLeaseCheck(raw)
	if err != nil {
		var decode *CheckDecodeError
		if errors.As(err, &decode) {
			return LeaseCheck{}, &CheckDecodeError{Job: id, Field: field, Err: decode.Err}
		}
		return LeaseCheck{}, err
	}
	return c, nil
}

// NewGoal is g with its check read as a lease check.
func NewGoal(g job.Goal) (Goal, error) {
	view := Goal{Goal: g}
	view.Goal.Check = nil
	if job.HasCheck(g.Check) {
		c, err := decodeAt(g.Check, "", goalField(g.ID))
		if err != nil {
			return Goal{}, err
		}
		view.Check = c
	}
	return view, nil
}

// Module is g as the job module records it.
func (g Goal) Module() (job.Goal, error) {
	out := g.Goal
	out.Check = nil
	if !declaresCheck(g.Check) {
		return out, nil
	}
	raw, err := EncodeLeaseCheck(g.Check)
	if err != nil {
		return job.Goal{}, err
	}
	out.Check = raw
	return out, nil
}

// MarshalYAML prints g as its JSON reads. See [Job.MarshalYAML].
func (g Goal) MarshalYAML() (any, error) { return yamlOfJSON(g) }

func goalField(id string) string {
	if id == job.PrimaryGoalID {
		return "check"
	}
	return fmt.Sprintf("goals[%s].check", id)
}

func newGoals(id string, in []job.Goal) ([]Goal, error) {
	if in == nil {
		return nil, nil
	}
	out := make([]Goal, len(in))
	for i, g := range in {
		view, err := NewGoal(g)
		if err != nil {
			var decode *CheckDecodeError
			if errors.As(err, &decode) {
				return nil, &CheckDecodeError{Job: id, Field: decode.Field, Err: decode.Err}
			}
			return nil, err
		}
		out[i] = view
	}
	return out, nil
}

func moduleGoals(in []Goal) ([]job.Goal, error) {
	if in == nil {
		return nil, nil
	}
	out := make([]job.Goal, len(in))
	for i, g := range in {
		m, err := g.Module()
		if err != nil {
			return nil, err
		}
		out[i] = m
	}
	return out, nil
}

// NewJob is row with its checks read as lease checks.
func NewJob(row job.Job) (Job, error) {
	view := Job{Job: row}
	view.Job.Check, view.Job.Goals = nil, nil
	if job.HasCheck(row.Check) {
		c, err := decodeAt(row.Check, row.ID, "check")
		if err != nil {
			return Job{}, err
		}
		view.Check = &c
	}
	goals, err := newGoals(row.ID, row.Goals)
	if err != nil {
		return Job{}, err
	}
	view.Goals = goals
	return view, nil
}

// Row is j as the job module records it.
func (j Job) Row() (job.Job, error) {
	row := j.Job
	row.Check = nil
	if j.Check != nil {
		raw, err := EncodeLeaseCheck(*j.Check)
		if err != nil {
			return job.Job{}, err
		}
		row.Check = raw
	}
	goals, err := moduleGoals(j.Goals)
	if err != nil {
		return job.Job{}, err
	}
	row.Goals = goals
	return row, nil
}

// MarshalYAML prints j as its JSON reads. A yaml tag cannot inline the module row
// beside the members that hide its own, so the row goes through its JSON.
func (j Job) MarshalYAML() (any, error) { return yamlOfJSON(j) }

// NewJobList is list with every row's checks read as lease checks.
func NewJobList(list job.List) (JobList, error) {
	view := JobList{List: list}
	view.List.Jobs = nil
	if list.Jobs != nil {
		view.Jobs = make([]Job, len(list.Jobs))
	}
	for i, row := range list.Jobs {
		j, err := NewJob(row)
		if err != nil {
			return JobList{}, err
		}
		view.Jobs[i] = j
	}
	return view, nil
}

// MarshalYAML prints l as its JSON reads. See [Job.MarshalYAML].
func (l JobList) MarshalYAML() (any, error) { return yamlOfJSON(l) }

// NewDeclaration is d with its checks read as lease checks.
func NewDeclaration(d job.Declaration) (Declaration, error) {
	view := Declaration{Declaration: d}
	view.Declaration.Check, view.Declaration.Goals = nil, nil
	if job.HasCheck(d.Check) {
		c, err := decodeAt(d.Check, d.ID, "check")
		if err != nil {
			return Declaration{}, err
		}
		view.Check = &c
	}
	goals, err := newGoals(d.ID, d.Goals)
	if err != nil {
		return Declaration{}, err
	}
	view.Goals = goals
	return view, nil
}

// Module is d as the job module reads it.
func (d Declaration) Module() (job.Declaration, error) {
	out := d.Declaration
	out.Check = nil
	if d.Check != nil {
		raw, err := EncodeLeaseCheck(*d.Check)
		if err != nil {
			return job.Declaration{}, err
		}
		out.Check = raw
	}
	goals, err := moduleGoals(d.Goals)
	if err != nil {
		return job.Declaration{}, err
	}
	out.Goals = goals
	return out, nil
}

// MarshalJSON writes d as the module's declaration does, its checks in their stored form.
func (d Declaration) MarshalJSON() ([]byte, error) {
	m, err := d.Module()
	if err != nil {
		return nil, err
	}
	return m.MarshalJSON()
}

// UnmarshalJSON reads d as the module's declaration does, then reads its checks strictly.
func (d *Declaration) UnmarshalJSON(data []byte) error {
	var m job.Declaration
	if err := m.UnmarshalJSON(data); err != nil {
		return err
	}
	view, err := NewDeclaration(m)
	if err != nil {
		return err
	}
	*d = view
	return nil
}

// yamlOfJSON is v's JSON as a YAML node, so its keys and their order are the JSON's.
func yamlOfJSON(v any) (any, error) {
	b, err := codec.Marshal(v)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(b, &node); err != nil {
		return nil, fmt.Errorf("read JSON as YAML: %w", err)
	}
	if len(node.Content) == 1 {
		return node.Content[0], nil
	}
	return &node, nil
}
