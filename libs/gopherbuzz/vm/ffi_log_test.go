package vm

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	json "github.com/egladman/magus/libs/gopherbuzz/internal/codec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureFFILog installs a JSON logger at the given level for one test, returning
// the records it collects. It restores the previous logger, so these tests do not
// leak an installed logger into the rest of the package.
func captureFFILog(t *testing.T, level slog.Level) func() []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	prev := ffiLogger.Load()
	SetFFILogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { ffiLogger.Store(prev) })

	return func() []map[string]any {
		var out []map[string]any
		for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var rec map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &rec), "log line is not JSON: %s", line)
			out = append(out, rec)
		}
		return out
	}
}

// TestFFILogSilentByDefault is the contract that matters most: a library emits
// nothing until an embedder asks. Anything that logs unbidden shows up here.
func TestFFILogSilentByDefault(t *testing.T) {
	prev := ffiLogger.Load()
	SetFFILogger(nil)
	t.Cleanup(func() { ffiLogger.Store(prev) })

	assert.False(t, ffiLogEnabled(FFILevelTrace), "no logger installed means nothing is enabled")
	assert.False(t, ffiLogEnabled(slog.LevelError), "not even at ERROR")

	// The instrumented operations must run normally with no logger installed.
	size, align, offsets, err := StructLayout([]string{"int", "char", "double"})
	require.NoError(t, err)
	assert.Positive(t, size)
	assert.Positive(t, align)
	assert.Len(t, offsets, 3)

	addr, err := AllocFFI(16)
	require.NoError(t, err)
	require.NoError(t, FreeFFI(addr))
}

// TestFFILogNothingAtInfo pins the level discipline. Choosing what an operator sees
// is the host's call, so this library must stay below INFO everywhere; a host that
// installs an INFO logger should get an empty stream no matter what FFI does.
func TestFFILogNothingAtInfo(t *testing.T) {
	records := captureFFILog(t, slog.LevelInfo)

	_, _, _, err := StructLayout([]string{"int", "double"})
	require.NoError(t, err)
	_, _, _, err = UnionLayout([]string{"int", "double"})
	require.NoError(t, err)
	addr, err := AllocFFI(32)
	require.NoError(t, err)
	require.NoError(t, FreeFFI(addr))

	assert.Empty(t, records(), "the FFI boundary must emit nothing at INFO or above")
}

func TestFFILogStructLayout(t *testing.T) {
	records := captureFFILog(t, FFILevelTrace)

	size, align, offsets, err := StructLayout([]string{"char", "int", "char"})
	require.NoError(t, err)

	recs := records()
	require.Len(t, recs, 1, "one record per layout")
	rec := recs[0]
	assert.Equal(t, "ffi struct layout", rec["msg"], "the message is a constant, with values in attrs")
	assert.EqualValues(t, size, rec["size"])
	assert.EqualValues(t, align, rec["align"])

	// The offsets are the reason this record exists: a wrong one marshals plausible
	// garbage rather than raising, so it must be in the record and it must match.
	logged := rec["offsets"].([]any)
	require.Len(t, logged, len(offsets))
	for i, want := range offsets {
		assert.EqualValues(t, want, logged[i], "offset %d", i)
	}
	assert.Equal(t, []any{"char", "int", "char"}, rec["fields"])
}

func TestFFILogUnionLayoutIsDistinguishable(t *testing.T) {
	records := captureFFILog(t, FFILevelTrace)

	_, _, _, err := UnionLayout([]string{"int", "double"})
	require.NoError(t, err)

	recs := records()
	require.Len(t, recs, 1)
	assert.Equal(t, "ffi union layout", recs[0]["msg"],
		"a union must not be reported as a struct: picking the wrong rule is the bug this catches")
}

func TestFFILogAllocFreeLifecycle(t *testing.T) {
	records := captureFFILog(t, FFILevelTrace)

	addr, err := AllocFFI(64)
	require.NoError(t, err)
	require.NoError(t, FreeFFI(addr))

	recs := records()
	require.Len(t, recs, 2, "one record for the alloc, one for the free")

	assert.Equal(t, "ffi alloc", recs[0]["msg"])
	assert.EqualValues(t, 64, recs[0]["size"])
	assert.EqualValues(t, addr, recs[0]["addr"])

	assert.Equal(t, "ffi free", recs[1]["msg"])
	assert.EqualValues(t, 64, recs[1]["size"], "the free reports the block's size, so a leak hunt can pair them")
	assert.EqualValues(t, addr, recs[1]["addr"])

	// live is what makes a leak visible without pairing every record by hand.
	assert.EqualValues(t, recs[0]["live"].(float64)-1, recs[1]["live"])
}

// TestFFILogLayoutIsBelowDebug pins that the per-operation records sit under DEBUG.
// A host that turns on debug logging is asking about zdef calls, not about every
// struct offset in a marshalling loop.
func TestFFILogLayoutIsBelowDebug(t *testing.T) {
	records := captureFFILog(t, slog.LevelDebug)

	_, _, _, err := StructLayout([]string{"int", "int"})
	require.NoError(t, err)
	addr, err := AllocFFI(8)
	require.NoError(t, err)
	require.NoError(t, FreeFFI(addr))

	assert.Empty(t, records(), "layout and lifecycle records belong at TRACE, below DEBUG")
	assert.Less(t, FFILevelTrace, slog.LevelDebug)
}

// TestFFILogFailedLayoutIsNotReported pins that an error path stays silent: the
// error is returned to the caller, and logging it here would report it twice.
func TestFFILogFailedLayoutIsNotReported(t *testing.T) {
	records := captureFFILog(t, FFILevelTrace)

	_, _, _, err := StructLayout([]string{"int", "struct nope"})
	require.Error(t, err, "an unknown C type must still be an error")

	assert.Empty(t, records(), "a returned error is not also a log record")
}
