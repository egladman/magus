package file

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

type counter struct {
	N int `json:"n"`
}

// N processes each increment one counter M times. A read-modify-write that loses an
// update anywhere leaves the count short of N*M, and every store built on Doc inherits
// the guarantee this pins.
func TestDocUpdateAcrossProcesses(t *testing.T) {
	const procs, each = 4, 25
	path := filepath.Join(t.TempDir(), "counter.json")
	cmds := make([]*exec.Cmd, procs)
	for i := range cmds {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDocHelper$")
		cmd.Env = append(os.Environ(), "DOCTEST_PATH="+path, "DOCTEST_EACH="+strconv.Itoa(each))
		require.NoError(t, cmd.Start())
		cmds[i] = cmd
	}
	for _, cmd := range cmds {
		require.NoError(t, cmd.Wait())
	}
	got, err := Doc[counter]{Path: path}.Load()
	require.NoError(t, err)
	assert.Equal(t, counter{N: procs * each}, got)
}

func TestDocHelper(t *testing.T) {
	path := os.Getenv("DOCTEST_PATH")
	if path == "" {
		t.Skip("helper process for TestDocUpdateAcrossProcesses")
	}
	each, err := strconv.Atoi(os.Getenv("DOCTEST_EACH"))
	require.NoError(t, err)
	d := Doc[counter]{Path: path}
	for range each {
		require.NoError(t, d.Update(t.Context(), func(c *counter) error {
			c.N++
			return nil
		}))
	}
}

type olderRecord struct {
	types.Schema
	Name string `json:"name"`
}

// A writer that predates a member still carries it back out, which is what keeps an
// older magus from stripping what a newer one stored.
func TestDocUpdateKeepsUnknownMembers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"schema_version":9,"name":"a","added_later":{"x":1}}`), 0o644))

	require.NoError(t, Doc[olderRecord]{Path: path}.Update(t.Context(), func(r *olderRecord) error {
		r.Name = "b"
		return nil
	}))

	var got map[string]any
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, map[string]any{"schema_version": float64(9), "name": "b", "added_later": map[string]any{"x": float64(1)}}, got)
}

func TestDocLoadAbsentIsZero(t *testing.T) {
	got, err := Doc[counter]{Path: filepath.Join(t.TempDir(), "absent.json")}.Load()
	require.NoError(t, err)
	assert.Equal(t, counter{}, got)
}

func TestDocUpdateWritesNothingOnSkipOrFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.json")
	d := Doc[counter]{Path: path}

	require.NoError(t, d.Update(t.Context(), func(c *counter) error {
		c.N = 1
		return SkipWrite
	}))
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "SkipWrite writes nothing")

	boom := errors.New("boom")
	require.ErrorIs(t, d.Update(t.Context(), func(c *counter) error {
		c.N = 2
		return boom
	}), boom)
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "a failing fn writes nothing")
}

// A Decode refusal stops the update before fn runs, so a store can refuse a document it
// cannot act on without the rewrite destroying it.
func TestDocUpdateRefusedByDecode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"n":7}`), 0o644))
	d := Doc[counter]{Path: path, Decode: func(b []byte, c *counter) error {
		return fmt.Errorf("refused %s", b)
	}}
	ran := false
	err := d.Update(t.Context(), func(*counter) error {
		ran = true
		return nil
	})
	require.EqualError(t, err, `refused {"n":7}`)
	assert.False(t, ran)
}
