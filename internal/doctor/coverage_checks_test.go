package doctor

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckJSONCodec(t *testing.T) {
	got := (&runner{}).checkJSONCodec()
	assert.Equal(t, types.DoctorOK, got.Status)
	assert.True(t, strings.HasPrefix(got.Message, "encoding/json "), got.Message)
}

// graphStubWorkspace answers Graph() with a fixed error, which is the only thing
// checkGraphCycles reads.
type graphStubWorkspace struct {
	types.WorkspaceReader
	err error
}

func (g graphStubWorkspace) Graph() (*types.Graph, error) { return nil, g.err }

func TestCheckGraphCycles(t *testing.T) {
	ok := (&runner{ws: graphStubWorkspace{}}).checkGraphCycles()
	assert.Equal(t, types.DoctorOK, ok.Status)
	assert.Equal(t, "no cycles detected", ok.Message)

	// The graph builder is what detects a cycle, so its error IS the finding and has
	// to reach the report rather than being replaced with a generic message.
	bad := (&runner{ws: graphStubWorkspace{err: errors.New("cycle: a -> b -> a")}}).checkGraphCycles()
	assert.Equal(t, types.DoctorFail, bad.Status)
	assert.Equal(t, "cycle: a -> b -> a", bad.Message)
}

// TestCheckConcurrencySizing pins the machine's size through MAGUS_CONCURRENCY so the
// verdict does not depend on the CPU count of whoever runs the suite.
func TestCheckConcurrencySizing(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "4")

	sized := func(n int) types.DoctorCheck {
		return (&runner{opts: options{cfg: config.Config{Concurrency: n}}}).checkConcurrencySizing()
	}

	t.Run("unset", func(t *testing.T) {
		got := sized(0)
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "unset; sized to this machine (4)")
	})

	t.Run("matches the machine", func(t *testing.T) {
		got := sized(4)
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "4, which is what this machine sizes to")
	})

	// Advice, not fail: a deliberately small value is a legitimate choice and magus
	// cannot tell it apart from a stale one.
	t.Run("undersized", func(t *testing.T) {
		got := sized(2)
		assert.Equal(t, types.DoctorAdvice, got.Status)
		assert.Contains(t, got.Message, "undersized")
		assert.Contains(t, got.Message, "leaves capacity idle")
		assert.Equal(t, []string{"config", "set", "key=concurrency,value=4"}, got.Fix)
	})

	// The worse direction: the work still completes, just slower, so nothing ever
	// points at the cause.
	t.Run("oversized", func(t *testing.T) {
		got := sized(16)
		assert.Equal(t, types.DoctorAdvice, got.Status)
		assert.Contains(t, got.Message, "oversized")
		assert.Contains(t, got.Message, "contend rather than finish sooner")
		assert.Equal(t, []string{"config", "set", "key=concurrency,value=4"}, got.Fix)
	})
}

