package broker

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// wireFixtures is one value of every body the wire carries, as this build encodes it.
// Each is checked against testdata/wire/v<ProtocolVersion>/<name>.json, written by hand
// when a body is added and never rewritten: a golden that changes is a protocol that
// changed.
func wireFixtures() map[string]any {
	since := time.Date(2026, 9, 24, 14, 2, 0, 0, time.UTC)
	holder := types.MachineClaimant{Project: "api", Target: "test", PID: 4242, MemoryMB: 512, Slots: 2, Dir: "/src/a", Command: "magus run test", Since: since}
	return map[string]any{
		typeHello: hello{Magic: helloMagic, Protocol: 1, MinProtocol: 1, PID: 4242, Dir: "/src/a",
			Argv: []string{"magus", "run", "test"}, Version: "v0.5.0"},
		typeHelloReply: helloReply{PID: 100, Protocol: 1, Version: "v0.5.0"},
		typeClaim: claimRequest{Reassert: true, Claim: types.MachineClaim{Project: "api", Target: "test", DeclaredBy: "ci",
			MemoryMB: 512, Slots: 2, PID: 4242, Dir: "/src/a", Command: "magus run test", Invocation: "inv-2", Ancestors: []string{"inv-1"}}},
		typeClaimReply: claimReply{Verdict: types.MachineVerdict{Granted: false, ID: "c-1", Fits: true, OwnRun: true,
			Holders: []types.MachineClaimant{holder}, BudgetMB: 4096, HeldMB: 512, BudgetSlots: 8, HeldSlots: 2}},
		typeRelease: releaseRequest{ClaimID: "c-1"},
		typeServiceAcquire: serviceAcquireRequest{Key: "pg", Service: serviceWire{Command: []string{"postgres", "-D", "/data"},
			Readiness: []string{"pg_isready"}, Stop: []string{"pg_ctl", "stop"}, IdleMS: 1_800_000}},
		typeServiceRelease: serviceReleaseRequest{Key: "pg"},
		typeServiceReply:   serviceReply{Stopped: 1},
		typeStatusReply: types.StatusBroker{PID: 100, Version: "v0.5.0", Protocol: 1, Socket: "unix:///run/user/1000/magus/broker.sock",
			Executable: "/usr/bin/magus", StartTime: since, IdleExitSeconds: 600,
			Capacity: types.MachineSnapshot{BudgetMB: 4096, HeldMB: 512, BudgetSlots: 8, HeldSlots: 2, Holders: []types.MachineClaimant{holder}},
			Services: []types.StatusService{{ID: "pg", Label: "postgres", Command: "postgres -D /data", Ports: []string{"5432"},
				State: types.ServiceRunning, Dependents: 1, StartedAt: since}}},
		typeShutdown: shutdownRequest{Magic: shutdownMagic},
		typeError:    errorReply{Code: CodeUnsupported, Message: "broker: this broker predates part of the service"},
	}
}

// TestWireStaysAdditive freezes every body on the wire. The golden is what an older
// peer sends and expects, so every member it has must still be encoded the same way
// (a newer client talking to an older broker) and must still decode to the same thing
// (an older client talking to a newer broker). A member the golden lacks is additive
// and passes.
func TestWireStaysAdditive(t *testing.T) {
	for name, v := range wireFixtures() {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("testdata", "wire", fmt.Sprintf("v%d", ProtocolVersion), name+".json")
			current, err := json.Marshal(v)
			require.NoError(t, err)
			golden, err := os.ReadFile(path)
			require.NoError(t, err, "every body on the wire has a golden")
			assertAdditive(t, golden, current, "encoding %s", name)

			decoded := reflect.New(reflect.TypeOf(v))
			require.NoError(t, json.Unmarshal(golden, decoded.Interface()))
			reencoded, err := json.Marshal(decoded.Elem().Interface())
			require.NoError(t, err)
			assertAdditive(t, golden, reencoded, "decoding %s", name)
		})
	}
}

// assertAdditive fails unless every member of golden appears in current with the same
// value, recursively.
func assertAdditive(t *testing.T, golden, current []byte, msg string, args ...any) {
	t.Helper()
	var g, c any
	require.NoError(t, json.Unmarshal(golden, &g))
	require.NoError(t, json.Unmarshal(current, &c))
	if path, ok := subset(g, c, "$"); !ok {
		t.Errorf("%s: %s changed or disappeared\n golden:  %s\n current: %s", fmt.Sprintf(msg, args...), path, golden, current)
	}
}

