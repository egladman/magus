package std

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConformance runs all .buzz files in testdata/, registering the Buzz std
// library so imports resolve. Each file may carry header directives:
//
//	// @expect: <value>  — run and assert __r.String() == <value>
//	// @error: <substr>  — assert error contains <substr>
//	// @skip: <reason>   — skip this test case
func TestConformance(t *testing.T) {
	files, err := filepath.Glob("testdata/*.buzz")
	require.NoError(t, err)
	require.NotEmpty(t, files, "no conformance test files in testdata/")
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".buzz")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			require.NoErrorf(t, err, "read %s", path)
			meta := parseMeta(string(src))
			if meta.skip != "" {
				t.Skipf("skip: %s", meta.skip)
			}
			runCase(t, string(src), meta)
		})
	}
}

// TestRegisterNoPanic verifies that Register does not panic on a fresh session.
func TestRegisterNoPanic(t *testing.T) {
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	assert.NotPanics(t, func() { Register(sess) })
}

// TestAllModulesImportable verifies that every standard module can be imported
// by name (no file-not-found error for native modules).
func TestAllModulesImportable(t *testing.T) {
	for _, mod := range []string{"std", "math", "fs", "os", "crypto", "gc", "debug", "io", "serialize", "buffer", "ffi"} {
		t.Run(mod, func(t *testing.T) {
			sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
			defer func() { _ = sess.Close() }()
			Register(sess)
			src := fmt.Sprintf("import %q;", mod)
			require.NoErrorf(t, sess.Exec(context.Background(), src), "import %q raised error", mod)
		})
	}
}

type conformanceMeta struct {
	expect string
	errStr string
	skip   string
}

func parseMeta(src string) conformanceMeta {
	var m conformanceMeta
	scanner := bufio.NewScanner(strings.NewReader(src))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "//") {
			break
		}
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "@expect:"); ok {
			m.expect = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(line, "@error:"); ok {
			m.errStr = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(line, "@skip:"); ok {
			m.skip = strings.TrimSpace(rest)
		}
	}
	return m
}

func runCase(t *testing.T, src string, meta conformanceMeta) {
	t.Helper()
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	Register(sess)

	err := sess.Exec(context.Background(), src)
	if meta.errStr != "" {
		require.Errorf(t, err, "expected error containing %q, got nil", meta.errStr)
		require.Containsf(t, err.Error(), meta.errStr, "error %q does not contain %q", err.Error(), meta.errStr)
		return
	}
	require.NoError(t, err, "Exec")
	if meta.expect != "" {
		globals := sess.Globals()
		r, ok := globals["__r"]
		require.True(t, ok, "__r not set; did the script assign it?")
		assert.Equal(t, meta.expect, r.String(), "__r")
	}
}

// Each aliased import runs in its own sub-session, and the second to import a native
// module with declarations still needs its types after the first collected them.
func TestSession_SiblingAliasImportsEachCollectModuleDecls(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	lib := `import "serialize"; export fun amount(b: serialize\Boxed) > int { return b.integerValue(); }`
	for _, name := range []string{"first", "second"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".buzz"), []byte(lib), 0o644))
	}
	s := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithSearchPaths(filepath.Join(dir, "?.buzz")))
	Register(s)
	require.NoError(t, s.Exec(ctx, `import "first" as first; import "second" as second;`))
}

// TestEngineConformance runs the engine's own cases, ../testdata/*.buzz, through a
// std-enabled session with the same header directives TestConformance reads. It lives
// here rather than beside Session because the cases import std modules, and the
// gopherbuzz package cannot import this one back.
func TestEngineConformance(t *testing.T) {
	files, err := filepath.Glob("../testdata/*.buzz")
	require.NoError(t, err)
	require.NotEmpty(t, files, "no conformance test files found in ../testdata/")
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".buzz")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			require.NoErrorf(t, err, "read %s", path)
			meta := parseMeta(string(src))
			if meta.skip != "" {
				t.Skipf("skip: %s", meta.skip)
			}
			runCase(t, string(src), meta)
		})
	}
}

