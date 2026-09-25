package bindings

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/sandbox"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// readOnlyReads are the members of the modules RestrictToReads walks that stay callable
// read-only. With readOnlyRefused and readOnlyGated they classify every function member,
// so a member added to a writing module fails TestReadOnlyClassifiesEveryMember until
// someone decides which it is.
var readOnlyReads = []string{
	"archive.list", "archive.readFile",

	"crypto.base64DecodeBytes", "crypto.base64EncodeBytes", "crypto.hash", "crypto.hmacSha256",
	"crypto.hmacSha256Hex", "crypto.md5File", "crypto.md5Hex", "crypto.publicKey",
	"crypto.sha1File", "crypto.sha1Hex", "crypto.sha256File", "crypto.sha256Hex",
	"crypto.sha512File", "crypto.sha512Hex", "crypto.sign", "crypto.verify",

	"fs.basename", "fs.currentDirectory", "fs.dirname", "fs.exists", "fs.ext", "fs.glob",
	"fs.isDir", "fs.isFile", "fs.join", "fs.list", "fs.listDir", "fs.modified", "fs.readFile",
	"fs.readLines", "fs.readlink", "fs.size", "fs.stat", "fs.walk", "fs.watch",

	"http.byteSize", "http.get",

	"io.runFile", "iocore.runFile",

	"magus.cache.remote", "magus.canonicalName", "magus.ci.provider", "magus.describeModule",
	"magus.fatal", "magus.guard.advise", "magus.guard.allow", "magus.guard.bash",
	"magus.guard.command", "magus.guard.count", "magus.guard.deny", "magus.guard.once",
	"magus.guard.shell", "magus.guard.spawn", "magus.guard.write", "magus.harness.provider",
	"magus.hasCharm", "magus.job.list", "magus.job.wait", "magus.log.debug", "magus.log.error",
	"magus.log.hint", "magus.log.info", "magus.log.warn", "magus.project", "magus.projectGraph",
	"magus.projects", "magus.pry", "magus.raise", "magus.review.provider", "magus.secret.endpoint",
	"magus.secret.provider", "magus.secret.read", "magus.targets", "magus.where",
	"magus.workspace.provider",

	"net.freePort", "net.isPortOpen", "net.waitForPort",

	"os.env", "os.executable", "os.exit", "os.hostname", "os.numCpu", "os.platform", "os.retry",
	"os.sleep", "os.time", "os.tmpDir", "os.withEnv",

	"pipe.all", "pipe.emit", "pipe.history", "pipe.more", "pipe.next", "pipe.outputs", "pipe.value",

	"proc.stdinIsTerminal", "proc.which", "proc.withSlots",

	"term.clearScreen", "term.colorize", "term.isInteractive", "term.notify", "term.pick",
	"term.size", "term.wantsColor",
}

// readOnlyStdio are the three standard streams io and iocore both carry; writing to one
// is output, not a change on disk.
var readOnlyStdio = []string{"close", "collect", "isTTY", "read", "readAll", "readLine", "write"}

// surfaceFunctions lists every function member of the session's non-pure native modules
// by the key readOnlyRefused uses.
func surfaceFunctions(t *testing.T, sess *buzz.Session) []string {
	t.Helper()
	var out []string
	var walk func(key string, mod vm.Value)
	walk = func(key string, mod vm.Value) {
		for _, member := range mod.MapKeys() {
			v, _ := mod.MapGet(member)
			switch {
			case v.IsMap():
				walk(key+"."+member, v)
			case v.IsFun():
				out = append(out, key+"."+member)
			}
		}
	}
	for name, pure := range readOnlyModuleNames() {
		if mod, ok := sess.NativeModule(name); ok && !pure {
			walk(name, mod)
		}
	}
	slices.Sort(out)
	return out
}

func fullSurface(ctx context.Context, out *bytes.Buffer) *buzz.Session {
	sess := buzz.NewSession(ctx)
	RegisterModuleSurface(ctx, sess, WithScriptOutput(out))
	RegisterMagusNamespace(ctx, sess)
	RegisterSpellSourceModules(sess)
	return sess
}

