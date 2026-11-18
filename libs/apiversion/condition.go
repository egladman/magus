package apiversion

import "time"

// ConditionStatus is the state a Condition reports.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition mirrors metav1.Condition's members under snake_case keys, so a
// record's status can be a list of them.
type Condition struct {
	Type   string          `json:"type" yaml:"type" buzz:"type"`
	Status ConditionStatus `json:"status" yaml:"status" buzz:"status"`
	// ObservedGeneration is the Metadata.Generation behind the condition.
	ObservedGeneration int64     `json:"observed_generation,omitzero" yaml:"observed_generation,omitempty" buzz:"observedGeneration"`
	LastTransitionTime time.Time `json:"last_transition_time" yaml:"last_transition_time" buzz:"lastTransitionTime"`
	// Reason is a CamelCase machine-readable cause.
	Reason  string `json:"reason" yaml:"reason" buzz:"reason"`
	Message string `json:"message" yaml:"message" buzz:"message"`
}
