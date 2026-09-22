package types

import "fmt"

// ConcurrencyProfile is a build width relative to the machine rather than a number, so
// one committed magus.yaml means the same thing on a laptop and on a build server.
//
// UnmarshalText is the only way from a name to a profile, and it refuses an unknown one,
// so yaml, the environment and flags all stop a misspelling where it enters. The zero
// value means unset and sizes like Balanced.
type ConcurrencyProfile string

const (
	// ProfileConservative is half the cores: a machine someone is also using.
	ProfileConservative ConcurrencyProfile = "conservative"
	// ProfileBalanced is min(cores, 8), the default.
	ProfileBalanced ConcurrencyProfile = "balanced"
	// ProfileAggressive is every core: a dedicated build machine.
	ProfileAggressive ConcurrencyProfile = "aggressive"
)

// Values lists the profiles a caller may choose, excluding the zero value.
func (p ConcurrencyProfile) Values() []string {
	return []string{string(ProfileConservative), string(ProfileBalanced), string(ProfileAggressive)}
}

// Valid reports whether p is a declared profile or unset.
func (p ConcurrencyProfile) Valid() bool {
	switch p {
	case "", ProfileConservative, ProfileBalanced, ProfileAggressive:
		return true
	}
	return false
}

// String renders p, naming the zero value by the width it gets.
func (p ConcurrencyProfile) String() string {
	if p == "" {
		return string(ProfileBalanced)
	}
	return string(p)
}

// MarshalText writes the name as configured, so an unset profile stays unset.
func (p ConcurrencyProfile) MarshalText() ([]byte, error) { return []byte(p), nil }

// UnmarshalText sets p from a name, refusing one outside Values.
func (p *ConcurrencyProfile) UnmarshalText(text []byte) error {
	v := ConcurrencyProfile(text)
	if !v.Valid() {
		return fmt.Errorf("unknown concurrency profile %q (want one of %v)", text, v.Values())
	}
	*p = v
	return nil
}

// Width returns the slots p grants on a machine with cores CPUs, never fewer than one.
func (p ConcurrencyProfile) Width(cores int) int {
	n := min(cores, 8)
	switch p {
	case ProfileConservative:
		n = cores / 2
	case ProfileAggressive:
		n = cores
	}
	return max(n, 1)
}
