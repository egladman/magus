package handler

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewDefaultsLogger checks New(serve, nil) never leaves Log nil - it falls back to
// slog.Default() so handlers can always log.
func TestNewDefaultsLogger(t *testing.T) {
	b := New(func(http.ResponseWriter, *http.Request) {}, nil)
	require.NotNil(t, b.Log)
	assert.Same(t, slog.Default(), b.Log)
}

// TestNewKeepsLogger checks a supplied logger rides through unchanged.
func TestNewKeepsLogger(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := New(func(http.ResponseWriter, *http.Request) {}, log)
	assert.Same(t, log, b.Log)
}

// TestBaseServeHTTP checks the embedded http.Handler is what actually serves the route:
// ServeHTTP dispatches to the wrapped serve func.
func TestBaseServeHTTP(t *testing.T) {
	served := false
	b := New(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "ok")
	}, nil)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)

	assert.True(t, served, "ServeHTTP must dispatch to the wrapped serve func")
	assert.Equal(t, http.StatusTeapot, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}
