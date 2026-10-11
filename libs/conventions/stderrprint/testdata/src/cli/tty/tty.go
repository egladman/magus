package tty

import "io"

// Probe stands in for a terminal probe the display owns.
func Probe(w io.Writer) { _ = w }