func subset(g, c any, path string) (string, bool) {
	switch gv := g.(type) {
	case map[string]any:
		cv, ok := c.(map[string]any)
		if !ok {
			return path, false
		}
		for k, v := range gv {
			if p, ok := subset(v, cv[k], path+"."+k); !ok {
				return p, false
			}
		}
		return "", true
	case []any:
		cv, ok := c.([]any)
		if !ok || len(cv) != len(gv) {
			return path, false
		}
		for i := range gv {
			if p, ok := subset(gv[i], cv[i], fmt.Sprintf("%s[%d]", path, i)); !ok {
				return p, false
			}
		}
		return "", true
	default:
		return path, reflect.DeepEqual(g, c)
	}
}

// TestWireFixturesCoverEveryFrameType keeps the goldens honest: a frame type added to
// wire.go without a fixture is a frame nobody froze.
func TestWireFixturesCoverEveryFrameType(t *testing.T) {
	bodiless := map[string]bool{typeReleaseReply: true, typeServiceStopAll: true, typeStatus: true, typeShutdownReply: true}
	src, err := os.ReadFile("wire.go")
	require.NoError(t, err)
	fixtures := wireFixtures()
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "type") || !strings.Contains(line, "= \"") {
			continue
		}
		name := strings.Trim(strings.TrimSpace(line[strings.Index(line, "=")+1:]), `"`)
		_, fixed := fixtures[name]
		assert.True(t, fixed || bodiless[name], "frame type %q has no wire fixture", name)
	}
}

// session is one step of a recorded conversation: a frame as a client of that version
// wrote it, and what the broker must answer.
type sessionStep struct {
	Send   json.RawMessage `json:"send"`
	Expect string          `json:"expect"`
	Code   ErrorCode       `json:"code,omitempty"`
}

// TestBrokerAnswersThePreviousProtocol enforces the compatibility rule in wire.go: the
// broker answers a client of the previous protocol version, byte for byte as that
// client wrote its frames, for at least one release after a bump. Each version's
// conversation lives in testdata/wire/v<N>/session.jsonl and is never edited once that
// version ships.
func TestBrokerAnswersThePreviousProtocol(t *testing.T) {
	previous := max(ProtocolVersion-1, 1)
	require.LessOrEqual(t, MinProtocolVersion, previous,
		"a broker keeps answering protocol %d for a release after %d ships", previous, ProtocolVersion)
	for v := previous; v <= ProtocolVersion; v++ {
		t.Run(fmt.Sprintf("v%d", v), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "wire", fmt.Sprintf("v%d", v), "session.jsonl"))
			require.NoError(t, err, "protocol %d needs a recorded session", v)
			host := &fakeHost{}
			addr := testAddr(t)
			serve(t, addr, WithServices(host), WithCapacity(4096, 8))
			conn, err := net.Dial("unix", strings.TrimPrefix(addr, "unix://"))
			require.NoError(t, err)
			defer func() { _ = conn.Close() }()
			r := newFrameReader(conn)

			for i, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
				var step sessionStep
				require.NoError(t, json.Unmarshal(line, &step), "step %d", i)
				_, err := conn.Write(append(bytes.TrimSpace(step.Send), '\n'))
				require.NoError(t, err)
				f, err := r.read()
				require.NoError(t, err, "step %d: the broker hung up", i)
				require.Equal(t, step.Expect, f.Type, "step %d: %s", i, f.Body)
				switch f.Type {
				case typeError:
					var er errorReply
					require.NoError(t, decodeBody(f, &er))
					assert.Equal(t, step.Code, er.Code, "step %d: %s", i, er.Message)
				case typeHelloReply:
					var hr helloReply
					require.NoError(t, decodeBody(f, &hr))
					assert.Equal(t, v, hr.Protocol, "the broker speaks the client's version back")
				}
			}
		})
	}
}

func TestNegotiatePicksTheNewestSharedVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    hello
		want int
	}{
		{"exact", hello{Protocol: ProtocolVersion}, ProtocolVersion},
		{"a newer client that still speaks ours", hello{Protocol: ProtocolVersion + 3, MinProtocol: ProtocolVersion}, ProtocolVersion},
		{"a newer client that no longer speaks ours", hello{Protocol: ProtocolVersion + 3, MinProtocol: ProtocolVersion + 1}, 0},
		{"a newer client that names one version", hello{Protocol: ProtocolVersion + 1}, 0},
		{"older than we answer", hello{Protocol: MinProtocolVersion - 1}, 0},
		{"no version", hello{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, negotiate(tc.h))
		})
	}
}

