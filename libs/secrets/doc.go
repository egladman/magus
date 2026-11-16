// Package secrets resolves credential references through a caller's provider, remembers
// the values it handed out so they can be masked from everything the caller records, and
// forwards a credential to a child process without handing the child the value.
//
// A value is a secret because it was read through a [Resolver], never because its name
// looked credential-shaped. That removes the "forgot to classify it" failure mode and
// makes redaction exact over the bytes the caller produced.
//
// WHERE a secret comes from is the caller's business: a [Provider] fetches by an opaque
// reference, and the environment is the built-in one. What only this package can do is
// know that a value IS a secret.
//
// State lives on a Resolver the caller creates and carries on the context, never in the
// process. A server that holds one Resolver per workspace keeps resolved plaintext that
// long; a second workspace gets a second Resolver.
//
// The package keeps no process-wide state and writes nowhere on its own: a logger, a
// provider opener and the notice for a value too short to mask all come from options. It
// imports the standard library and golang.org/x/sync.
package secrets
