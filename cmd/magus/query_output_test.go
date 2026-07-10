package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteOutputMetaHeader renders the --meta header for a failing run: identity
// fields on their own greppable lines, a "failed" status, and a closing rule.
func TestWriteOutputMetaHeader(t *testing.T) {
	var buf bytes.Buffer
	writeOutputMetaHeader(&buf, cache.OutputMeta{
		Ref:        "refdeadbeef",
		Project:    "svc/api",
		Target:     "test",
		Failed:     true,
		DurationMs: 1200,
	})
	out := buf.String()
	assert.Contains(t, out, "ref:      refdeadbeef")
	assert.Contains(t, out, "project:  svc/api")
	assert.Contains(t, out, "target:   test")
	assert.Contains(t, out, "status:   failed")
	assert.Contains(t, out, "duration: 1200ms")
	assert.True(t, strings.HasSuffix(strings.TrimRight(out, "\n"), "----"), "header ends with a rule")
}

// TestBuildLogViewerURL builds the log-viewer deep link: the ref identity AND the
// gzip+base64url Journal both ride the URL fragment (after #), which the browser never
// transmits - so nothing about the run, not even its ref, leaves the machine.
func TestBuildLogViewerURL(t *testing.T) {
	events := []journal.Event{
		{Ts: 1, Kind: journal.KindOutput, Stream: journal.StreamStdout, Text: "build failed: boom"},
		{Ts: 2, Kind: journal.KindResult, Status: journal.StatusFail, Ref: "refdeadbeef"},
	}
	url, err := buildLogViewerURL(defaultLogViewerURL, cache.OutputMeta{Ref: "refdeadbeef", Failed: true}, events)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(url, defaultLogViewerURL+"#ref=refdeadbeef&data="),
		"url should carry the ref identity then the fragment data, got %q", url)
	// Everything rides after #, which browsers never send to the server: no query string.
	before, after, found := strings.Cut(url, "#")
	require.True(t, found, "url must have a fragment")
	assert.NotContains(t, before, "?", "nothing must ride the query string")
	assert.Contains(t, after, "ref=refdeadbeef", "ref must live in the fragment")
	assert.Contains(t, after, "data=", "data must live in the fragment")
}
