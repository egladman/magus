package types

import "fmt"

// BrokerPolicy is what a run does about the broker: the per-user process that holds this
// host's capacity (slots and declared memory) and the services every magus on it shares.
//
// UnmarshalText is the only way from a name to a policy, and it refuses an unknown one,
// so yaml, the environment and flags all stop a misspelling where it enters. The zero
// value means unset and behaves as BrokerBestEffort.
type BrokerPolicy string

const (
	// BrokerRequired refuses to start a step when no broker answers.
	BrokerRequired BrokerPolicy = "required"
	// BrokerBestEffort asks the broker when one answers and runs unarbitrated, saying so
	// once, when none does. The default.
	BrokerBestEffort BrokerPolicy = "best-effort"
	// BrokerOff never contacts or starts a broker; a run hosts its services itself for its
	// own life.
	BrokerOff BrokerPolicy = "off"
)

// Values lists the policies a caller may choose, excluding the zero value.
func (p BrokerPolicy) Values() []string {
	return []string{string(BrokerRequired), string(BrokerBestEffort), string(BrokerOff)}
}

// Valid reports whether p is a declared policy or unset.
func (p BrokerPolicy) Valid() bool {
	switch p {
	case "", BrokerRequired, BrokerBestEffort, BrokerOff:
		return true
	}
	return false
}

// Resolved is p with the zero value replaced by the policy it behaves as.
func (p BrokerPolicy) Resolved() BrokerPolicy {
	if p == "" {
		return BrokerBestEffort
	}
	return p
}

// String renders p, naming the zero value by the policy it behaves as.
func (p BrokerPolicy) String() string { return string(p.Resolved()) }

// MarshalText writes the name as configured, so an unset policy stays unset.
func (p BrokerPolicy) MarshalText() ([]byte, error) { return []byte(p), nil }

// UnmarshalText sets p from a name, refusing one outside Values.
func (p *BrokerPolicy) UnmarshalText(text []byte) error {
	v := BrokerPolicy(text)
	if !v.Valid() {
		return fmt.Errorf("unknown broker policy %q (want one of %v)", text, v.Values())
	}
	*p = v
	return nil
}
