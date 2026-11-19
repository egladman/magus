// Package library is no main package, so its flags are its own business.
package library

import "flag"

// Bind registers the library's flags on fs.
func Bind(fs *flag.FlagSet) *string { return fs.String("out", "", "where") }
