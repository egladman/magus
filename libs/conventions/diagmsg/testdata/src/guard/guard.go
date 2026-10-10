package guard

// Verdict carries a verdict in Deny and Lead, and its rationale in Why.
type Verdict struct {
	Deny string
	Lead string
	Why  string
}

// Other has a Deny field too, and is not configured.
type Other struct {
	Deny string
}
