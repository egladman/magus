package job

import (
	"bytes"
	"fmt"
	"slices"

	"github.com/egladman/magus/libs/codec"
)

// Check is one check as its host wrote it. The store keeps it, compares it and renders
// it through the host's [CheckSpec], and never reads what it says. The zero Check
// declares none.
//
// A Check holds the codec's canonical JSON, so the line on disk does not depend on which
// JSON build wrote it, and nothing outside the package can change it once made.
type Check struct{ raw []byte }

// NewCheck is v, the host's value, as a Check. A v that encodes as JSON null is the zero
// Check.
func NewCheck(v any) (Check, error) {
	b, err := codec.Marshal(v)
	if err != nil {
		return Check{}, fmt.Errorf("job: encode a check: %w", err)
	}
	return storedCheck(b), nil
}

// storedCheck is b, canonical JSON the package already holds, as a Check.
func storedCheck(b []byte) Check {
	if bytes.Equal(b, []byte("null")) {
		return Check{}
	}
	return Check{raw: b}
}

// IsZero reports whether c declares no check.
func (c Check) IsZero() bool { return len(c.raw) == 0 }

// Decode reads c into v strictly: a member v does not declare is refused. The zero Check
// is [ErrNoCheck].
func (c Check) Decode(v any) error {
	if c.IsZero() {
		return ErrNoCheck
	}
	return codec.UnmarshalStrict(c.raw, v)
}

// String is c's JSON, for a log line, and "" for the zero Check. A message names a check
// through [CheckSpec.Render] instead.
func (c Check) String() string { return string(c.raw) }

// MarshalJSON writes c's JSON, and null for the zero Check.
func (c Check) MarshalJSON() ([]byte, error) {
	if c.IsZero() {
		return []byte("null"), nil
	}
	return slices.Clone(c.raw), nil
}

// UnmarshalJSON reads one JSON value as a Check, keeping it in the codec's canonical form.
// null and an empty document give the zero Check.
func (c *Check) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		*c = Check{}
		return nil
	}
	canonical, err := codec.Marshal(codec.RawMessage(trimmed))
	if err != nil {
		return fmt.Errorf("job: read a check: %w", err)
	}
	*c = storedCheck(canonical)
	return nil
}
