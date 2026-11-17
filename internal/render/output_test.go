package render

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDestinationWithoutTeeIsTheWriterItWasGiven(t *testing.T) {
	tee := &Tee{}
	var out bytes.Buffer
	w, closeTee, err := tee.Destination(&out, false)
	require.NoError(t, err)
	defer func() { _ = closeTee() }()
	assert.Same(t, &out, w)
}

func TestDestinationTeeWritesBothAndReplacesPerInvocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tee.json")
	tee := &Tee{Path: path}
	emit := func(s string) string {
		var out bytes.Buffer
		w, closeTee, err := tee.Destination(&out, false)
		require.NoError(t, err)
		_, err = io.WriteString(w, s)
		require.NoError(t, err)
		require.NoError(t, closeTee())
		return out.String()
	}

	assert.Equal(t, "a", emit("a"))
	assert.Equal(t, "b", emit("b"), "a second destination in one invocation appends")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "ab", string(got))

	tee.Reset()
	emit("c")
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "c", string(got), "a new invocation replaces the capture")
}

func TestDestinationSilentTeeKeepsStdoutEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tee.json")
	tee := &Tee{Path: path}
	var out bytes.Buffer
	w, closeTee, err := tee.Destination(&out, true)
	require.NoError(t, err)
	_, err = io.WriteString(w, "rec")
	require.NoError(t, err)
	require.NoError(t, closeTee())
	assert.Empty(t, out.String())
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "rec", string(got))
}

func TestDestinationBadTeePathIsNamed(t *testing.T) {
	tee := &Tee{Path: filepath.Join(t.TempDir(), "missing", "tee")}
	_, _, err := tee.Destination(io.Discard, false)
	require.ErrorContains(t, err, "--tee")
}

func TestLazyFileOpensOnFirstWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tee")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))
	l := NewLazyFile(&Tee{Path: path})
	require.NoError(t, l.Close())
	got, _ := os.ReadFile(path)
	assert.Equal(t, "old", string(got), "an unused lazy file leaves the contents alone")

	_, err := l.Write([]byte("x"))
	require.NoError(t, err)
	_, err = l.Write([]byte("y"))
	require.NoError(t, err)
	require.NoError(t, l.Close())
	got, _ = os.ReadFile(path)
	assert.Equal(t, "xy", string(got))
}
