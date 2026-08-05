package bindings

import (
	"strings"
	"testing"

	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAttachDecodedJSON pins the ordering that is the whole design of a
// returns_json op: the exit code decides whether stdout is even a candidate for
// decoding, so a failing tool reports as a failing tool rather than as a parse
// error against whatever it printed on the way down.
func TestAttachDecodedJSON(t *testing.T) {
	t.Run("exit 0 and valid JSON yields a Boxed json member", func(t *testing.T) {
		rec := vm.NewMap()
		res := run.ExecResult{Code: 0, Stdout: `{"Module":{"Path":"example.com/m"},"Go":"1.25"}`}
		require.NoError(t, attachDecodedJSON(rec, "go-mod-edit-json", res))

		boxed, ok := rec.MapGet("json")
		require.True(t, ok, "json member must be set")
		// Boxed is the same shape serialize\jsonDecode returns, so it answers to q()
		// and the typed accessors rather than being a bare map.
		q, ok := boxed.MapGet("q")
		require.True(t, ok, "json member must be a Boxed (no q accessor found)")
		assert.True(t, q.IsFun(), "q must be callable")
	})

	t.Run("non-zero exit leaves json unset and reports no error", func(t *testing.T) {
		rec := vm.NewMap()
		// A tool that dies mid-run writes an error to stdout. Decoding it would report
		// a parse failure where the real event is the non-zero exit.
		res := run.ExecResult{Code: 1, Stdout: "go: cannot load module\n"}
		require.NoError(t, attachDecodedJSON(rec, "go-mod-edit-json", res),
			"a failed tool is the caller's existing code/stderr story, not a decode error")
		_, ok := rec.MapGet("json")
		assert.False(t, ok, "json must NOT be set for a failed run")
	})

	t.Run("exit 0 with non-JSON names the op and quotes the head", func(t *testing.T) {
		rec := vm.NewMap()
		res := run.ExecResult{Code: 0, Stdout: "not json at all"}
		err := attachDecodedJSON(rec, "go-mod-edit-json", res)
		require.Error(t, err, "declaring returns_json and emitting non-JSON is an authoring bug")
		assert.Contains(t, err.Error(), "go-mod-edit-json", "the error must name the op")
		assert.Contains(t, err.Error(), "not json at all", "the error must quote what it got")
	})
}

// TestHeadOf pins that a huge stdout is truncated in the error rather than pasting a
// whole build log into the terminal.
func TestHeadOf(t *testing.T) {
	assert.Equal(t, "abc", headOf("abc", 10))
	got := headOf(strings.Repeat("x", 500), 10)
	assert.Equal(t, strings.Repeat("x", 10)+"...", got)
}
