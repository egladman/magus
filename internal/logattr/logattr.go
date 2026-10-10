// Package logattr names the attributes magus attaches to its slog records.
package logattr

import "log/slog"

// Component is the attribute key naming the part of magus that logged a record,
// such as "knowledge" or "broker". The pretty handler renders it as a leading
// "name: " before the message; the text and JSON handlers render it as an
// ordinary attribute.
const Component = "component"

// For returns the default logger with [Component] set to name. It reads
// [slog.Default] on every call, so a logger taken before the CLI installs its
// handler never pins the old one; call it at the log site rather than holding
// the result in a package variable.
func For(name string) *slog.Logger {
	return slog.Default().With(Component, name)
}
