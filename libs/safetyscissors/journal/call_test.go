package journal

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

type CallsSuite struct {
	suite.Suite
	dir string
}

func TestCalls(t *testing.T) { suite.Run(t, new(CallsSuite)) }

func (s *CallsSuite) SetupTest() { s.dir = s.T().TempDir() }

// writeLines lays down the activity log as a producer would have left it.
func (s *CallsSuite) writeLines(lines ...string) {
	path := filepath.Join(s.dir, callsFile)
	s.Require().NoError(os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
}

func (s *CallsSuite) line(c Call) string {
	b, err := codec.Marshal(c)
	s.Require().NoError(err)
	return string(b)
}

// read collects every call and error Calls yields.
func (s *CallsSuite) read(limit int) ([]Call, []error) {
	var (
		calls []Call
		errs  []error
	)
	for c, err := range Calls(s.T().Context(), s.dir, limit) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		calls = append(calls, c)
	}
	return calls, errs
}

// writtenCalls are lines the magus writer produced before the activity log
// moved into this module: a job, and a judged agent command.
var writtenCalls = []string{
	`{"ts":1794086077260,"kind":"job","user":"eli","workspace":"/repo","action":"graph build","outcome":"ok","duration_ms":1200,"guard_ms":0,"request_bytes":0,"response_bytes":0}`,
	`{"ts":1794086077261,"kind":"agent_command","host":"claude","session":"s1","workspace":"/repo","action":"Bash","lease":"fold","lease_from":"env","outcome":"ok","duration_ms":0,"guard_ms":12,"request_ref":"agent0123456789abcdef","response_ref":"agentfedcba9876543210","preview":"guard: deny","request_bytes":40,"response_bytes":60,"verdict_ref":"grd0123456789abcdef","policy_digest":"sha256:1","decided_by":"builtin","binary":"/bin/magus","binary_version":"v1","cwd":"/repo","phases":[{"name":"load","elapsed_ms":3,"budget_ms":0,"lapsed":false}]}`,
}

func (s *CallsSuite) TestEveryWrittenCallRoundTripsByteForByte() {
	for _, line := range writtenCalls {
		var c Call
		s.Require().NoError(codec.Unmarshal([]byte(line), &c))
		got, err := codec.Marshal(c)
		s.Require().NoError(err)
		s.Equal(line, string(got))
	}
}

func (s *CallsSuite) TestAGuardedCallReadsItsGuard() {
	var c Call
	s.Require().NoError(codec.Unmarshal([]byte(writtenCalls[1]), &c))
	s.Require().NotNil(c.Guard)
	s.Equal(12*time.Millisecond, c.Guard.Duration)
	s.Equal("grd0123456789abcdef", c.Guard.Verdict.Ref)
	s.Equal(Payload{Ref: "agent0123456789abcdef", Bytes: 40}, c.Request)

	var plain Call
	s.Require().NoError(codec.Unmarshal([]byte(writtenCalls[0]), &plain))
	s.Nil(plain.Guard, "a call no guard judged has no Guard")
	s.Equal("graph build", plain.Name)
}

func (s *CallsSuite) TestCallsIsNewestFirst() {
	s.writeLines(
		s.line(Call{Time: time.UnixMilli(1), Kind: CallKindJob, Name: "one", Outcome: OutcomeOK}),
		s.line(Call{Time: time.UnixMilli(2), Kind: CallKindJob, Name: "two", Outcome: OutcomeOK}),
		s.line(Call{Time: time.UnixMilli(3), Kind: CallKindJob, Name: "three", Outcome: OutcomeOK}),
	)
	got, errs := s.read(10)
	s.Empty(errs)
	s.Require().Len(got, 3)
	s.Equal([]string{"three", "two", "one"}, []string{got[0].Name, got[1].Name, got[2].Name})
}

func (s *CallsSuite) TestALimitKeepsTheTail() {
	lines := make([]string, 0, 10)
	for i := 1; i <= 10; i++ {
		lines = append(lines, s.line(Call{Time: time.UnixMilli(int64(i)), Kind: CallKindJob, Name: fmt.Sprint(i)}))
	}
	s.writeLines(lines...)
	got, _ := s.read(3)
	s.Require().Len(got, 3)
	s.Equal([]string{"10", "9", "8"}, []string{got[0].Name, got[1].Name, got[2].Name})
}