// homepageExample is the canonical program from https://buzz-lang.dev/0.5.0/,
// verbatim, with a trailing `main(["10"]);` driver line so the test can run it
// (the language has no implicit main invocation from an embedded session). It
// exercises imports, namespace `\` access, fiber return-vs-yield types
// (`void *> int?`), `yield`, the discard `_`, optional subscript (`args[?0]`),
// `??`, force-unwrap (`!`), a half-open range foreach (`0..n`), foreach over a
// fiber instance (`&fibonacci(N)`), and string interpolation.
const homepageExample = `import "std";

fun fibonacci(n: int) > void *> int? {
    var n1 = 0;
    var n2 = 1;
    var next: int? = null;

    foreach (_ in 0..n) {
        _ = yield n1;
        next = n1 + n2;
        n1 = n2;
        n2 = next!;
    }
}

fun main(args: [str]) > void {
    final N = std\parseInt(args[?0] ?? "10")!;

    foreach (n in &fibonacci(N)) {
        std\print("{n}");
    }
}

main(["10"]);
`

// TestHomepageExample is the compliance anchor: the official 0.5.0 homepage
// program must compile and run, printing the first ten Fibonacci numbers.
func TestHomepageExample(t *testing.T) {
	var out bytes.Buffer
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	RegisterWithOutput(sess, &out)

	require.NoError(t, sess.Exec(context.Background(), homepageExample), "homepage example failed to run")

	const want = "0\n1\n1\n2\n3\n5\n8\n13\n21\n34\n"
	assert.Equal(t, want, out.String(), "fibonacci output")
}

// evalWithStd is gopherbuzz's evalParity with the bundled stdlib registered, for the
// upstream-parity features that reach a host module (crypto, io, fs). The plain helper
// beside Session deliberately wires nothing, which is why these cases live here.
func evalWithStd(t *testing.T, src string) vm.Value {
	t.Helper()
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = s.Close() })
	Register(s)
	require.NoError(t, s.Exec(ctx, src), "Exec")
	probe, ok := s.Globals()["probe"]
	require.True(t, ok, "source must declare a zero-argument probe()")
	v, err := s.CallValue(ctx, probe, nil)
	require.NoError(t, err, "probe()")
	return v
}

func TestParity_SelectiveAndFlatImports(t *testing.T) {
	// A selective import binds exactly the named members, unprefixed; a flat `as _`
	// binds all of them. Both are checked AND executed: the checker must see the
	// signatures or an inferred enum case in an argument cannot resolve.
	v := evalWithStd(t, `
import print, assert from "buzz:std";
import "buzz:crypto" as _;

fun probe() > str {
    assert(true, message: "selective import binds the named member");
    return hash(.Md5, data: "abc").hex();
}`)
	assert.Equal(t, "900150983cd24fb0d6963f7d28e17f72", v.AsString(),
		"the flat import binds hash and HashAlgorithm, and the signature resolves .Md5")
}

func TestParity_HostModuleSignaturesResolveInferredEnumCases(t *testing.T) {
	// The io module's typed declaration is what lets `mode: .write` resolve; a host
	// value alone carries no parameter types.
	v := evalWithStd(t, `
import "buzz:std";
import "buzz:io";
import "buzz:fs";

fun probe() > str {
    final f = io\File.open("./parity_probe.txt", mode: .write);
    f.write("hi");
    f.close();
    final r = io\File.open("./parity_probe.txt", mode: .read);
    final got = r.readAll();
    r.close();
    fs\deleteFile("./parity_probe.txt");
    return "{got}:{fs\exists("./parity_probe.txt")}";
}`)
	assert.Equal(t, "hi:false", v.AsString(), "File.open resolves .write/.read, and deleteFile removes it")
}

func TestParity_FsDeleteDistinguishesFilesFromDirectories(t *testing.T) {
	// deleteFile and deleteDirectory are upstream's two narrow deletes; each refuses
	// the wrong kind of path rather than acting like the broader `delete`.
	v := evalWithStd(t, `
import "buzz:fs";

fun probe() > str {
    fs\makeDirectory("./parity_dir");
    // deleteFile must refuse a directory, so it is still there afterwards. An fs call
    // returns null on success too, so existence is the signal, not the return value.
    _ = fs\deleteFile("./parity_dir") catch null;
    final survived = fs\exists("./parity_dir");
    fs\deleteDirectory("./parity_dir");
    return "{survived}:{fs\exists("./parity_dir")}";
}`)
	assert.Equal(t, "true:false", v.AsString(), "deleteFile refuses a directory; deleteDirectory removes it")
}

