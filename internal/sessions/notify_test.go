package sessions

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type NotifySuite struct{ suite.Suite }

func TestNotify(t *testing.T) { suite.Run(t, new(NotifySuite)) }

// captureWarnings installs a slog handler for the duration of fn and returns what it
// logged. A producer reports a request it could not use through slog rather than an
// error (a non-zero exit from an agent hook interrupts the very session the notification
// exists to help), so the default logger is the only place to observe it.
func captureWarnings(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}

func (s *NotifySuite) TestClassifyOutcomeReadsEachKnownSpelling() {
	s.Equal(types.OutcomeWaiting, ClassifyOutcome("Notification"))
	s.Equal(types.OutcomeFinished, ClassifyOutcome("SubagentStop"))
	s.Equal(types.OutcomePermission, ClassifyOutcome("permission-request"))
	s.Equal(types.OutcomeFailed, ClassifyOutcome("build failed"))
	s.Equal(types.OutcomeDiagnostic, ClassifyOutcome("diagnostic"))
	s.Equal(types.OutcomeUpdate, ClassifyOutcome("available update"))
}

func (s *NotifySuite) TestClassifyOutcomeOfAnythingElseIsOther() {
	s.Equal(types.OutcomeOther, ClassifyOutcome(""))
	s.Equal(types.OutcomeOther, ClassifyOutcome("something new"))
}

func (s *NotifySuite) TestEventFromStdin() {
	t := s.T()
	canonical := `{"api_version":"magus.event/v1alpha1","outcome":"permission","severity":"critical","source":{"kind":"agent","sub":"host","id":"abc"},"message":"needs approval"}`
	got := EventFromStdin(t.Context(), []byte(canonical))
	assert.Equal(t, types.Event{
		APIVersion: types.EventAPIVersion,
		Outcome:    types.OutcomePermission,
		Severity:   types.SeverityCritical,
		Source:     types.EventSource{Kind: "agent", Sub: "host", ID: "abc"},
		Message:    "needs approval",
	}, got)

	assert.Equal(t, types.Event{Message: `{"outcome":"waiting"}`}, EventFromStdin(t.Context(), []byte(`{"outcome":"waiting"}`)))
	assert.Equal(t, types.Event{Message: "build failed"}, EventFromStdin(t.Context(), []byte("build failed")))
}

// demoted reads body through EventFromStdin, which must still deliver it as
// prose, and returns what it warned.
func (s *NotifySuite) demoted(body string) string {
	var ev types.Event
	logged := captureWarnings(s.T(), func() { ev = EventFromStdin(s.T().Context(), []byte(body)) })
	s.Equal(body, ev.Message, "the body is still delivered as prose")
	return logged
}

// A body that LOOKS like an envelope and fails still demotes to prose (the notification
// has to fire either way), but silently it was undiagnosable: the producer saw a working
// notification, no attention request, and nothing anywhere saying why.
func (s *NotifySuite) TestEventFromStdinWarnsOnAnEnvelopeMissingEveryRequiredField() {
	logged := s.demoted(`{"severity":"warning"}`)
	for _, want := range []string{"not a complete event envelope", "message", "outcome", "source.kind"} {
		s.Contains(logged, want)
	}
}

func (s *NotifySuite) TestEventFromStdinWarnsOnAnEnvelopeMissingOnlyTheSource() {
	logged := s.demoted(`{"outcome":"waiting","message":"needs a decision"}`)
	s.Contains(logged, "not a complete event envelope")
	s.Contains(logged, "source.kind")
}

func (s *NotifySuite) TestEventFromStdinWarnsOnAnUnparseableObject() {
	s.Contains(s.demoted(`{"outcome":"waiting",`), "did not parse")
}

// Prose is the documented input, not a degraded envelope, so it must warn about nothing.
func (s *NotifySuite) TestEventFromStdinIsSilentForPlainText() {
	t := s.T()
	logged := captureWarnings(t, func() { EventFromStdin(t.Context(), []byte("build failed")) })
	assert.Empty(t, logged)
}

func (s *NotifySuite) TestRenderNotificationAlwaysHasABody() {
	t := s.T()
	title, body := RenderNotification(types.Event{Outcome: types.OutcomeWaiting})
	assert.NotEmpty(t, title)
	assert.NotEmpty(t, body)
}