func TestErrorsMatchByCode(t *testing.T) {
	err := fmt.Errorf("magus: %w", &Error{Code: CodeService, Message: "readiness probe failed"})
	assert.ErrorIs(t, err, ErrService, "the code matches whatever the message says")
	assert.NotErrorIs(t, err, ErrNoServices)
	future := &Error{Code: "a-code-from-a-newer-broker", Message: "x"}
	for _, s := range []*Error{ErrProtocol, ErrMalformed, ErrUnknownType, ErrUnsupported, ErrNoServices, ErrService} {
		assert.NotErrorIs(t, future, s, "an unknown code is a failure and nothing more")
	}
}

// TestAClientRefusesAReplyOutsideItsOffer pins the client half of negotiation: a broker
// that answers with a version the client never offered is no broker for it.
func TestAClientRefusesAReplyOutsideItsOffer(t *testing.T) {
	addr := testAddr(t)
	ln, err := net.Listen("unix", strings.TrimPrefix(addr, "unix://"))
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		f, err := newFrameReader(conn).read()
		if err != nil {
			return
		}
		_ = (&frameWriter{w: conn}).write(typeHelloReply, f.ID, helloReply{PID: 1, Protocol: ProtocolVersion + 7})
		_, _ = newFrameReader(conn).read()
	}()
	_, err = Dial(t.Context(), addr)
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.ErrorIs(t, err, ErrProtocol)
}

func TestAHelloRefusalCarriesTheProtocolCode(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr)
	c := NewClient(addr)
	c.hello.Protocol, c.hello.MinProtocol = ProtocolVersion+2, ProtocolVersion+1
	_, err := c.Status(t.Context())
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.ErrorIs(t, err, ErrProtocol)
}

func TestServiceSpecCarriesOnlyWhatTheBrokerRuns(t *testing.T) {
	svc := spells.Service{
		Command: spells.Command{Bin: "postgres", Args: []string{"-D", "/data"}, Sources: []string{"*.sql"},
			Charms: map[string]spells.Charm{"rw": {}}},
		Readiness: spells.Command{Bin: "pg_isready"},
		Stop:      spells.Command{Bin: "pg_ctl", Args: []string{"stop"}},
		Distinct:  "tests need their own",
		Idle:      "30m",
	}
	spec := NewServiceSpec(svc)
	assert.Equal(t, ServiceSpec{Command: []string{"postgres", "-D", "/data"}, Readiness: []string{"pg_isready"},
		Stop: []string{"pg_ctl", "stop"}, Idle: 30 * time.Minute}, spec)
	assert.Equal(t, spells.Service{
		Command:   spells.Command{Bin: "postgres", Args: []string{"-D", "/data"}},
		Readiness: spells.Command{Bin: "pg_isready"},
		Stop:      spells.Command{Bin: "pg_ctl", Args: []string{"stop"}},
		Idle:      "30m0s",
	}, spec.Service(), "the supervisor gets back exactly what it runs")
	assert.Equal(t, ServiceSpec{Command: []string{"x"}}, NewServiceSpec(spells.Service{Command: spells.Command{Bin: "x"}, Idle: "soon"}),
		"an idle the spell spelled wrong is the broker's default, as it is in-process")

	addr := testAddr(t)
	host := &fakeHost{}
	serve(t, addr, WithServices(host))
	require.NoError(t, dial(t, addr).AcquireService(t.Context(), "pg", spec))
	host.mu.Lock()
	defer host.mu.Unlock()
	assert.Equal(t, spec, host.last, "the spec survives the wire whole")
}

// TestAServiceFieldTheBrokerPredatesIsRefused is the one strict body on the wire: a
// service the broker would run without a field the client set is refused, so the client
// runs it in-process with everything it asked for.
func TestAServiceFieldTheBrokerPredatesIsRefused(t *testing.T) {
	addr := testAddr(t)
	host := &fakeHost{}
	serve(t, addr, WithServices(host))
	c := dial(t, addr)
	cn, err := c.connect(t.Context())
	require.NoError(t, err)
	body := map[string]any{"key": "pg", "service": map[string]any{"command": []string{"postgres"}, "env": map[string]string{"PGDATA": "/d"}}}
	err = cn.roundTrip(t.Context(), c.next(), typeServiceAcquire, body, typeServiceReply, nil, nil)
	require.ErrorIs(t, err, ErrUnsupported)
	assert.Zero(t, host.count("pg"), "nothing started")

	lenient := map[string]any{"claim": map[string]any{"project": ".", "target": "t"}, "from_a_newer_client": true}
	var reply claimReply
	require.NoError(t, cn.roundTrip(t.Context(), c.next(), typeClaim, lenient, typeClaimReply, &reply, nil),
		"every other body ignores a member it does not know")
	assert.True(t, reply.Verdict.Granted)
}
