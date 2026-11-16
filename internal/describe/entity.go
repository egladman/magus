package describe

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/clikit/exit"
)

// FilterByName returns a single-element slice holding the item whose name equals
// name, or nil if none match. Used to turn a list view into a one-entity detail.
func FilterByName[T any](items []T, name string, nameOf func(T) string) []T {
	for _, it := range items {
		if nameOf(it) == name {
			return []T{it}
		}
	}
	return nil
}

// CollectNames projects each item to its name, for typo suggestions.
func CollectNames[T any](items []T, nameOf func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = nameOf(it)
	}
	return out
}

// UnknownEntity prints a "no such <kind>" message (with a nearest-match hint) and
// returns the already-printed sentinel.
func UnknownEntity(kind, name string, all []string) error {
	msg := fmt.Sprintf("unknown %s %q", kind, name)
	if sug := hint.Nearest(name, all); sug != "" {
		msg += fmt.Sprintf("; did you mean %q?", sug)
	}
	slog.ErrorContext(context.Background(), msg, attr.Notice(""), attr.Component("magus describe "+kind))
	return exit.Reported(exit.Usage)
}

// FirstLine returns the first line of s, for compact one-line rendering of a
// possibly multi-line doc comment.
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
