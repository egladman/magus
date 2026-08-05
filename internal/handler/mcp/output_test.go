package mcp

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOutputReader is a hand-built outputReader: it returns canned bytes and a
// descriptor, or a chosen error, from OutputByRef, and canned matches, or a chosen
// error, from IdentifyRef - so outputTool.Invoke is unit-testable without a real
// workspace cache.
type fakeOutputReader struct {
	data []byte
	desc cache.OutputDescriptor
	err  error

	identifyMatches []types.RefMatch
	identifyErr     error
}

func (f fakeOutputReader) OutputByRef(string) ([]byte, cache.OutputDescriptor, error) {
	return f.data, f.desc, f.err
}

func (f fakeOutputReader) IdentifyRef(context.Context, string) ([]types.RefMatch, error) {
	return f.identifyMatches, f.identifyErr
}

// TestOutputToolRequiredParam covers the guard that returns before any store access.
func TestOutputToolRequiredParam(t *testing.T) {
	_, err := (&outputTool{}).Invoke(context.Background(), spells.InvokeRequest{})
	assert.ErrorContains(t, err, "ref is required")
}

// TestOutputToolRejectsMalformedRef pins that magus_output validates the ref SHAPE
// before touching the store, so a non-ref argument fails loudly (and, unlike the old
// magus_query shape-routing, a graph search term can never land here by accident).
func TestOutputToolRejectsMalformedRef(t *testing.T) {
	_, err := (&outputTool{}).Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"ref": "refactor"}})
	assert.ErrorContains(t, err, "not a target-output reference")
}

// TestOutputToolInvokeHappy drives outputTool.Invoke through a fake reader: a valid
// ref resolves to the descriptor fields plus the output string.
func TestOutputToolInvokeHappy(t *testing.T) {
	reader := fakeOutputReader{
		data: []byte("hello stdout"),
		desc: cache.OutputDescriptor{
			Ref:        "out1a2b3c",
			Project:    "pkg/a",
			Target:     "build",
			Failed:     true,
			DurationMs: 42,
		},
	}
	resp, err := (&outputTool{reader: reader}).Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"ref": "out1a2b3c"}})
	require.NoError(t, err)
	assert.Equal(t, outputRefResult{
		Ref:        "out1a2b3c",
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
	reader := fakeOutputReader{err: &cache.AmbiguousRefError{Prefix: "ref1a", Candidates: []string{"out1a2b3c", "out1a9f0e"}}}
	_, err := (&outputTool{reader: reader}).Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"ref": "out1a2b3c"}})
	assert.ErrorContains(t, err, "mcp: ")
	assert.ErrorContains(t, err, "is ambiguous")
	var amb *cache.AmbiguousRefError
	assert.True(t, errors.As(err, &amb), "the wrapped error is still an *AmbiguousRefError")
}

// TestOutputToolInvokeNotExist maps an fs.ErrNotExist to the "no stored output"
// message; a generic error passes through unwrapped.
func TestOutputToolInvokeNotExist(t *testing.T) {
	reader := fakeOutputReader{err: fs.ErrNotExist}
	_, err := (&outputTool{reader: reader}).Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"ref": "out1a2b3c"}})
	assert.ErrorContains(t, err, "no stored output")

	generic := errors.New("disk on fire")
	_, err = (&outputTool{reader: fakeOutputReader{err: generic}}).Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"ref": "out1a2b3c"}})
	assert.ErrorIs(t, err, generic)
}

// TestOutputToolInvokeNotExistNoMatch covers the zero-match branch of the
// not-found suggestion: no candidate target keys to the ref, so the message says
// the run that printed it had different inputs rather than naming a command.
func TestOutputToolInvokeNotExistNoMatch(t *testing.T) {
	reader := fakeOutputReader{err: fs.ErrNotExist}
	_, err := (&outputTool{reader: reader}).Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"ref": "out1a2b3c"}})
	assert.ErrorContains(t, err, "no stored output for ref")
	assert.ErrorContains(t, err, "no target in this workspace keys to that ref")
	assert.ErrorContains(t, err, "different inputs")
}

// TestOutputToolInvokeNotExistOneMatch covers the single-match branch: the message
// names the exact `magus run` command that would produce the ref, rendered via
// clihint.RefMatchCommand with the tool's configured defaultCharms.
func TestOutputToolInvokeNotExistOneMatch(t *testing.T) {
	reader := fakeOutputReader{
		err:             fs.ErrNotExist,
		identifyMatches: []types.RefMatch{{Project: ".", Target: "build"}},
	}
	_, err := (&outputTool{reader: reader, defaultCharms: []string{"rw"}}).Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"ref": "out1a2b3c"}})
	assert.ErrorContains(t, err, "no stored output for ref")
	assert.ErrorContains(t, err, "magus run build --no-default-charms")
}

// TestOutputToolInvokeNotExistSeveralMatches covers the multi-match branch: every
// candidate command is named.
func TestOutputToolInvokeNotExistSeveralMatches(t *testing.T) {
	reader := fakeOutputReader{
		err: fs.ErrNotExist,
		identifyMatches: []types.RefMatch{
			{Project: ".", Target: "build"},
			{Project: "pkg/a", Target: "lint"},
		},
	}
	_, err := (&outputTool{reader: reader}).Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"ref": "out1a2b3c"}})
	assert.ErrorContains(t, err, "no stored output for ref")
	assert.ErrorContains(t, err, "any of")
	assert.ErrorContains(t, err, "magus run build")
	assert.ErrorContains(t, err, "magus run lint pkg/a")
}

// TestOutputToolInvokeNotExistIdentifyRefErrors pins the fallback: when IdentifyRef
// itself errors, the plain not-found message survives unchanged - a best-effort
// suggestion must never replace a lookup failure with a different one.
func TestOutputToolInvokeNotExistIdentifyRefErrors(t *testing.T) {
	reader := fakeOutputReader{err: fs.ErrNotExist, identifyErr: errors.New("cache unavailable")}
	_, err := (&outputTool{reader: reader}).Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"ref": "out1a2b3c"}})
	assert.ErrorContains(t, err, "no stored output for ref \"out1a2b3c\"")
	assert.NotContains(t, err.Error(), "cache unavailable")
	assert.NotContains(t, err.Error(), "magus run")
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
