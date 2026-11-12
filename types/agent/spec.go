package agent

import (
	"errors"
	"fmt"
	"strings"
)

// Spec is a job's agent section: what the agent taking the job is asked for.
// A job with none is ordinary work, such as a server's maintenance run.
type Spec struct {
	// Model is the requested model. Zero leaves the choice to the
	// workspace's matrix or default.
	Model Model `json:"model,omitzero" yaml:"model,omitempty"`
	// Instructions say what the job is for and what done means, as prose
	// a reader grades. The machine-checkable half is the job's goals.
	Instructions string `json:"instructions,omitempty" yaml:"instructions,omitempty"`
	// Worker says which worker takes the job. Its type is always written.
	Worker Worker `json:"worker" yaml:"worker"`
}

// Instructed is a section carrying instructions and no model request, taken
// by the warm worker when there is one.
func Instructed(instructions string) *Spec {
	return &Spec{Instructions: instructions, Worker: Worker{Type: WorkerTypeResume}}
}

// Clone is a copy of s that shares nothing with it, nil for nil.
func (s *Spec) Clone() *Spec {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

// Trimmed is a clone of s with its model and worker reason trimmed of
// space, nil for nil.
func (s *Spec) Trimmed() *Spec {
	c := s.Clone()
	if c != nil {
		c.Model.ID = strings.TrimSpace(c.Model.ID)
		c.Model.Tier = strings.TrimSpace(c.Model.Tier)
		c.Worker.Reason = strings.TrimSpace(c.Worker.Reason)
	}
	return c
}

// RequestedModel is s's model, the zero Model for nil.
func (s *Spec) RequestedModel() Model {
	if s == nil {
		return Model{}
	}
	return s.Model
}

// Model is a requested model: a host model id, a workspace tier name, or
// both. With both, the tier decides and the id must map to no other tier.
type Model struct {
	ID   string `json:"id,omitempty"   yaml:"id,omitempty"`
	Tier string `json:"tier,omitempty" yaml:"tier,omitempty"`
}

// IsZero reports a request naming neither an id nor a tier.
func (m Model) IsZero() bool { return m.ID == "" && m.Tier == "" }

// String is the tier, the id, or `<tier> (<id>)` for both.
func (m Model) String() string {
	switch {
	case m.Tier != "" && m.ID != "":
		return fmt.Sprintf("%s (%s)", m.Tier, m.ID)
	case m.Tier != "":
		return m.Tier
	}
	return m.ID
}

// Worker is who takes a job: the warm worker of the job it follows, or a
// new one, and why.
type Worker struct {
	Type WorkerType `json:"type" yaml:"type"`
	// Reason says why a new worker starts instead of the warm one.
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// WorkerType says whether a job resumes the warm worker or starts a new one.
// A written section always names one, so a reader never infers it.
type WorkerType string

const (
	// WorkerTypeResume continues the warm worker of the followed job.
	WorkerTypeResume WorkerType = "resume"
	// WorkerTypeNew starts a new worker, for the reason the section gives.
	WorkerTypeNew WorkerType = "new"
)

// Validate refuses a section no door stores: a worker type outside the
// set, a new worker with no reason, or a reason on a resume worker.
func (s Spec) Validate() error {
	switch {
	case s.Worker.Type == "":
		return fmt.Errorf("agent: the section names no worker.type, one of %s",
			strings.Join(s.Worker.Type.Values(), ", "))
	case !s.Worker.Type.Valid():
		return fmt.Errorf("agent: worker.type %q is not one of %s",
			string(s.Worker.Type), strings.Join(s.Worker.Type.Values(), ", "))
	case s.Worker.Type == WorkerTypeNew && strings.TrimSpace(s.Worker.Reason) == "":
		return errors.New("agent: a new worker says why it starts instead of the warm one, in worker.reason")
	case s.Worker.Type == WorkerTypeResume && s.Worker.Reason != "":
		return errors.New("agent: worker.reason says why a new worker starts, so a resume worker carries none")
	}
	return nil
}
