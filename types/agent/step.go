package agent

import "time"

// StepAPIVersion is the version of the Step record, carried as api_version.
// A Step that names another version is refused.
const StepAPIVersion = "magus.step/v1alpha1"

// Step is one thing an agent is about to do, as data. Kind is a name its
// program declared with a StepSpec, and Input holds the step's own
// arguments. The judge reads them as sent and never restates the step.
// Dir is required when the spec reads paths or a command. The judge does
// not read ID or At yet.
type Step struct {
	APIVersion string         `json:"api_version"`
	ID         string         `json:"id,omitempty"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name,omitempty"`
	Input      map[string]any `json:"input,omitempty"`
	Session    string         `json:"session,omitempty"`
	Agent      string         `json:"agent,omitempty"`
	Dir        string         `json:"dir,omitempty"`
	At         time.Time      `json:"at,omitzero"`
}

// StepSpec declares one kind of step before any is judged. An empty Name
// covers every name of the kind. Capabilities are types.ToolCapability
// values. Paths and Command are JSON Pointers (RFC 6901) into a step's
// Input, read by the built-in rules only. A step whose Input does not
// match InputSchema is refused before any rule runs.
type StepSpec struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name,omitempty"`
	Capabilities []string `json:"capabilities"`
	Paths        []string `json:"paths,omitempty"`
	Command      string   `json:"command,omitempty"`
	// SpawnDescriptionField is where a spawn spec's description sits.
	SpawnDescriptionField string `json:"spawn_description_field,omitempty"`
	InputSchema           []byte `json:"input_schema,omitempty"`
}

// StepRequest is what a step rule reads: the step, its spec's capabilities,
// and the paths and command read from its input. Lease is the job the step
// acts under, empty when it names none.
type StepRequest struct {
	Step         Step
	Capabilities []string
	Paths        []string
	Command      string
	Lease        string
}
