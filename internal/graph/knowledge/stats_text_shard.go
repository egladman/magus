package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/log/attr"
)

// GraphBuildShardFailure fails a build on any left-out shard but partial coverage.
//
// A narrowed test leaves coverage incomplete without invalidating the refreshed symbols.
// Keep that gap visible, while a corrupt profile or any other shard failure still fails.
func GraphBuildShardFailure(ctx context.Context, omitted []string) error {
	var failed []string
	for _, message := range omitted {
		if strings.HasPrefix(message, "@coverage: coverage profile omits ") {
			interactive.Hint(ctx, "graph refreshed without complete coverage", attr.Error(errors.New(message)))
			continue
		}
		failed = append(failed, message)
	}
	if len(failed) > 0 {
		return fmt.Errorf("magus graph build: left out of the graph: %s", strings.Join(failed, "; "))
	}
	return nil
}
