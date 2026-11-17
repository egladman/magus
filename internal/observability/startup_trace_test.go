package observability

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
)

type StartupTraceSuite struct{ suite.Suite }

func TestStartupTraceSuite(t *testing.T) { suite.Run(t, new(StartupTraceSuite)) }

func (s *StartupTraceSuite) TestADisabledTracerRecordsAndPrintsNothing() {
	var buf bytes.Buffer
	tr := NewStartupTracer(false)
	tr.SetWriter(&buf)
	tr.Phase("a")()
	tr.Done()
	s.Empty(buf.String())
	s.Nil(tr.Profile())
}

func (s *StartupTraceSuite) TestAnEnabledTracerPrintsEachPhaseAndTheTotal() {
	var buf bytes.Buffer
	tr := NewStartupTracer(true)
	tr.SetWriter(&buf)
	tr.Phase("startup.find_root")()
	tr.Done()
	s.Contains(buf.String(), "magus startup trace:")
	s.Contains(buf.String(), "startup.find_root")
	s.Contains(buf.String(), "total")
}

func (s *StartupTraceSuite) TestEnableRetrofitsADisabledTracerWithoutAProfile() {
	tr := NewStartupTracer(false)
	tr.Enable()
	s.True(tr.Enabled())
	s.Nil(tr.Profile())
	s.NotNil(NewStartupTracer(true).Profile())
}

func (s *StartupTraceSuite) TestTheContextCarriesTheTracerAndDefaultsToADisabledOne() {
	tr := NewStartupTracer(true)
	s.Same(tr, TraceFromContext(WithTrace(context.Background(), tr)))
	s.False(TraceFromContext(context.Background()).Enabled())
}
