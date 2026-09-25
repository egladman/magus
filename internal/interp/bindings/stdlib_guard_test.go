package bindings

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/sandbox"
	sandboxenv "github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guardFixture is a session with the full module surface, a workspace the policy grants,
// and a directory outside it.
type guardFixture struct {
	sess         *buzz.Session
	ws, outside  string
	confined     context.Context
	unrestricted context.Context
}

func newGuardFixture(t *testing.T, rules ...filesystem.Rule) guardFixture {
	t.Helper()
	ws := filesystem.ResolveRulePath(t.TempDir())
	outside := filesystem.ResolveRulePath(t.TempDir())
	sess := buzz.NewSession(t.Context(), buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModuleSurface(t.Context(), sess)
	base := []filesystem.Rule{{Path: ws, Read: true, Write: true}}
	// A child the kernel confines needs its ELF interpreter and libc too.
	for _, dir := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64"} {
		base = append(base, filesystem.Rule{Path: filesystem.ResolveRulePath(dir), Read: true, Exec: true})
	}
	p := &sandbox.Policy{
		Workspace: ws,
		FS:        filesystem.Ruleset{Rules: append(base, rules...)},
		Env:       sandboxenv.Allowlist{Names: []string{"PATH"}},
		BaseEnv:   []string{"PATH=" + os.Getenv("PATH")},
	}
	return guardFixture{
		sess: sess, ws: ws, outside: outside,
		confined:     std.WithCwd(sandbox.WithPolicy(t.Context(), p), ws),
		unrestricted: std.WithCwd(t.Context(), ws),
	}
}

func (f guardFixture) call(t *testing.T, ctx context.Context, module, name string, args ...vm.Value) (vm.Value, error) {
	t.Helper()
	mod, ok := f.sess.NativeModule(module)
	require.True(t, ok, module)
	fn, ok := mod.MapGet(name)
	require.True(t, ok, module+"."+name)
	// Through the trampoline the guard compiled: CallValue on a native function drops its
	// result.
	tramp := f.sess.GetGlobal(upstreamTrampolineNames[len(args)])
	require.True(t, tramp.IsFun())
	return f.sess.CallValue(ctx, tramp, append([]vm.Value{fn}, args...))
}

// TestUpstreamOsEnvHonorsTheAllowlist: os.env read the raw process environment, so a
// secret env.get hides was one import away.
func TestUpstreamOsEnvHonorsTheAllowlist(t *testing.T) {
	f := newGuardFixture(t)
	t.Setenv("MAGUS_GUARD_SECRET", "hunter2")

	got, err := f.call(t, f.confined, "os", "env", vm.StrValue("MAGUS_GUARD_SECRET"))
	require.NoError(t, err)
	assert.True(t, got.IsNull(), "a stripped name reads as unset")

	got, err = f.call(t, f.unrestricted, "os", "env", vm.StrValue("MAGUS_GUARD_SECRET"))
	require.NoError(t, err)
	assert.Equal(t, "hunter2", got.AsString(), "with no policy os.env is upstream's")
}

// TestUpstreamFsIsChecked: Buzz's own fs functions wrote and deleted wherever the
// process could.
func TestUpstreamFsIsChecked(t *testing.T) {
	f := newGuardFixture(t)
	victim := filepath.Join(f.outside, "keep.txt")
	require.NoError(t, os.WriteFile(victim, []byte("x"), 0o644))

	for _, c := range []struct {
		name string
		args []vm.Value
		want types.DiagnosticCode
	}{
		{"delete", []vm.Value{vm.StrValue(victim)}, types.PathWriteDenied},
		{"deleteFile", []vm.Value{vm.StrValue(victim)}, types.PathWriteDenied},
		{"makeDirectory", []vm.Value{vm.StrValue(filepath.Join(f.outside, "d"))}, types.PathWriteDenied},
		{"move", []vm.Value{vm.StrValue("in.txt"), vm.StrValue(filepath.Join(f.outside, "moved"))}, types.PathWriteDenied},
		{"list", []vm.Value{vm.StrValue(f.outside)}, types.PathReadDenied},
		{"modified", []vm.Value{vm.StrValue(victim)}, types.PathReadDenied},
	} {
		_, err := f.call(t, f.confined, "fs", c.name, c.args...)
		require.ErrorIs(t, err, c.want, "fs.%s", c.name)
	}
	assert.FileExists(t, victim)

	_, err := f.call(t, f.confined, "fs", "makeDirectory", vm.StrValue("inside"))
	require.NoError(t, err, "a path the policy grants still works")
	assert.DirExists(t, filepath.Join(f.ws, "inside"))
}

// TestUpstreamFileOpenIsChecked covers io.File.open under both names gopherbuzz binds it.
func TestUpstreamFileOpenIsChecked(t *testing.T) {
	f := newGuardFixture(t)
	secret := filepath.Join(f.outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("x"), 0o644))

	for _, module := range []string{"io", "iocore"} {
		for _, mode := range []string{"read", "write", "update"} {
			src := "import \"" + module + "\";\nfinal f = " + module + "\\File.open(\"" + secret + "\", " + module + "\\FileMode." + mode + ");\n"
			err := f.sess.Exec(f.confined, src)
			require.Error(t, err, "%s File.open %s", module, mode)
			assert.Contains(t, err.Error(), "denied", "%s File.open %s", module, mode)
		}
	}
	data, err := os.ReadFile(secret)
	require.NoError(t, err)
	assert.Equal(t, "x", string(data), "a refused write mode must not have truncated the file")

	require.NoError(t, f.sess.Exec(f.confined,
		"import \"io\";\nfinal f = io\\File.open(\"inside.txt\", io\\FileMode.write);\nf.close();\n"),
		"a path the policy grants still opens")
}

// TestUpstreamOsExecuteGoesThroughRunExec: os.execute forked with the whole process
// environment, proc socket included, and with no exec check.
func TestUpstreamOsExecuteGoesThroughRunExec(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("'sh' not available")
	}
	t.Setenv("MAGUS_PROC_SOCKET", "unix:///tmp/parent.sock")
	t.Setenv("MAGUS_GUARD_SECRET", "hunter2")
	cmd := vm.ListValue([]vm.Value{
		vm.StrValue("sh"), vm.StrValue("-c"),
		vm.StrValue(`printf '%s|%s' "$MAGUS_PROC_SOCKET" "$MAGUS_GUARD_SECRET" > out.txt`),
	})

	denied := newGuardFixture(t)
	_, err = denied.call(t, denied.confined, "os", "execute", cmd)
	require.ErrorIs(t, err, types.ExecDenied, "no exec grant, no exec")

	resolved, err := filepath.EvalSymlinks(sh)
	require.NoError(t, err)
	f := newGuardFixture(t, filesystem.Rule{Path: filepath.Dir(resolved), Read: true, Exec: true})
	code, err := f.call(t, f.confined, "os", "execute", cmd)
	require.NoError(t, err)
	assert.Equal(t, int64(0), code.AsInt())
	out, err := os.ReadFile(filepath.Join(f.ws, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "|", strings.TrimSpace(string(out)), "neither the socket nor a stripped secret reaches the child")
}
