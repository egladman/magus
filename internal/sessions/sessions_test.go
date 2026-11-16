package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// writeRaw lays down an invocation file directly, so a test can pin timestamps and
// sequence numbers the Writer would otherwise stamp from the clock.
func writeRaw(t *testing.T, dir, invocation string, records []session.Record) {
	t.Helper()
	var b strings.Builder
	for _, rec := range records {
		line, err := json.Marshal(rec)
		require.NoError(t, err)
		b.Write(line)
		b.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, invocation+session.FileExt), []byte(b.String()), 0o644))
}

type MessageBoundSuite struct{ suite.Suite }

func TestMessageBound(t *testing.T) { suite.Run(t, new(MessageBoundSuite)) }

// A message is whatever a hook piped to `magus session notify`, so an unbounded one is one
// process's decision that every later read of this repository pays for. The writer
// applies the bound, which is what makes it hold for a producer that never heard of it.
func (s *MessageBoundSuite) TestAppendBoundsAnOversizedAttentionMessage() {
	dir := s.T().TempDir()

	w, err := session.Open(dir, "sess1", session.InvocationStart{})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(KindAttentionOpen, AttentionOpen{
		Request: "att-1",
		Outcome: "waiting",
		Message: strings.Repeat("x", MaxMessageBytes*4),
	}))

	fold, err := session.ReadAll(dir)
	s.Require().NoError(err)
	s.Require().Len(fold.Records, 2)

	var got AttentionOpen
	s.Require().NoError(json.Unmarshal(fold.Records[1].Payload, &got))
	// The marker is what lets a reader tell a short message from a long one nobody kept.
	s.Equal(strings.Repeat("x", MaxMessageBytes)+messageTruncated, got.Message)
}

func (s *MessageBoundSuite) TestBoundedMessageCutsOnARuneBoundary() {
	// U+4E16 encodes to three bytes, and the bound is not a multiple of three, so the
	// cut lands mid-rune unless it is walked back.
	cut := AttentionOpen{Message: strings.Repeat(string(rune(0x4e16)), MaxMessageBytes)}.bounded()
	s.True(utf8.ValidString(cut.Message), "a mid-rune cut stores bytes no reader can decode")
	s.LessOrEqual(len(cut.Message), MaxMessageBytes+len(messageTruncated))

	short := AttentionOpen{Request: "att-1", Message: "needs a decision"}
	s.Equal(short, short.bounded(), "a message inside the bound is stored verbatim")
}

// The composed case: a request opened on an oversized line is unreadable, and the
// normal open after it must still reach the queue. A file that hid its own tail would
// also defeat attention dedupe, because dedupe IS a search for a later record.
//
// Both lines are written raw, because the write-time bound means no current magus
// produces the first one; an older binary, or a hand-edited file, does. The first is
// past the reader's 1 MiB line budget.
func (s *MessageBoundSuite) TestOversizedOpenDoesNotHideALaterOne() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "sess1.jsonl")

	body := strings.Join([]string{
		fmt.Sprintf(`{"v":"magus.sessions/v1alpha1","invocation":"sess1","seq":1,"kind":"attention_open","ts":1,"payload":{"request":"att-huge","outcome":"waiting","message":"%s"}}`, strings.Repeat("x", 2<<20)),
		`{"v":"magus.sessions/v1alpha1","invocation":"sess1","seq":2,"kind":"attention_open","ts":2,"payload":{"request":"att-small","outcome":"waiting","message":"needs a decision"}}`,
	}, "\n")
	s.Require().NoError(os.WriteFile(path, []byte(body+"\n"), 0o644))

	fold, err := session.ReadAll(dir)
	s.Require().NoError(err)
	s.Equal(1, fold.Skipped)

	open := AttentionQueue(fold)
	s.Require().Len(open, 1, "the open after the oversized line must still fold")
	s.Equal(AttentionRequest{ID: "att-small", Invocation: "sess1", OpenedMs: 2, Outcome: "waiting", Message: "needs a decision"}, open[0])
}

type DirSuite struct{ suite.Suite }

func TestDir(t *testing.T) { suite.Run(t, new(DirSuite)) }

// Worktrees of one repo must resolve to one store, or a fact recorded in one is
// invisible from the other and the whole feature is pointless.
func (s *DirSuite) TestIsSharedAcrossWorktreesOfOneRepo() {
	t := s.T()
	testkit.Isolate(t)

	main := t.TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(main, ".git", "worktrees", "feature"), 0o755))

	linked := t.TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(linked, ".git"),
		fmt.Appendf(nil, "gitdir: %s\n", filepath.Join(main, ".git", "worktrees", "feature")), 0o644))

	mainDir, err := Dir(main)
	s.Require().NoError(err)
	linkedDir, err := Dir(linked)
	s.Require().NoError(err)
	s.Equal(mainDir, linkedDir)

	unrelated, err := Dir(t.TempDir())
	s.Require().NoError(err)
	s.NotEqual(mainDir, unrelated)
}

func (s *DirSuite) TestIsUnderTheUserStateDir() {
	t := s.T()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	dir, err := Dir(t.TempDir())
	s.Require().NoError(err)
	s.True(strings.HasPrefix(dir, filepath.Join(state, "magus", "sessions")),
		"store %s is not under the user state dir", dir)
}
