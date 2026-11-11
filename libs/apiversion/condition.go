package apiversion

import "time"

// ConditionStatus is the state a Condition reports.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition mirrors metav1.Condition, so a record's status can be a list of
// them and stay Kubernetes-conformant.
type Condition struct {
	Type   string          `json:"type" yaml:"type" buzz:"type"`
	Status ConditionStatus `json:"status" yaml:"status" buzz:"status"`
	// ObservedGeneration is the Metadata.Generation behind the condition.
	ObservedGeneration int64     `json:"observedGeneration,omitempty" yaml:"observedGeneration,omitempty" buzz:"observedGeneration"`
	LastTransitionTime time.Time `json:"lastTransitionTime" yaml:"lastTransitionTime" buzz:"lastTransitionTime"`
	// Reason is a CamelCase machine-readable cause.
	Reason  string `json:"reason" yaml:"reason" buzz:"reason"`
	Message string `json:"message" yaml:"message" buzz:"message"`
}
