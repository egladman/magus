package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/stretchr/testify/require"
)

// TestTermNotifyEnumResolvesThroughItsOwnNamespace pins that reusing LogLevel for
// term.notify's severity does not collide with the log module's copy: both are
// exported, but each lives behind its own import path.
func TestTermNotifyEnumResolvesThroughItsOwnNamespace(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "term";
import "log";
export fun check(ctx: magus\Context, args: [str]) > void !> any {
    term\notify("built", level: term\LogLevel.warn, ttl_ms: 10);
    term\notify("plain");
    log\info("also logged");
}`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr, "term.notify with a LogLevel case")
}
