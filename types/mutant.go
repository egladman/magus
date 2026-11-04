package types

import (
	"fmt"

	"github.com/egladman/magus/internal/json"
)

// MutantOutcome is what the tests did to a mutant. The zero value is unset.
type MutantOutcome string

const (
	// MutantKilled is a mutant a test failed on.
	MutantKilled MutantOutcome = "killed"
	// MutantSurvived is a mutant every test passed on.
	MutantSurvived MutantOutcome = "survived"
	// MutantExcepted is a survivor a `magus:mutant` marker excuses.
	MutantExcepted MutantOutcome = "excepted"
)

// Mutant is one token swap in a touched function and what the tests made of it.
type Mutant struct {
	ID       string
	Symbol   string
	Path     string
	Operator string
	Find     string
	Replace  string
	Line     int
	Col      int
	Outcome  MutantOutcome
	Seconds  float64
	// Reason is the marker's reason when the mutant is excepted.
	Reason string
}

// Mutants is the mutation answer for a change.
type Mutants struct {
	Definition string
	Base       string
	Mutants    []Mutant
	Gaps       []FunctionGap
	// Unused are `magus:mutant` markers that match no surviving mutant or
	// carry no reason.
	Unused []Finding
}

// MutantRun is one mutants.jsonl entry: what one mutant did under one scoped
// test run. The last entry per Key is the answer.
type MutantRun struct {
	// Key is sha256(ID | the scoped test op's cache key), so a changed test
	// or tool misses.
	Key string
	ID  string
	// Outcome is killed or survived; an exception is read from markers and
	// never stored.
	Outcome MutantOutcome
	Seconds float64
	// Checkout names the checkout that produced the entry.
	Checkout string
}

type mutantRunJSON struct {
	Key      string        `json:"key"`
	ID       string        `json:"id"`
	Outcome  MutantOutcome `json:"outcome"`
	Seconds  float64       `json:"seconds"`
	Checkout string        `json:"checkout"`
}

func (r mutantRunJSON) check() error {
	switch {
	case r.Key == "" || r.ID == "" || r.Checkout == "":
		return fmt.Errorf("a mutant run names its key, id and checkout; got key %q, id %q, checkout %q", r.Key, r.ID, r.Checkout)
	case r.Outcome != MutantKilled && r.Outcome != MutantSurvived:
		return fmt.Errorf("a mutant run records killed or survived, not %q", r.Outcome)
	}
	return nil
}

// MarshalJSON writes one JSON object and refuses an entry UnmarshalJSON
// would refuse.
func (r MutantRun) MarshalJSON() ([]byte, error) {
	raw := mutantRunJSON{Key: r.Key, ID: r.ID, Outcome: r.Outcome, Seconds: r.Seconds, Checkout: r.Checkout}
	if err := raw.check(); err != nil {
		return nil, err
	}
	return json.Marshal(raw)
}

// UnmarshalJSON reads an entry MarshalJSON wrote. It refuses one with no key,
// id or checkout, or an outcome other than killed or survived.
func (r *MutantRun) UnmarshalJSON(b []byte) error {
	var raw mutantRunJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := raw.check(); err != nil {
		return err
	}
	*r = MutantRun{Key: raw.Key, ID: raw.ID, Outcome: raw.Outcome, Seconds: raw.Seconds, Checkout: raw.Checkout}
	return nil
}