func TestCheckWorkspaceRegistration(t *testing.T) {
	loaded := time.Now().Add(-90 * time.Second)

	t.Run("no daemon", func(t *testing.T) {
		got := (&runner{}).checkWorkspaceRegistration()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "no loaded workspaces in daemon", got.Message)
	})

	t.Run("daemon reachable but holding nothing", func(t *testing.T) {
		r := &runner{opts: options{daemonInfo: &DaemonInfo{Reachable: true}}}
		assert.Equal(t, "no loaded workspaces in daemon", r.checkWorkspaceRegistration().Message)
	})

	t.Run("unreachable daemon", func(t *testing.T) {
		r := &runner{opts: options{daemonInfo: &DaemonInfo{
			Workspaces: []LoadedWorkspace{{Root: "/repo", LastAccess: loaded}},
		}}}
		assert.Equal(t, "no loaded workspaces in daemon", r.checkWorkspaceRegistration().Message)
	})

	t.Run("registered", func(t *testing.T) {
		r := &runner{root: "/repo", opts: options{daemonInfo: &DaemonInfo{
			Reachable:  true,
			Workspaces: []LoadedWorkspace{{Root: "/repo", LastAccess: loaded}, {Root: "/other", LastAccess: loaded}},
		}}}
		got := r.checkWorkspaceRegistration()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "loaded in daemon")
		assert.Contains(t, got.Message, "(2 workspace(s) total)")
		require.Len(t, got.Details, 2)
		assert.Contains(t, got.Details[0], "/repo")
		assert.Contains(t, got.Details[0], "idle ")
	})

	// Not yet loaded is normal - a workspace loads on first use - so this stays OK
	// and only says what it sees.
	t.Run("not registered", func(t *testing.T) {
		r := &runner{root: "/repo", opts: options{daemonInfo: &DaemonInfo{
			Reachable:  true,
			Workspaces: []LoadedWorkspace{{Root: "/elsewhere", LastAccess: loaded}},
		}}}
		got := r.checkWorkspaceRegistration()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "not yet loaded in daemon")
	})

	// The daemon passes the workspace through r.ws, leaving r.root empty on that path.
	t.Run("root comes from the workspace when set", func(t *testing.T) {
		r := &runner{ws: rootStubWorkspace{root: "/repo"}, opts: options{daemonInfo: &DaemonInfo{
			Reachable:  true,
			Workspaces: []LoadedWorkspace{{Root: "/repo", LastAccess: loaded}},
		}}}
		assert.Contains(t, r.checkWorkspaceRegistration().Message, "loaded in daemon")
	})
}

func TestSockDirOrDefault(t *testing.T) {
	var absent *DaemonInfo
	assert.Equal(t, "", absent.sockDirOrDefault())
	assert.Equal(t, "", (&DaemonInfo{}).sockDirOrDefault())
	assert.Equal(t, "/run/magus", (&DaemonInfo{SockDir: "/run/magus"}).sockDirOrDefault())
}

// listenUnix opens a real socket so the dial probe has something live to find. macOS
// caps a Unix socket path near 104 bytes and a temp dir can exceed it, so a failure to
// bind is reported as an environment skip rather than as a defect in the check.
func listenUnix(t *testing.T, path string) {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("cannot bind a unix socket at %s: %v", path, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
}

func TestIsSocketAlive(t *testing.T) {
	dir := t.TempDir()

	dead := filepath.Join(dir, "dead.sock")
	require.NoError(t, os.WriteFile(dead, nil, 0o644))
	assert.False(t, isSocketAlive(context.Background(), dead), "a plain file is not a listener")
	assert.False(t, isSocketAlive(context.Background(), filepath.Join(dir, "absent.sock")))

	live := filepath.Join(dir, "live.sock")
	listenUnix(t, live)
	assert.True(t, isSocketAlive(context.Background(), live))
}

func TestCheckStaleSockets(t *testing.T) {
	t.Run("no socket directory configured", func(t *testing.T) {
		got := (&runner{}).checkStaleSockets()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "no socket directory", got.Message)
	})

	t.Run("socket directory does not exist", func(t *testing.T) {
		r := &runner{opts: options{daemonInfo: &DaemonInfo{SockDir: filepath.Join(t.TempDir(), "absent")}}}
		got := r.checkStaleSockets()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "no socket directory", got.Message)
	})

	t.Run("empty directory", func(t *testing.T) {
		r := &runner{opts: options{daemonInfo: &DaemonInfo{SockDir: t.TempDir()}}}
		got := r.checkStaleSockets()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "0 live socket(s)", got.Message)
	})

	// Leftover dead sockets are harmless cruft, so they are context rather than a
	// failure. Anything not named magus-*.sock, and any directory, is not ours.
	t.Run("stale sockets are reported, not failed", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magus-a.sock"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magus-b.sock"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.sock"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magus-notasocket"), nil, 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "magus-dir.sock"), 0o755))

		got := (&runner{opts: options{daemonInfo: &DaemonInfo{SockDir: dir}}}).checkStaleSockets()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "2 stale socket(s)", got.Message)
		require.Len(t, got.Details, 2)
		for _, d := range got.Details {
			assert.True(t, strings.HasPrefix(d, "stale: "), d)
		}
	})

	// Multiple live daemons is a real conflict, and the only shape that fails.
	t.Run("two live daemons", func(t *testing.T) {
		dir := t.TempDir()
		listenUnix(t, filepath.Join(dir, "magus-a.sock"))
		listenUnix(t, filepath.Join(dir, "magus-b.sock"))

		got := (&runner{opts: options{daemonInfo: &DaemonInfo{SockDir: dir}}}).checkStaleSockets()
		assert.Equal(t, types.DoctorFail, got.Status)
		assert.Contains(t, got.Message, "multiple daemons running")
		require.Len(t, got.Details, 2)
		for _, d := range got.Details {
			assert.True(t, strings.HasPrefix(d, "live: "), d)
		}
	})
}

