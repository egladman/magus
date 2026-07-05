package playground

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tourFile reads a website/tour fixture relative to the package dir. The fixtures
// are the acceptance inputs, so the console must render the exact bytes the docs ship.
func tourFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "website", "tour", name))
	require.NoError(t, err, "read tour fixture %s", name)
	return string(b)
}

// TestConsole_spellServiceAndWard drives the terminal end to end on the two spell
// fixtures: `ls` lists the spell's op, `run serve` on the clean fixture renders the
// service hint, and `run serve` on the ward fixture renders the MGS5002 line as an
// error. This proves the console distinguishes a spell op and a ward from a plain
// magusfile op.
func TestConsole_spellServiceAndWard(t *testing.T) {
	ctx := context.Background()

	svc := NewConsole(testInfo)
	ok, status := svc.SetSource(ctx, tourFile(t, "09-services.buzz"))
	require.True(t, ok, "09-services did not load: %s", status)
	assert.Contains(t, status, "op", "a spell buffer's status counts ops, not targets")
	assert.Contains(t, joinHTML(svc.Exec(ctx, "ls").Lines), "serve", "ls lists the spell op")

	runOut := joinHTML(svc.Exec(ctx, "run serve").Lines)
	assert.Contains(t, runOut, "service", "run serve shows the service op with its kind hint")
	assert.Contains(t, runOut, "supervised", "the service hint reads supervised, shared")

	ward := NewConsole(testInfo)
	ok, _ = ward.SetSource(ctx, tourFile(t, "10-wards.buzz"))
	require.True(t, ok, "10-wards did not load")
	wardOut := joinHTML(ward.Exec(ctx, "run serve").Lines)
	assert.Contains(t, wardOut, "MGS5002", "run serve on the ward fixture surfaces the code")
}
