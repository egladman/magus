//go:build mcp

package mcp

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOutputReader is a hand-built outputReader: it returns canned bytes and a
// descriptor, or a chosen error, so outputTool.Invoke is unit-testable without a
// real workspace cache.
type fakeOutputReader struct {
	data []byte
	desc cache.OutputDescriptor
	err  error
}

func (f fakeOutputReader) OutputByRef(string) ([]byte, cache.OutputDescriptor, error) {
	return f.data, f.desc, f.err
}

// TestOutputToolRequiredParam covers the guard that returns before any store access.
func TestOutputToolRequiredParam(t *testing.T) {
	_, err := (&outputTool{}).Invoke(context.Background(), types.InvokeRequest{})
	assert.ErrorContains(t, err, "ref is required")
}

// TestOutputToolRejectsMalformedRef pins that magus_output validates the ref SHAPE
// before touching the store, so a non-ref argument fails loudly (and, unlike the old
// magus_query shape-routing, a graph search term can never land here by accident).
func TestOutputToolRejectsMalformedRef(t *testing.T) {
	_, err := (&outputTool{}).Invoke(context.Background(), types.InvokeRequest{Params: map[string]any{"ref": "refactor"}})
	assert.ErrorContains(t, err, "not a target-output reference")
}

// TestOutputToolInvokeHappy drives outputTool.Invoke through a fake reader: a valid
// ref resolves to the descriptor fields plus the output string.
func TestOutputToolInvokeHappy(t *testing.T) {
	reader := fakeOutputReader{
		data: []byte("hello stdout"),
		desc: cache.OutputDescriptor{
			Ref:        "ref1a2b3c",
			Project:    "pkg/a",
			Target:     "build",
			Failed:     true,
			DurationMs: 42,
		},
	}
	resp, err := (&outputTool{reader: reader}).Invoke(context.Background(), types.InvokeRequest{Params: map[string]any{"ref": "ref1a2b3c"}})
	require.NoError(t, err)
	assert.Equal(t, outputRefResult{
		Ref:        "ref1a2b3c",
		Project:    "pkg/a",
		Target:     "build",
		Failed:     true,
		DurationMs: 42,
		Output:     "hello stdout",
	}, resp.Data)
}

// TestOutputToolInvokeAmbiguous pins that an *cache.AmbiguousRefError from the reader
// is wrapped as an "mcp: ..." error that still lists the candidates.
func TestOutputToolInvokeAmbiguous(t *testing.T) {
	reader := fakeOutputReader{err: &cache.AmbiguousRefError{Prefix: "ref1a", Candidates: []string{"ref1a2b3c", "ref1a9f0e"}}}
	_, err := (&outputTool{reader: reader}).Invoke(context.Background(), types.InvokeRequest{Params: map[string]any{"ref": "ref1a2b3c"}})
	assert.ErrorContains(t, err, "mcp: ")
	assert.ErrorContains(t, err, "is ambiguous")
	var amb *cache.AmbiguousRefError
	assert.True(t, errors.As(err, &amb), "the wrapped error is still an *AmbiguousRefError")
}

// TestOutputToolInvokeNotExist maps an fs.ErrNotExist to the "no stored output"
// message; a generic error passes through unwrapped.
func TestOutputToolInvokeNotExist(t *testing.T) {
	reader := fakeOutputReader{err: fs.ErrNotExist}
	_, err := (&outputTool{reader: reader}).Invoke(context.Background(), types.InvokeRequest{Params: map[string]any{"ref": "ref1a2b3c"}})
	assert.ErrorContains(t, err, "no stored output")

	generic := errors.New("disk on fire")
	_, err = (&outputTool{reader: fakeOutputReader{err: generic}}).Invoke(context.Background(), types.InvokeRequest{Params: map[string]any{"ref": "ref1a2b3c"}})
	assert.ErrorIs(t, err, generic)
}

// TestRegistryHasOutputDriver pins that magus_output is both described and wired:
// registerTools panics if a descriptor lacks a driver.
func TestRegistryHasOutputDriver(t *testing.T) {
	var described bool
	for _, d := range Registry {
		if d.Name == "magus_output" {
			described = true
		}
	}
	assert.True(t, described, "magus_output missing from Registry")
}