func TestCheckStaleShadowAcks(t *testing.T) {
	acks := config.Config{Spells: config.SpellsConfig{AllowShadow: []config.ShadowAck{
		{Name: "spells/hello", Reason: "the nested copy is deliberate"},
		{Name: "spells/world", Reason: "ditto"},
	}}}

	t.Run("nothing acknowledged", func(t *testing.T) {
		got := (&runner{}).checkStaleShadowAcks()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "no allow_shadow entries", got.Message)
	})

	t.Run("workspace not loaded", func(t *testing.T) {
		got := (&runner{opts: options{cfg: acks}}).checkStaleShadowAcks()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "workspace not loaded", got.Message)
	})

	// An acknowledgment whose shadow is gone is dead config: the reason it carries no
	// longer describes anything, which is what keeps the opt-out list meaningful.
	t.Run("every ack is stale", func(t *testing.T) {
		r := &runner{ws: rootStubWorkspace{root: t.TempDir()}, opts: options{cfg: acks}}
		got := r.checkStaleShadowAcks()
		assert.Equal(t, types.DoctorFail, got.Status)
		assert.Contains(t, got.Message, "2 allow_shadow entr(ies) no longer match a real shadow")
		require.Len(t, got.Details, 2)
		// Sorted, so the report does not reorder between runs over the same config.
		assert.Contains(t, got.Details[0], `"spells/hello" no longer shadows anything`)
		assert.Contains(t, got.Details[0], "the nested copy is deliberate")
		assert.Contains(t, got.Details[1], `"spells/world"`)
	})
}

func TestCheckSpellContract(t *testing.T) {
	t.Run("no spells", func(t *testing.T) {
		got := checkSpellContract(nil)
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "no spells registered", got.Message)
	})

	// The one thing no spell can function without, and the only required half of the
	// contract.
	t.Run("unnamed spell", func(t *testing.T) {
		got := checkSpellContract([]*spells.Spell{spells.NewSpell("")})
		assert.Equal(t, types.DoctorFail, got.Status)
		assert.Contains(t, got.Message, "do not satisfy the required mgs_ contract")
		require.Len(t, got.Details, 1)
		assert.Contains(t, got.Details[0], "<unnamed>: mgs_getName returned an empty name")
	})

	// An empty target list is NOT a defect: the magusfile spell contributes the targets
	// a magusfile exports, so it declares none statically and is entirely correct.
	t.Run("targets supplied by the magusfile", func(t *testing.T) {
		got := checkSpellContract([]*spells.Spell{spells.NewSpell("magusfile")})
		assert.Equal(t, types.DoctorOK, got.Status)
		require.Len(t, got.Details, 1)
		assert.Contains(t, got.Details[0], "targets only (no optional hooks)")
		assert.Contains(t, got.Details[0], "targets supplied by the magusfile, not the spell")
	})

	t.Run("every optional hook", func(t *testing.T) {
		full := spells.NewSpell("go",
			spells.WithTargets("build"),
			spells.WithSources("**/*.go"),
			spells.WithOutputs("bin/**"),
			spells.WithIgnoreDirs("vendor"),
			spells.WithTools(map[string]spells.Tool{
				"go": {Probe: spells.Command{Bin: "go", Args: []string{"version"}}},
			}),
			spells.WithLanguage("go"),
			spells.WithOpaque(),
		)
		got := checkSpellContract([]*spells.Spell{full})
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "1 spell(s) satisfy the required mgs_ contract")
		require.Len(t, got.Details, 1)
		assert.Equal(t, "go: needs, provides, ignore-dirs, version-probe, language, opaque", got.Details[0])
	})

	// Sorted, because the details are a coverage report a reader scans by spell name.
	t.Run("details are sorted", func(t *testing.T) {
		got := checkSpellContract([]*spells.Spell{spells.NewSpell("zig"), spells.NewSpell("acme")})
		require.Len(t, got.Details, 2)
		assert.True(t, strings.HasPrefix(got.Details[0], "acme:"), got.Details)
		assert.True(t, strings.HasPrefix(got.Details[1], "zig:"), got.Details)
	})
}

