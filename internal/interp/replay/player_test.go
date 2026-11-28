package replay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

type PlayerSuite struct {
	suite.Suite
	dir    string
	header Header
	// ran counts how often each native ran, across sessions.
	ran map[string]int
	// dieInRead makes fs.readFile cancel the run it is called in.
	dieInRead bool
}

func TestPlayer(t *testing.T) { suite.Run(t, new(PlayerSuite)) }

func (s *PlayerSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.header = Header{Script: "run.buzz"}
	s.ran = map[string]int{}
	s.dieInRead = false
}

// classes treats every native as an answer except those under "live.".
func classes(name string) (Class, bool) {
	if strings.HasPrefix(name, "live.") {
		return classLive, true
	}
	return classAnswer, true
}

// session is a script session whose natives count their runs. cancel is the context
// the run holds, which the native "probe.die" cancels to stand in for a killed run.
func (s *PlayerSuite) session(obs vm.CallInterceptor, cancel context.CancelFunc) *buzz.Session {
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded(), buzz.WithCallInterceptor(obs))
	s.T().Cleanup(func() { _ = sess.Close() })
	mod := vm.NewMap()
	native := func(module, name string, f func(ctx context.Context, args []vm.Value) (vm.Value, error)) {
		full := fmt.Sprintf("%s.%s", module, name)
		m := mod
		if module != "probe" {
			existing, ok := sess.NativeModule(module)
			if !ok {
				existing = vm.NewMap()
				sess.SetNativeModule(module, existing)
			}
			m = existing
		}
		m.MapSet(name, vm.DirectValue(full, func(ctx context.Context, args []vm.Value) (vm.Value, error) {
			s.ran[full]++
			return f(ctx, args)
		}))
	}
	native("probe", "count", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		return vm.IntValue(args[0].AsInt()*7 + 1), nil
	})
	native("probe", "each", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		var sum int64
		for _, it := range args[0].ListItems() {
			v, err := sess.CallValue(ctx, args[1], []vm.Value{it})
			if err != nil {
				return vm.Null, err
			}
			sum += v.AsInt()
		}
		return vm.IntValue(sum), nil
	})
	native("probe", "fail", func(context.Context, []vm.Value) (vm.Value, error) {
		return vm.Null, errors.New("disk on fire")
	})
	native("probe", "make", func(context.Context, []vm.Value) (vm.Value, error) {
		return vm.DirectValue("probe.cb", func(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil }), nil
	})
	native("probe", "die", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		cancel()
		return vm.Null, context.Canceled
	})
	native("live", "open", func(context.Context, []vm.Value) (vm.Value, error) { return vm.IntValue(1), nil })
	native("fs", "readFile", func(context.Context, []vm.Value) (vm.Value, error) {
		if s.dieInRead {
			cancel()
			return vm.Null, context.Canceled
		}
		return vm.StringValue("data"), nil
	})
	sess.SetNativeModule("probe", mod)
	return sess
}

// record runs src live under a Recorder, with the run's context cancelled by probe.die.
func (s *PlayerSuite) record(src string) error {
	rec, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess := s.session(NewRecorder(rec), cancel)
	runErr := sess.Exec(ctx, src)
	s.Require().NoError(rec.Close())
	return runErr
}

// replay runs src against the record under a Player, and returns it with the run's error.
func (s *PlayerSuite) replay(src string) (*Player, error) {
	_, calls, err := Read(s.dir, "out1")
	s.Require().NoError(err)
	rec, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	p := NewPlayer(s.header, calls, classes, nil, NewRecorder(rec))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := s.session(p, cancel).Exec(ctx, src)
	s.Require().NoError(rec.Close())
	return p, runErr
}

const countScript = `
import "probe";
var total = 0;
foreach (i in 0..3) { total = total + probe\count(i); }
`

func (s *PlayerSuite) TestAnEndedCallIsAnsweredWithoutRunning() {
	s.Require().NoError(s.record(countScript))
	s.Equal(3, s.ran["probe.count"])

	p, err := s.replay(countScript)
	s.Require().NoError(err)
	s.Equal(3, s.ran["probe.count"], "no native ran a second time")
	s.Require().NoError(p.Unreplayed())
}

