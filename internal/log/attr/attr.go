// Package attr names the attributes magus attaches to its slog records.
package attr

import (
	"log/slog"
	"time"
)

const (
	// ComponentKey names the part of magus that logged a record, such as "knowledge" or
	// "broker". The pretty handler renders it as a leading "name: " before the message;
	// the text and JSON handlers render it as an ordinary attribute.
	ComponentKey = "component"
	// WhyKey carries the reasoning behind a record. See [Why].
	WhyKey = "why"
	// ElapsedKey marks a record as a progress note for a wait. See [Elapsed].
	ElapsedKey = "elapsed"
)

// Component attaches the name of the part of magus that logged a record.
func Component(name string) slog.Attr { return slog.String(ComponentKey, name) }

// Why attaches the reasoning behind a record: what a check protects, or why the
// obvious alternative is wrong. An agent's display drops it unless verbose.
func Why(text string) slog.Attr { return slog.String(WhyKey, text) }

// Elapsed marks a record as a progress note for a wait that has lasted d, as a
// duration value. An agent's display drops the record while d is under a minute.
func Elapsed(d time.Duration) slog.Attr { return slog.Duration(ElapsedKey, d) }