func TestReadOnlyClassifiesEveryMember(t *testing.T) {
	sess := fullSurface(context.Background(), &bytes.Buffer{})
	t.Cleanup(func() { _ = sess.Close() })

	classified := map[string]bool{}
	for _, m := range readOnlyReads {
		classified[m] = true
	}
	for _, stream := range []string{"stdin", "stdout", "stderr"} {
		for _, m := range readOnlyStdio {
			classified["io."+stream+"."+m] = true
			classified["iocore."+stream+"."+m] = true
		}
	}
	for m := range readOnlyRefused {
		classified[m] = true
	}
	for m := range readOnlyGated {
		classified[m] = true
	}

	surface := surfaceFunctions(t, sess)
	for _, m := range surface {
		assert.True(t, classified[m], "%s is neither refused nor listed as a read; decide which in readonly.go", m)
	}
	for m := range classified {
		assert.Contains(t, surface, m, "%s is classified but no module has it", m)
	}
}

// readOnlySession is the session `magus buzz --read-only` builds: the full surface,
// restricted, over a context carrying the read-only policy.
func readOnlySession(t *testing.T, out *bytes.Buffer) (context.Context, *buzz.Session) {
	t.Helper()
	ctx := sandbox.WithPolicy(context.Background(), sandbox.ReadOnly(nil))
	sess := fullSurface(ctx, out)
	t.Cleanup(func() { _ = sess.Close() })
	require.NoError(t, RestrictToReads(ctx, sess))
	return ctx, sess
}

func runMain(ctx context.Context, sess *buzz.Session, src string) error {
	if err := sess.Exec(ctx, src); err != nil {
		return err
	}
	_, err := sess.CallValue(ctx, sess.GetGlobal("main"), []vm.Value{vm.ListValue(nil)})
	return err
}

