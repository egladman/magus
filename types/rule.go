package types

// RuleDoc is one catalogued guard rule: its stable name, which tier it lands on,
// and what it fires on. `magus describe rule` prints it and magus\describe.rule
// returns it.
// The json tags are the -o json and -o template field names, so they follow the
// lowercase convention every other magus output uses rather than the Go spelling.
type RuleDoc struct {
	// Name is the slug a verdict reports and a reader looks up.
	Name string `json:"name"`
	// Decision is the tier: "deny" or "advise". A rule never moves between them without
	// the move being the point of the change, so it is recorded rather than derived.
	Decision string `json:"decision"`
	// Catches says what the rule fires on, in one line, in the reader's terms.
	Catches string `json:"catches"`
	// Why is the reasoning behind the rule, for a reader who wants to disagree with it
	// or to understand why the replacement is better rather than merely different.
	//
	// It is where the rationale the three-line verdict budget displaced lives. Those
	// paragraphs were true and load-bearing and cost more than they returned at the
	// moment of refusal, when the reader is interrupted and wants the command; here they
	// are read by someone who came looking. Empty for a rule whose one line says all of
	// it, which is most of them: a Why that restates Catches is worse than none.
	Why string `json:"why,omitempty"`
}
