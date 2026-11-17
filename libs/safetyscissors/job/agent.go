package job

import (
	"fmt"
	"strings"
)

// Agent is a job's agent section: what the agent taking the job is asked for.
// A job with none is ordinary work, such as a server's maintenance run.
type Agent struct {
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
func Instructed(instructions string) *Agent {
	return &Agent{Instructions: instructions, Worker: Worker{Type: WorkerTypeResumed}}
}

// Clone is a copy of a that shares nothing with it, nil for nil.
func (a *Agent) Clone() *Agent {
	if a == nil {
		return nil
	}
	c := *a
	return &c
}

// Trimmed is a clone of a with its model and worker reason trimmed of
// space, nil for nil.
func (a *Agent) Trimmed() *Agent {
	c := a.Clone()
	if c != nil {
		c.Model.ID = strings.TrimSpace(c.Model.ID)
		c.Model.Tier = strings.TrimSpace(c.Model.Tier)
		c.Worker.Reason = strings.TrimSpace(c.Worker.Reason)
	}
	return c
}

// RequestedModel is a's model, the zero Model for nil.
func (a *Agent) RequestedModel() Model {
	if a == nil {
		return Model{}
	}
	return a.Model
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
	// WorkerTypeResumed continues the warm worker of the followed job.
	WorkerTypeResumed WorkerType = "resumed"
	// WorkerTypeNew starts a new worker, for the reason the section gives.
	WorkerTypeNew WorkerType = "new"
)

// Validate refuses a section no door stores, as an [*AgentError]: a worker type outside
// the set, a new worker with no reason, or a reason on a resumed worker.
func (a Agent) Validate() error {
	refuse := func(reason string) error { return &AgentError{Reason: reason} }
	switch {
	case a.Worker.Type == "":
		return refuse("the section names no worker.type, one of " + strings.Join(a.Worker.Type.Values(), ", "))
	case !a.Worker.Type.Valid():
		return refuse(fmt.Sprintf("worker.type %q is not one of %s", string(a.Worker.Type), strings.Join(a.Worker.Type.Values(), ", ")))
	case a.Worker.Type == WorkerTypeNew && strings.TrimSpace(a.Worker.Reason) == "":
		return refuse("a new worker says why it starts instead of the warm one, in worker.reason")
	case a.Worker.Type == WorkerTypeResumed && a.Worker.Reason != "":
		return refuse("worker.reason says why a new worker starts, so a resumed worker carries none")
	}
	return nil
}

// AgentError is an agent section [Agent.Validate] refuses, and why.
type AgentError struct{ Reason string }

func (e *AgentError) Error() string { return "agent section: " + e.Reason }