// Every refused member raises its code, whatever it is called with.
func TestRestrictToReadsReplacesEveryRefusedMember(t *testing.T) {
	ctx, sess := readOnlySession(t, &bytes.Buffer{})
	child := sess.NewChild()
	require.NoError(t, child.Exec(ctx, "fun call0(f: any) > any { return f(); }"))
	call0 := child.GetGlobal("call0")

	for key, why := range readOnlyRefused {
		module, rest, _ := strings.Cut(key, ".")
		mod, ok := sess.NativeModule(module)
		require.True(t, ok, key)
		v := mod
		for _, part := range strings.Split(rest, ".") {
			v, ok = v.MapGet(part)
			require.True(t, ok, key)
		}
		_, err := child.CallValue(ctx, call0, []vm.Value{v})
		want := types.PathWriteDenied
		if why == readOnlyExecs {
			want = types.ExecDenied
		}
		assert.ErrorIs(t, err, want, key)
		assert.ErrorContains(t, err, module+`\`+rest+" refused", key)
	}
}

// One script per host module that can write, each aimed at a file in dir: every one is
// refused with its code, and none of the files appears.
func TestReadOnlyScriptsCannotWriteThroughAnyModule(t *testing.T) {
	restore := RefuseFFI()
	t.Cleanup(restore)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "in.txt"), []byte("in"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "src"), 0o755))

	for name, tc := range map[string]struct {
		imports, call string
		want          types.DiagnosticCode
	}{
		"fs":         {"fs", `fs\writeFile("{d}/fs.txt", content: "x")`, types.PathWriteDenied},
		"fs remove":  {"fs", `fs\removeAll("{d}/in.txt")`, types.PathWriteDenied},
		"archive":    {"archive", `archive\compress("{d}/src", dest: "{d}/a.tar.gz")`, types.PathWriteDenied},
		"crypto":     {"crypto", `crypto\signFile(crypto\SignAlgorithm.Ed25519, path: "{d}/in.txt", key_env: "K")`, types.PathWriteDenied},
		"http":       {"http", `http\post("http://127.0.0.1:1/", body: "x")`, types.PathWriteDenied},
		"http fetch": {"http", `http\download("http://127.0.0.1:1/", dest: "{d}/dl.txt")`, types.PathWriteDenied},
		"io":         {"io", `io\File.open("{d}/io.txt", mode: io\FileMode.write)`, types.PathWriteDenied},
		"io update":  {"io", `io\File.open("{d}/in.txt", mode: io\FileMode.update)`, types.PathWriteDenied},
		"os":         {"os", `os\execute(["touch", "{d}/os.txt"])`, types.ExecDenied},
		"socket":     {"os", `os\Socket.connect(os\SocketProtocol.tcp, host: "127.0.0.1", port: 1)`, types.PathWriteDenied},
		"proc":       {"proc", `proc\exec("touch", args: ["{d}/proc.txt"])`, types.ExecDenied},
		"pipe":       {"pipe", `pipe\exportTo(pipe\Artifact{ path = "x" }, dest: "{d}/pipe.txt")`, types.PathWriteDenied},
		"vcs":        {"vcs", `vcs\cmd(["init", "{d}/repo"])`, types.ExecDenied},
		"magus run":  {"magus", `magus\run(["build"])`, types.ExecDenied},
		"magus job":  {"magus", `magus\job.put("x")`, types.PathWriteDenied},
		"cache":      {"magus", `magus\bustCache()`, types.PathWriteDenied},
		"zdef":       {"std", `zdef("libc", "int unlink(char *path);")`, types.ExecDenied},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, sess := readOnlySession(t, &bytes.Buffer{})
			local := "d"
			if !strings.Contains(tc.call, "{d}") {
				local = "_d"
			}
			src := fmt.Sprintf("import %q;\nfun main(args: [str]) > void !> any {\n    final %s = %q;\n    %s;\n}\n",
				tc.imports, local, dir, tc.call)
			err := runMain(ctx, sess, src)
			assert.ErrorIs(t, err, tc.want, "%v", err)
		})
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{"in.txt", "src"}, names, "a read-only script changed the directory")
}

// A read-only script still reads, through fs and through io, and prints.
func TestReadOnlyScriptsReadAndPrint(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "in.txt"), []byte("hello"), 0o644))
	var out bytes.Buffer
	ctx, sess := readOnlySession(t, &out)

	err := runMain(ctx, sess, fmt.Sprintf(`import "std"; import "fs"; import "io";
fun main(args: [str]) > void !> any {
    final d = %q;
    final f = io\File.open("{d}/in.txt", mode: io\FileMode.read);
    std\print("{fs\readFile("{d}/in.txt")} {f.readAll() ?? ""}");
    f.close();
}
`, dir))

	require.NoError(t, err)
	assert.Equal(t, "hello hello\n", out.String())
}

// The member table is one layer. A member it does not know that writes through the
// sandbox checks is still refused by the read-only policy on the context.
func TestReadOnlyPolicyRefusesWhatTheTableMisses(t *testing.T) {
	dir := t.TempDir()
	ctx := sandbox.WithPolicy(context.Background(), sandbox.ReadOnly(nil))
	sess := fullSurface(ctx, &bytes.Buffer{})
	t.Cleanup(func() { _ = sess.Close() })

	err := runMain(ctx, sess, fmt.Sprintf(`import "fs";
fun main(args: [str]) > void !> any { fs\writeFile(%q, content: "x"); }
`, filepath.Join(dir, "out.txt")))

	assert.ErrorIs(t, err, types.PathWriteDenied)
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
}

// Restricting one session leaves every other session's modules whole: the server runs
// sessions side by side in one process.
func TestRestrictToReadsTouchesOnlyItsSession(t *testing.T) {
	readOnlySession(t, &bytes.Buffer{})
	dir := t.TempDir()
	sess := fullSurface(context.Background(), &bytes.Buffer{})
	t.Cleanup(func() { _ = sess.Close() })

	err := runMain(context.Background(), sess, fmt.Sprintf(`import "fs";
fun main(args: [str]) > void !> any { fs\writeFile(%q, content: "x"); }
`, filepath.Join(dir, "out.txt")))

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "out.txt"))
}

func TestRefuseFFIRestores(t *testing.T) {
	prev := vm.GetFFIProvider()
	restore := RefuseFFI()
	_, err := vm.GetFFIProvider().OpenLibrary("libc", nil)
	assert.ErrorIs(t, err, types.ExecDenied)
	restore()
	assert.Equal(t, prev, vm.GetFFIProvider())
}
