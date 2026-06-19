package cache

import (
	"log/slog"
)

// LogScope emits a [scope] header through the cache logger so all
// output formats (pretty/text/JSON) receive the same event.
func (c *Cache) LogScope(label, source string) {
	c.log.Info(
		"cache.scope",
		slog.String("label", label),
		slog.String("source", source),
	)
}