func (s *PlayerSuite) TestACallWithNestedCallsRunsAgainAndItsNestedCallsAreAnswered() {
	const nested = `
import "probe";
final seen = mut [0];
final total = probe\each([1, 2, 3], fun (n: int) > int {
    seen.append(n);
    return probe\count(n);
});
final n = seen.len();
`
	s.Require().NoError(s.record(nested))
	s.Equal(3, s.ran["probe.count"])
	s.Equal(1, s.ran["probe.each"])

	p, err := s.replay(nested)
	s.Require().NoError(err)
	s.Equal(2, s.ran["probe.each"], "the outer call runs again so the callback's writes repeat")
	s.Equal(3, s.ran["probe.count"], "the nested calls are answered")
	s.Require().NoError(p.Unreplayed())
}

func (s *PlayerSuite) TestARaisedCallRaisesTheSameErrorWithoutRunning() {
	const raising = `
import "probe";
var caught = "";
try { probe\fail(); } catch (e) { caught = "{e}"; }
probe\count(caught.len());
`
	s.Require().NoError(s.record(raising))
	_, calls, err := Read(s.dir, "out1")
	s.Require().NoError(err)
	s.Require().Len(calls, 2)
	s.Equal(callRaised, calls[0].End)
	s.Contains(calls[0].Error, "disk on fire")

	p, err := s.replay(raising)
	s.Require().NoError(err, "the same error reached the script's catch, so the same count was asked")
	s.Require().NoError(p.Unreplayed())
	s.Equal(1, s.ran["probe.fail"], "the failing native did not run again")
	s.Equal(1, s.ran["probe.count"])
}

func (s *PlayerSuite) TestADifferentCallIsRefusedNamingBoth() {
	s.Require().NoError(s.record(countScript))

	p, err := s.replay("import \"probe\";\nfinal a = probe\\count(5);\n")
	var refusal *refusalError
	s.Require().ErrorAs(err, &refusal)
	s.ErrorIs(err, RefusalDiverged)
	refusal.Call.StartedAt = 0
	s.Equal(Call{
		Seq: 0, Parent: -1, Name: "probe.count", ArgsDigest: argsDigest([]vm.Value{vm.IntValue(0)}), End: callReturned, Result: "1",
	}, refusal.Call)
	s.Contains(err.Error(), "recorded probe.count[")
	s.Contains(err.Error(), "but the run made probe.count[")
	s.Contains(err.Error(), "run again with --restart to discard the record and start from the top")
	s.ErrorIs(p.Err(), RefusalDiverged, "the player holds the refusal it returned")
	s.Equal(0, refusal.Call.Seq)
}

func (s *PlayerSuite) TestNoCallRunsAfterARefusal() {
	s.Require().NoError(s.record(countScript))
	before := s.ran["probe.count"]

	_, err := s.replay("import \"probe\";\nfinal a = probe\\count(5);\nfinal b = probe\\count(6);\n")
	s.Require().Error(err)
	s.Equal(before, s.ran["probe.count"])
}

func (s *PlayerSuite) TestAnUnencodableResultIsRefused() {
	const unencodable = "import \"probe\";\nfinal f = probe\\make();\n"
	s.Require().NoError(s.record(unencodable))

	_, err := s.replay(unencodable)
	s.ErrorIs(err, RefusalUnencodable)
	s.ErrorContains(err, "its result could not be recorded: direct at $")
}

func (s *PlayerSuite) TestACallToALiveModuleIsRefused() {
	const live = "import \"live\";\nfinal h = live\\open();\n"
	s.Require().NoError(s.record(live))

	_, err := s.replay(live)
	s.ErrorIs(err, RefusalLive)
	s.ErrorContains(err, "live.open returns state that lives in the process that made it")
}

const readScript = `
import "probe";
import "fs";
final a = probe\count(1);
final text = fs\readFile("p");
final b = probe\count(2);
`

const dieScript = `
import "probe";
final a = probe\count(1);
probe\die();
final b = probe\count(2);
`

func (s *PlayerSuite) TestAnInterruptedRepeatableCallRunsAgain() {
	s.dieInRead = true
	s.Require().Error(s.record(readScript))
	_, calls, err := Read(s.dir, "out1")
	s.Require().NoError(err)
	s.Require().Len(calls, 2)
	s.Equal(callOpen, calls[1].End, "the cancelled read stays open in the record")

	s.dieInRead = false
	p, err := s.replay(readScript)
	s.Require().NoError(err)
	s.Require().NoError(p.Unreplayed())
	s.Equal(2, s.ran["fs.readFile"], "the read ran again")
	s.Equal(2, s.ran["probe.count"], "one answered, one live")

	_, calls, err = Read(s.dir, "out1")
	s.Require().NoError(err)
	s.Require().Len(calls, 3)
	calls[1].StartedAt = 0
	s.Equal(Call{
		Seq: 1, Parent: -1, Name: "fs.readFile", ArgsDigest: argsDigest([]vm.Value{vm.StringValue("p")}), End: callReturned, Result: `"data"`,
	}, calls[1], "the rerun closed the interrupted call")
}

