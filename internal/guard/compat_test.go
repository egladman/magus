package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestAddedMarkers(t *testing.T) {
	t.Parallel()
	markers := []string{"compat(until:", "Deprecated:"}
	for _, tc := range []struct {
		name   string
		fields writeFields
		want   []string
	}{
		{"an edit adding one", writeFields{OldText: "func Old() {}", NewText: "// Deprecated: use New.\nfunc Old() {}"}, []string{"Deprecated:"}},
		{"an edit keeping one", writeFields{OldText: "// Deprecated: x\nfunc a() {}", NewText: "// Deprecated: x\nfunc b() {}"}, nil},
		{"an edit removing one", writeFields{OldText: "// compat(until: x): y\nz()", NewText: "z()"}, nil},
		{"a second copy is an addition", writeFields{OldText: "// Deprecated: a", NewText: "// Deprecated: a\n// Deprecated: b"}, []string{"Deprecated:"}},
		{"a whole-file write", writeFields{Content: "// compat(until: no reader left): old\n// Deprecated: gone"}, []string{"compat(until:", "Deprecated:"}},
		{"each edit of a list", writeFields{Edits: []textEdit{
			{OldText: "a()", NewText: "a2()"},
			{OldText: "b()", NewText: "// compat(until: x): kept\nb()"},
		}}, []string{"compat(until:"}},
		{"moved between edits", writeFields{Edits: []textEdit{
			{OldText: "// Deprecated: a\nf()", NewText: "f()"},
			{OldText: "g()", NewText: "// Deprecated: a\ng()"},
		}}, nil},
		{"split across two texts", writeFields{OldText: "x", NewText: "Deprec", Edits: []textEdit{{OldText: "y", NewText: "ated:"}}}, nil},
		{"nothing", writeFields{OldText: "a", NewText: "b"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, addedMarkers(tc.fields, markers))
		})
	}
	assert.Equal(t, []string{"legacy:"}, addedMarkers(writeFields{NewText: "// legacy: x // Deprecated: y"}, []string{"legacy:"}),
		"the markers are the setting's, not a fixed list")
}

// compatFleet is the fleet fixture with a file inside lease-b's write paths.
func compatFleet(t *testing.T) (context.Context, string, string) {
	t.Helper()
	ctx, root := fleetFixture(t, fixture.FleetLeases()...)
	file := filepath.Join(root, "cmd", "magus", "old.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(file, []byte("package magus\n\n// Deprecated: use New.\nfunc Old() {}\n"), 0o644))
	return ctx, root, file
}

func attentionRows(t *testing.T, root string) []sessions.AttentionRequest {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	return sessions.AttentionQueue(fold)
}

var addsCompat = writeFields{OldText: "func Old() {}", NewText: "// compat(until: no caller passes v1): kept\nfunc Old() {}"}

// A worker's write its lease allows is advised, never refused, and the root reads one row
// per lease stating the facts however many markers the worker adds.
func TestCompatMarkerAdvisesAWorkerAndFilesOneRow(t *testing.T) {
	testkit.Isolate(t)
	ctx, root, file := compatFleet(t)

	got := gradeLeasedEdit(ctx, Dependencies{}, "lease-b", file, addsCompat)
	require.Equal(t, decisionAdvise, got.Decision)
	assert.Equal(t, hint.MarkerKind(denyRuleCompatMarker), got.Kind)
	assert.Contains(t, got.Context, "adds `compat(until:` to cmd/magus/old.go")
	assert.Contains(t, got.Context, "unresolved_risks")
	assert.Contains(t, got.Why, "never refused")

	again := gradeLeasedEdit(ctx, Dependencies{}, "lease-b", file, writeFields{Content: "package magus\n\n// Deprecated: use New.\n// Deprecated: twice.\nfunc Old() {}\n"})
	assert.Equal(t, decisionAdvise, again.Decision, "every addition is advised")
	assert.Contains(t, again.Context, "adds `Deprecated:`")

	rows := attentionRows(t, root)
	require.Len(t, rows, 1, "one row per lease")
	assert.Equal(t, "lease-b", rows[0].Lease)
	assert.Equal(t, "job lease-b added `compat(until:` to cmd/magus/old.go. It ran on a model magus has no record of.", rows[0].Message)
}

func TestCompatMarkerStaysQuiet(t *testing.T) {
	testkit.Isolate(t)
	ctx, root, file := compatFleet(t)

	t.Run("a whole-file write keeping the markers on disk", func(t *testing.T) {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Empty(t, gradeLeasedEdit(ctx, Dependencies{}, "lease-b", file, writeFields{Content: string(body) + "\nfunc New() {}\n"}).Decision)
	})
	t.Run("no lease: a person or the root", func(t *testing.T) {
		assert.Empty(t, gradeLeasedEdit(ctx, Dependencies{}, "", filepath.Join(root, "README.md"), addsCompat).Decision)
		got := gradeLeasedEdit(ctx, Dependencies{}, "", file, addsCompat)
		assert.NotEqual(t, hint.MarkerKind(denyRuleCompatMarker), got.Kind, "the leased-path advisory speaks, not this one")
	})
	t.Run("turned off", func(t *testing.T) {
		assert.Empty(t, gradeLeasedEdit(ctx, withSetting(string(denyRuleCompatMarker), builtin.Off), "lease-b", file, addsCompat).Decision)
	})
	t.Run("a write the boundary refuses keeps its deny", func(t *testing.T) {
		got := gradeLeasedEdit(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal", "ledger", "store.go"), addsCompat)
		assert.Equal(t, decisionDeny, got.Decision)
	})
	t.Run("a dry run advises and files nothing", func(t *testing.T) {
		dry, cancel := withDryRunDeadline(ctx)
		defer cancel()
		assert.Equal(t, decisionAdvise, gradeLeasedEdit(dry, Dependencies{}, "lease-b", file, addsCompat).Decision)
		assert.False(t, stallGate(hookLocation(ctx, Dependencies{}).cacheDir, "lease-b").AlreadyFired(hint.MarkerKind(denyRuleCompatMarker)))
	})
	assert.Empty(t, attentionRows(t, root))
}

// Through Judge, the advisory is the verdict's and is named after the rule.
func TestCompatMarkerThroughJudge(t *testing.T) {
	testkit.Isolate(t)
	ctx, root, file := compatFleet(t)
	edit := func(lease string, dry bool) types.GuardJudgment {
		return Judge(ctx, Dependencies{}, Request{Host: "claude-code", Lease: lease, DryRun: dry, Input: hookJSON(t, map[string]any{
			"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Edit", "cwd": root,
			"tool_input": map[string]any{"file_path": file, "old_string": addsCompat.OldText, "new_string": addsCompat.NewText},
		})})
	}

	assert.Equal(t, verdictWithRule(decisionAdvise, string(denyRuleCompatMarker)), unworded(edit("lease-b", true)))
	assert.Empty(t, attentionRows(t, root), "a dry run writes nothing")

	assert.Equal(t, verdictWithRule(decisionAdvise, string(denyRuleCompatMarker)), unworded(edit("lease-b", false)))
	assert.Len(t, attentionRows(t, root), 1)
}
