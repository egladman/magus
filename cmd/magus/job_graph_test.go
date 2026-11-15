package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// The graph of a checkout the process did not open answers from that checkout's own
// workspace and leaves the one the process holds alone.
func TestJobGraphOpensAnotherCheckoutInItsOwnScope(t *testing.T) {
	process, other := t.TempDir(), t.TempDir()
	for _, dir := range []string{process, other} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
	}
	opened := magusOpened
	t.Cleanup(func() { magusOpened = opened })
	magusOpened = openedRoot{override: process, root: process}
	magusOnce.Do(func() {})

	graphIn := graphsByTree(process, newJobGraph(process, true), true)
	graph := graphIn(other)

	require.NotPanics(t, func() { graph.staleIndexes(t.Context()) })
	require.NotPanics(t, func() { graph.loadScoped(nil)(t.Context()) })
	require.NotPanics(t, func() { graph.LoadDomain(t.Context(), nil) })
	assert.Equal(t, openedRoot{override: process, root: process}, magusOpened)
}

type progressWriterSuite struct {
	suite.Suite
	said     bytes.Buffer
	previous *slog.Logger
}

func TestProgressWriterSuite(t *testing.T) { suite.Run(t, new(progressWriterSuite)) }

func (s *progressWriterSuite) SetupTest() {
	s.previous = slog.Default()
	s.said.Reset()
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.said, nil)))
}

func (s *progressWriterSuite) TearDownTest() { slog.SetDefault(s.previous) }

func (s *progressWriterSuite) TestProgressGoesToStderrUnlessQuiet() {
	s.Equal(os.Stderr, progressWriter(s.T().Context()))
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.said, &slog.HandlerOptions{Level: slog.LevelWarn})))
	s.Equal(io.Discard, progressWriter(s.T().Context()))
}
