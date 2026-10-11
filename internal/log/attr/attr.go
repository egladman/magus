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
	// NextKey carries the one command that acts on a record. See [Next].
	NextKey = "next"
	// NoticeKey marks a record as a notice to the person running magus. See [Notice].
	NoticeKey = "notice"
	// HintLabel is the [Notice] label of advice. See [Hint].
	HintLabel = "hint"
	// ErrorKey carries the error a record reports. See [Error].
	ErrorKey = "error"
)

// Component attaches the name of the part of magus that logged a record.
func Component(name string) slog.Attr { return slog.String(ComponentKey, name) }

// Why attaches the reasoning behind a record: what a check protects, or why the
// obvious alternative is wrong. A quiet display (-q, -s) drops it unless verbose.
func Why(text string) slog.Attr { return slog.String(WhyKey, text) }

// Elapsed marks a record as a progress note for a wait that has lasted d, as a
// duration value. A quiet display drops the record while d is under a minute.
func Elapsed(d time.Duration) slog.Attr { return slog.Duration(ElapsedKey, d) }

// Next attaches the one command that acts on a record, printed under the message as a
// `next:` block. A quiet display (-q, -s) reduces an info record to this command.
func Next(cmd string) slog.Attr { return slog.String(NextKey, cmd) }

// Notice marks a record as a line addressed to the person running magus, not a log
// entry. The pretty display prints "label: message" with no level glyph, and colors the
// label above info. An empty label prints the component instead.
func Notice(label string) slog.Attr { return slog.String(NoticeKey, label) }

// Error attaches the error a record reports. An error is never a record's message: the
// label names who is speaking and the error names where it failed, so a notice prints
// "label: message: error", and the name once when the error originated in the label's
// own package.
func Error(err error) slog.Attr { return slog.Any(ErrorKey, err) }

// Hint marks a record as advice, printed "hint: message". A quiet display keeps an info
// hint, since a hint is what -s still surfaces.
func Hint() slog.Attr { return Notice(HintLabel) }
