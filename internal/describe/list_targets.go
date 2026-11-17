package describe

import (
	"fmt"
	"log/slog"

	"github.com/egladman/magus/types"
)

// ListTargets logs a one-line summary of the targets and prints their paths on stdout,
// one per line: the summary is a log line, so a script reading stdout sees only paths.
func ListTargets(scope string, targets []types.Target, source string) {
	var label string
	switch len(targets) {
	case 0:
		label = "no projects"
	case 1:
		label = targets[0].Path
	default:
		label = fmt.Sprintf("%d projects", len(targets))
	}
	if source != "" {
		label += fmt.Sprintf(" (%s)", source)
	}
	slog.Info("listed targets", slog.String("scope", scope), slog.String("summary", label))
	for _, t := range targets {
		fmt.Println(t.Path)
	}
}