func (s *CallsSuite) TestAMissingLogHasNoCalls() {
	got, errs := s.read(10)
	s.Empty(got)
	s.Empty(errs)

	got, errs = func() ([]Call, []error) {
		s.dir = filepath.Join(s.dir, "absent")
		return s.read(10)
	}()
	s.Empty(got)
	s.Empty(errs, "a directory nothing has recorded in is a normal state")
}

func (s *CallsSuite) TestNoDirAndNoLimitAreRefused() {
	for _, args := range []struct {
		dir   string
		limit int
	}{{"", 10}, {s.dir, 0}} {
		var errs []error
		for _, err := range Calls(s.T().Context(), args.dir, args.limit) {
			errs = append(errs, err)
		}
		s.Len(errs, 1, "dir %q limit %d", args.dir, args.limit)
	}
}

// TestABadLineIsALineError replaces the reader that dropped a corrupt line
// without a word: the line comes back as a *codec.LineError and the read goes
// on.
func (s *CallsSuite) TestABadLineIsALineError() {
	s.writeLines(
		s.line(Call{Time: time.UnixMilli(1), Kind: CallKindJob, Name: "good-one"}),
		"this is not json",
		s.line(Call{Time: time.UnixMilli(2), Kind: CallKindJob, Name: "good-two"}),
	)
	got, errs := s.read(10)
	s.Require().Len(got, 2)
	s.Equal("good-two", got[0].Name)
	s.Equal("good-one", got[1].Name)
	s.Require().Len(errs, 1)
	var lineErr *codec.LineError
	s.Require().ErrorAs(errs[0], &lineErr)
	s.Equal(2, lineErr.Line)
}

func (s *CallsSuite) TestTheOriginReadsInline() {
	s.writeLines(`{"ts":1,"kind":"job","user":"eli","host":"claude","action":"a","outcome":"ok"}`)
	got, _ := s.read(1)
	s.Require().Len(got, 1)
	s.Equal(job.Origin{User: "eli", Host: "claude"}, got[0].Origin)
}

func (s *CallsSuite) TestDurMsIsNotRead() {
	s.writeLines(`{"ts":1,"kind":"job","action":"a","outcome":"ok","dur_ms":900}`)
	got, _ := s.read(1)
	s.Require().Len(got, 1)
	s.Zero(got[0].Duration)
}

func (s *CallsSuite) TestReadReturnsTheStoredPayload() {
	ref := "mcp0123456789abcdef"
	path := filepath.Join(s.dir, payloadDir, ref)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	s.Require().NoError(os.WriteFile(path, []byte("payload"), 0o600))

	got, err := Payload{Ref: ref}.Read(s.dir)
	s.Require().NoError(err)
	s.Equal("payload", string(got))
}

func (s *CallsSuite) TestReadRefusesAnUnsafeRef() {
	for _, bad := range []string{"", "..", "mcp/../x", "mcpZZZZZZZZZZZZZZZZ", "mcp0123", "MCP0123456789abcd"} {
		_, err := Payload{Ref: bad}.Read(s.dir)
		s.Require().ErrorIs(err, fs.ErrNotExist, "Read(%q)", bad)
	}
	_, err := os.Stat(filepath.Join(s.dir, payloadDir))
	s.ErrorIs(err, os.ErrNotExist, "a refused read creates nothing")
}

func (s *CallsSuite) TestValidPayloadRefSplitsTheHashFromTheEnd() {
	s.True(ValidPayloadRef("mcp0123456789abcdef"))
	s.True(ValidPayloadRef("agentabcdefabcdefabcd"), "hex letters after a letter prefix")
	s.True(ValidPayloadRef("ab0123456789abcdef"), "a two-letter prefix")
	s.False(ValidPayloadRef("a0123456789abcdef"), "a one-letter prefix")
	s.False(ValidPayloadRef("abcdefghi0123456789abcdef"), "a nine-letter prefix")
	s.False(ValidPayloadRef("mcp0123456789abcdeF"), "an upper-case hex digit")
}
