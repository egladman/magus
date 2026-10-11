package logs

import (
	"context"
	"fmt"
	"log/slog"
)

const tagged = "broker: drained"

func calls(ctx context.Context, log *slog.Logger, name string) {
	slog.Info("drained; stopping")
	slog.Warn("broker: drained; stopping")                            // want `message-tag: Drop the leading 'broker:' tag`
	slog.InfoContext(ctx, "knowledge: cannot decode symbol index")    // want `message-tag: Drop the leading 'knowledge:' tag`
	slog.ErrorContext(ctx, tagged)                                    // want `message-tag: Drop the leading 'broker:' tag`
	slog.DebugContext(ctx, fmt.Sprintf("magus: %s is waiting", name)) // want `message-tag: Drop the leading 'magus:' tag`
	log.Debug("[AGENT] read " + name)                                 // want `message-tag: Drop the '\[AGENT\]' marker`
	log.WarnContext(ctx, "server: not running", "name", name)         // want `message-tag: Drop the leading 'server:' tag`
	log.With("component", "server").Info("not running")
	slog.Warn("diff session: not open")                      // want `message-tag: Drop the leading 'diff session:' tag`
	slog.InfoContext(ctx, "server check-drift: "+name)       // want `message-tag: Drop the leading 'server check-drift:' tag`
	log.Error(fmt.Sprintf("graph build: %s is stale", name)) // want `message-tag: Drop the leading 'graph build:' tag`
	slog.Warn("cannot open the file: " + name)
	slog.Warn("write failed: " + name)
	slog.Warn("knowledge graph rebuilt: " + name)

	// Only the tag rule judges a log message: this one is long, gives two
	// reasons and names two commands, and none of that is reported.
	slog.Info("refused because the row ended, so nothing runs; run `magus graph build`, then `magus refs X`, and read the rest of this long line")

	// The message argument alone is judged, never an attribute.
	slog.Info("drained", "detail", "broker: drained")
}
