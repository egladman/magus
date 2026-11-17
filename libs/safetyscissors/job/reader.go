package job

import "context"

// Reader reads the job rows a lease rule judges a leased call against. The lease on a
// caller's context ([WithLease]) names one row, and a rule reads the rest for its
// parent, children and siblings. An implementation answers from its own store.
type Reader interface {
	// List returns every row, ended ones included, or why none could be read. It honors
	// ctx's cancellation where its store can.
	List(ctx context.Context) ([]Job, error)
}
