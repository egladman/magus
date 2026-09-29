package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interp/mcpclient"
)

func TestClientWorkerOutsideAWorkspace(t *testing.T) {
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer
	in := strings.NewReader(`{"script":"fun main(args: [str]) > int { return 0; }"}`)
	assert.Equal(t, 1, runClientWorker(context.Background(), in, &stdout, &stderr))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "client: open workspace:")
}

func TestClientWorkerRejectsOversizedRequest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	in := strings.NewReader(strings.Repeat("x", mcpclient.MaxRequestBytes+1))
	assert.Equal(t, 1, runClientWorker(context.Background(), in, &stdout, &stderr))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "client: request is too large")
}

func TestClientWorkspaceKeepsTheContextOnError(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, err := clientWorkspace(context.Background(), io.Discard)
	require.Error(t, err)
	assert.NotNil(t, ctx)
}
