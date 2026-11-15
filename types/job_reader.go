package types

// JobReader reads the job rows the guard's lease rules judge a leased call
// against (ADR 0045). The lease on a caller's context ([WithJobLease]) names
// one row, and the rules read the rest for its parent, children and siblings.
// magus's job store is the default; a Go program embedding magus brings its own
// rows with magus.WithJobReader.
type JobReader interface {
	// List returns every row, ended ones included, or why none could be read.
	List() ([]Job, error)
}