func (s *PlayerSuite) TestAnInterruptedCallThatIsNotRepeatableIsRefused() {
	s.Require().Error(s.record(dieScript))

	p, err := s.replay(dieScript)
	s.ErrorIs(err, RefusalOutcomeUnknown)
	s.ErrorContains(err, "the run died inside it")
	var refusal *refusalError
	s.Require().ErrorAs(p.Err(), &refusal)
	s.Equal("probe.die", refusal.Call.Name)
	s.Equal(1, s.ran["probe.die"], "the interrupted native was not run again")
}

func (s *PlayerSuite) TestPastTheRecordTheRunIsLiveAndAppends() {
	s.Require().NoError(s.record("import \"probe\";\nfinal a = probe\\count(0);\nfinal b = probe\\count(1);\n"))

	p, err := s.replay(countScript)
	s.Require().NoError(err)
	s.Require().NoError(p.Unreplayed())
	s.Equal(3, s.ran["probe.count"], "two recorded calls were answered and the third ran")

	_, calls, err := Read(s.dir, "out1")
	s.Require().NoError(err)
	s.Require().Len(calls, 3)
	calls[2].StartedAt = 0
	s.Equal(Call{
		Seq: 2, Parent: -1, Name: "probe.count", ArgsDigest: argsDigest([]vm.Value{vm.IntValue(2)}), End: callReturned, Result: "15",
	}, calls[2], "the live call continues the record's seqs")
}

func (s *PlayerSuite) TestARunThatDoesLessIsRefused() {
	s.Require().NoError(s.record(countScript))

	p, err := s.replay("import \"probe\";\nfinal a = probe\\count(0);\n")
	s.Require().NoError(err, "the shorter run itself is fine")
	err = p.Unreplayed()
	s.ErrorIs(err, RefusalDiverged)
	s.ErrorContains(err, "ended with 2 recorded calls not made")
}

func (s *PlayerSuite) importsPlayer() *Player {
	h := Header{Script: "run.buzz", Imports: map[string]string{"/w/a.buzz": "aaaa", "/w/b.buzz": "bbbb"}}
	return NewPlayer(h, nil, classes, nil, nil)
}

func (s *PlayerSuite) TestTheSameImportsResume() {
	s.NoError(s.importsPlayer().Imports(map[string]string{"/w/a.buzz": "aaaa", "/w/b.buzz": "bbbb"}))
}

func (s *PlayerSuite) TestAChangedImportIsRefusedNamingTheFile() {
	p := s.importsPlayer()
	err := p.Imports(map[string]string{"/w/a.buzz": "aaaa", "/w/b.buzz": "cccc"})
	s.ErrorIs(err, RefusalImportChanged)
	s.EqualError(err, "resume refused: import-changed: /w/b.buzz changed: recorded sha256 bbbb, now cccc: "+remedy)
	s.Equal(err, p.Err(), "the refusal stops the run")
}

func (s *PlayerSuite) TestAnAddedOrDroppedImportIsRefused() {
	err := s.importsPlayer().Imports(map[string]string{"/w/a.buzz": "aaaa", "/w/b.buzz": "bbbb", "/w/c.buzz": "cccc"})
	s.ErrorIs(err, RefusalImportChanged)
	s.ErrorContains(err, "/w/c.buzz is imported now and was not")
	err = s.importsPlayer().Imports(map[string]string{"/w/b.buzz": "bbbb"})
	s.ErrorContains(err, "/w/a.buzz was imported when the run was recorded and is not now")
}

func (s *PlayerSuite) TestARecordWithoutImportsIsRefused() {
	err := NewPlayer(Header{Script: "run.buzz"}, nil, classes, nil, nil).Imports(map[string]string{})
	s.ErrorIs(err, RefusalImportChanged, "an empty map is not trusted for a missing line")
	s.ErrorContains(err, "the record holds no import digests: the run that wrote it died before its entry compiled")
	unread := Header{Script: "run.buzz", ImportsUnread: "the run ended before its entry compiled"}
	err = NewPlayer(unread, nil, classes, nil, nil).Imports(map[string]string{})
	s.ErrorContains(err, "no import digests: the run ended before its entry compiled")
}
