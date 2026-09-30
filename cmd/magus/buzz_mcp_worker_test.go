package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interp/transform"
	"github.com/egladman/magus/internal/json"
)

func TestBuzzWorker(t *testing.T) {
	req := transform.Request{
		Script: `fun transform(input: any, args: [str]) > any { return input; }`,
		Input:  json.RawMessage(`{"ok":true}`),
	}
	wire, err := json.Marshal(req)
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	assert.Zero(t, runBuzzWorker(context.Background(), bytes.NewReader(wire), &stdout, &stderr))
	assert.Empty(t, stderr.String())
	var result transform.Result
	require.NoError(t, json.UnmarshalStrict(stdout.Bytes(), &result))
	assert.JSONEq(t, `{"ok":true}`, string(result.JSON))
}

func TestBuzzWorkerRejectsOversizedRequest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	input := strings.NewReader(strings.Repeat("x", transform.MaxRequestBytes+1))
	assert.Equal(t, 1, runBuzzWorker(context.Background(), input, &stdout, &stderr))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "request is too large")
}