func TestCheckReadinessProbes(t *testing.T) {
	gated := []*types.Project{{
		ResolvedSpells: []*spells.Spell{
			spells.NewSpell("compose", spells.WithTools(map[string]spells.Tool{
				"docker": {Ready: spells.Command{Bin: "magus-doctor-no-such-bin", Args: []string{"info"}}},
				"plain":  {},
			})),
		},
	}}

	t.Run("no gates", func(t *testing.T) {
		got := (&runner{}).checkReadinessProbes([]*types.Project{{}})
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "no spell gates an op on a tool being reachable", got.Message)
	})

	// Doctor answers questions about the workspace, so listing the gate is the default
	// and forking the probe is opt-in. Declaring a gate is not a finding, so the listing
	// is OK: an advice nobody can clear is one every docker workspace carries forever.
	t.Run("lists the gate without running it", func(t *testing.T) {
		got := (&runner{}).checkReadinessProbes(gated)
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "1 tool(s) gated on a readiness probe", got.Message)
		require.Len(t, got.Details, 1)
		assert.Equal(t, "compose: docker gated on `magus-doctor-no-such-bin info`", got.Details[0])
	})

	// The same spell reached through two projects is one gate, not two.
	t.Run("deduplicates across projects", func(t *testing.T) {
		got := (&runner{}).checkReadinessProbes(append(gated, gated[0]))
		assert.Len(t, got.Details, 1)
	})

	// --probe means the caller asked whether their environment is ready, and the
	// honest answer for a tool that is down is no.
	t.Run("probing a tool that is down fails", func(t *testing.T) {
		got := (&runner{opts: options{probe: true}}).checkReadinessProbes(gated)
		assert.Equal(t, types.DoctorFail, got.Status)
		assert.Equal(t, "1 of 1 gated tool(s) not ready", got.Message)
		require.Len(t, got.Details, 1)
		assert.Contains(t, got.Details[0], "docker NOT ready")
	})

	// And the other half of that claim, which had no way to be reported: everything the
	// caller asked about answered, so the check passes rather than filing "0 of 1 not
	// ready" as advice.
	t.Run("probing a tool that is up passes", func(t *testing.T) {
		bin, err := exec.LookPath("true")
		if err != nil {
			t.Skip("no `true` on PATH to stand in for a healthy gate")
		}
		up := []*types.Project{{
			ResolvedSpells: []*spells.Spell{
				spells.NewSpell("compose", spells.WithTools(map[string]spells.Tool{
					"docker": {Ready: spells.Command{Bin: bin}},
				})),
			},
		}}
		got := (&runner{opts: options{probe: true}}).checkReadinessProbes(up)
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "1 gated tool(s), all ready", got.Message)
		assert.Equal(t, types.EvidenceMeasured, got.Evidence)
	})
}

func TestRoughly(t *testing.T) {
	// time.Duration's own String gives "4319h0m0s" at this scale, which nobody reads
	// as six months.
	assert.Equal(t, "180 days", roughly(180*24*time.Hour))
	assert.Equal(t, "2 days", roughly(48*time.Hour))
	assert.Equal(t, "47 hours", roughly(47*time.Hour))
	assert.Equal(t, "2 hours", roughly(2*time.Hour))
	assert.Equal(t, "under an hour", roughly(30*time.Minute))
	assert.Equal(t, "under an hour", roughly(0))
}
