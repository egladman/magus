package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
)

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