func TestParity_CryptoHashAlgorithms(t *testing.T) {
	// One case per HashAlgorithm the enum declares, so the native switch cannot lose
	// or mis-wire an arm silently. Digests are the published test vectors for "abc".
	cases := []struct{ algo, want string }{
		{"Md5", "900150983cd24fb0d6963f7d28e17f72"},
		{"Sha1", "a9993e364706816aba3e25717850c26c9cd0d89d"},
		{"Sha224", "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7"},
		{"Sha256", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"Sha384", "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7"},
		{"Sha512", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
		{"Sha3256", "3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532"},
		{"Sha3512", "b751850b1a57168a5693cd924b6b096e08f621827444f70d884f5d0240d2712e10e116e9192af3c91a7ec57647e3934057340b4cf408d5a56592f8274eec53f0"},
	}
	for _, tc := range cases {
		t.Run(tc.algo, func(t *testing.T) {
			v := evalWithStd(t, `
import "buzz:crypto";

fun probe() > str {
    return crypto\hash(crypto\HashAlgorithm.`+tc.algo+`, data: "abc").hex();
}`)
			assert.Equal(t, tc.want, v.AsString(), "the digest matches the published vector")
		})
	}
}

// TestParity_CollectorKeepsReachableObjects covers the reachability sweep in
// vm/gc_collect.go, which had NO test at all, which is how both holes below
// survived. The dangerous direction of this check is a false COLLECT: calling a
// live object's collect() is unrecoverable, where missing a dead one merely
// delays it.
func TestParity_CollectorKeepsReachableObjects(t *testing.T) {
	cases := []struct{ name, body string }{
		{
			// The list literal is reachable ONLY through the iterator state: it is
			// bound to no local and sits on no frame's env. markReachable had no
			// tagIterState case, so collecting mid-loop reclaimed the elements the
			// loop had not reached yet.
			name: "the collection under a foreach",
			body: `
    foreach (t in [ Tracked{ id = 1 }, Tracked{ id = 2 }, Tracked{ id = 3 } ]) {
        gc\collect();
        _ = t.id;
    }
    return collected;`,
		},
		{
			// markReachable walked mo.Vals but not mo.keyVals, so an object used as
			// a KEY was invisible to the mark while the map still keyed on it.
			name: "an object used as a map key",
			body: `
    final m = mut {};
    m[Tracked{ id = 1 }] = "v";
    gc\collect();
    return collected;`,
		},
		// The fiber cases live in testdata/gc_collect_*.buzz: the fixture runner
		// builds the same session this helper does, and a file there is round-tripped
		// through the bytecode codec by TestBytecodeRoundTrip as well.
	}
	const decls = `
import "gc";

var collected = 0;

object Tracked {
    id: int,

    fun collect() > void {
        collected = collected + 1;
    }
}
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalWithStd(t, decls+"\nfun probe() > int {"+tc.body+"\n}")
			assert.Equal(t, int64(0), v.AsInt(), "a reachable object must not be collected")
		})
	}
}

// TestParity_CollectorSweepsWithoutAnExplicitCall pins the automatic sweep. The
// registry pinned every collectable until the program called gc\collect(), so a
// program that never called it retained all of them. Upstream fires collect() when
// an object becomes garbage, not when asked, so sweeping unbidden is the closer
// behaviour as well as the bounded one.
func TestParity_CollectorSweepsWithoutAnExplicitCall(t *testing.T) {
	v := evalWithStd(t, `
var collected = 0;

object Tracked {
    id: int,

    fun collect() > void {
        collected = collected + 1;
    }
}

fun probe() > int {
    var i = 0;
    // Each instance is unreachable the moment the next statement runs, and the
    // program never imports gc, let alone calls collect().
    while (i < 5000) {
        _ = Tracked{ id = i };
        i = i + 1;
    }
    return collected;
}`)
	assert.Positive(t, v.AsInt(), "an unreachable collectable must be swept without an explicit gc\\collect()")
}

func TestParity_CryptoHashReturnsRawBytes(t *testing.T) {
	// Upstream returns the raw digest and leaves rendering to `.hex()`. Returning hex
	// directly made upstream's own `hash(...).hex()` double-encode.
	v := evalWithStd(t, `
import "buzz:crypto";

fun probe() > bool {
    final raw = crypto\hash(crypto\HashAlgorithm.Md5, data: "abc");
    // The digest is NOT already hex (the regression this guards), and renders to 32
    // characters. Deliberately not asserting a raw BYTE count: str.len() counts runes,
    // so a binary digest measures shorter than its 16 bytes.
    return raw != raw.hex() and raw.hex().len() == 32;
}`)
	assert.True(t, v.AsBool(), "hash returns the digest itself; .hex() renders it as 32 characters")
}
