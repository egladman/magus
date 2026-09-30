package buzz

import (
	"context"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memBytecodeStore struct {
	blobs map[string][]byte
}

func (m *memBytecodeStore) Load(key string) ([]byte, error) {
	b, ok := m.blobs[key]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return b, nil
}

func (m *memBytecodeStore) Store(key string, blob []byte) error {
	if m.blobs == nil {
		m.blobs = map[string][]byte{}
	}
	m.blobs[key] = append([]byte(nil), blob...)
	return nil
}

type compileCounter struct{ n int }

func (c *compileCounter) Phase(phase CompilePhase, _ time.Duration, _ error) {
	if phase == PhaseCompile {
		c.n++
	}
}

func (c *compileCounter) Import(string, ImportOutcome, time.Duration, error) {}

// cachingSession is a session on store that resolves imports from dir only and
// reports each value the program passes to report().
func cachingSession(t *testing.T, store BytecodeStore, dir string, got *[]string) *Session {
	t.Helper()
	sess := NewSession(context.Background(), WithEmbedded(), WithSearchPaths(filepath.Join(dir, "?.buzz")))
	t.Cleanup(func() { _ = sess.Close() })
	sess.SetIncludeDirs(nil)
	sess.SetBytecodeStore(store)
	sess.SetGlobal("report", vmpackage.DirectValue("report", func(_ context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
		for _, a := range args {
			*got = append(*got, a.String())
		}
		return vmpackage.Null, nil
	}))
	return sess
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
	}
}

func TestBytecodeStoreSecondExecDoesNotCompile(t *testing.T) {
	store := &memBytecodeStore{}
	var ticks int
	run := func() int {
		t.Helper()
		counter := &compileCounter{}
		sess := NewSession(context.Background(), WithEmbedded())
		t.Cleanup(func() { _ = sess.Close() })
		sess.SetBytecodeStore(store)
		sess.SetCompileObserver(counter)
		sess.SetGlobal("tick", vmpackage.DirectValue("tick", func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
			ticks++
			return vmpackage.Null, nil
		}))
		require.NoError(t, sess.Exec(context.Background(), "tick();"))
		return counter.n
	}
	assert.Positive(t, run())
	assert.Zero(t, run())
	assert.Equal(t, 2, ticks)
}

func TestBytecodeStoreEntryFilterIsPartOfTheKey(t *testing.T) {
	store := &memBytecodeStore{}
	keepAll := func(*ast.Program, ImportLookup) {}
	run := func(id string) int {
		t.Helper()
		counter := &compileCounter{}
		sess := NewSession(context.Background(), WithEmbedded())
		t.Cleanup(func() { _ = sess.Close() })
		sess.SetBytecodeStore(store)
		if id != "" {
			sess.SetEntryFilter(id, keepAll)
		}
		sess.SetCompileObserver(counter)
		require.NoError(t, sess.Exec(context.Background(), "var n = 1;"))
		return counter.n
	}
	assert.Positive(t, run("a"))
	assert.Zero(t, run("a"))
	assert.Positive(t, run("b"), "a chunk compiled through one filter is not served to another")
	assert.Positive(t, run(""), "nor to a session with none")
	assert.Panics(t, func() {
		NewSession(context.Background()).SetEntryFilter("", keepAll)
	})
}

// A filter's decision rests on the files it read, found or not, so a change to
// either recompiles an entry whose own source did not change.
func TestBytecodeStoreFilterReadsJoinTheClosure(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"dep.buzz": "var x = 1;\n"})
	store := &memBytecodeStore{}
	var found []bool
	filter := func(_ *ast.Program, lookup ImportLookup) {
		_, _, ok := lookup("dep")
		found = append(found, ok)
		lookup("later")
	}
	run := func() int {
		t.Helper()
		var got []string
		sess := cachingSession(t, store, dir, &got)
		counter := &compileCounter{}
		sess.SetCompileObserver(counter)
		sess.SetEntryFilter("test", filter)
		require.NoError(t, sess.Exec(context.Background(), "var n = 1;"))
		return counter.n
	}
	assert.Positive(t, run())
	assert.Zero(t, run())
	writeFiles(t, dir, map[string]string{"dep.buzz": "var x = 2;\n"})
	assert.Positive(t, run(), "a file the filter read changed")
	assert.Zero(t, run())
	writeFiles(t, dir, map[string]string{"later.buzz": "var y = 1;\n"})
	assert.Positive(t, run(), "a file the filter found missing appeared")
	assert.Equal(t, []bool{true, true, true}, found)
}

func TestBytecodeStoreImportChangeRecompiles(t *testing.T) {
	dir := t.TempDir()
	dep := filepath.Join(dir, "dep.buzz")
	nested := filepath.Join(dir, "nested.buzz")
	require.NoError(t, os.WriteFile(nested, []byte("fun ready() > void { tick(); }\nready();\n"), 0o644))
	require.NoError(t, os.WriteFile(dep, []byte("import \"nested\";\nexport fun ping() > void { tick(); }\n"), 0o644))
	store := &memBytecodeStore{}
	var ticks int
	run := func() int {
		t.Helper()
		counter := &compileCounter{}
		sess := NewSession(context.Background(), WithEmbedded())
		t.Cleanup(func() { _ = sess.Close() })
		sess.SetIncludeDirs([]string{dir})
		sess.SetBytecodeStore(store)
		sess.SetCompileObserver(counter)
		sess.SetGlobal("tick", vmpackage.DirectValue("tick", func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
			ticks++
			return vmpackage.Null, nil
		}))
		require.NoError(t, sess.Exec(context.Background(), "import \"dep\" as dep;\ndep\\ping();\n"))
		return counter.n
	}
	first := run()
	assert.Equal(t, 3, first, "entry, dep, and nested each compile once")
	assert.Zero(t, run())
	require.NoError(t, os.WriteFile(nested, []byte("fun ready() > void { tick(); tick(); }\nready();\n"), 0o644))
	assert.Equal(t, first, run(), "a changed import invalidates the chunks compiled against it")
	assert.Equal(t, 7, ticks)
}

// a binds b as already imported, and its chunk still bakes in b's field
// defaults, so an edit to b must invalidate a's chunk.
func TestBytecodeStoreBoundImportIsInTheClosure(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"b.buzz": "export object Cfg { mode: str = \"warn\" }\n",
		"a.buzz": "import \"b\";\nexport fun mode() > str { return Cfg{}.mode; }\n",
	})
	store := &memBytecodeStore{}
	run := func() []string {
		t.Helper()
		var got []string
		sess := cachingSession(t, store, dir, &got)
		require.NoError(t, sess.Exec(context.Background(), "import \"b\";\nimport \"a\";\nreport(mode());\n"))
		return got
	}
	assert.Equal(t, []string{"warn"}, run())
	writeFiles(t, dir, map[string]string{"b.buzz": "export object Cfg { mode: str = \"deny\" }\n"})
	assert.Equal(t, []string{"deny"}, run())
}

// A file that now shadows a transitive import changes what a fresh compile
// sees, though every recorded file still has its bytes.
func TestBytecodeStoreShadowedTransitiveImportRecompiles(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"c.buzz":     "export object Cfg { mode: str = \"warn\" }\n",
		"lib/a.buzz": "import \"c\";\nexport fun ready() > bool { return true; }\n",
	})
	store := &memBytecodeStore{}
	run := func() []string {
		t.Helper()
		var got []string
		sess := cachingSession(t, store, dir, &got)
		require.NoError(t, sess.Exec(context.Background(), "import \"lib/a\";\nreport(Cfg{}.mode);\n"))
		return got
	}
	assert.Equal(t, []string{"warn"}, run())
	writeFiles(t, dir, map[string]string{"lib/c.buzz": "export object Cfg { mode: str = \"deny\" }\n"})
	assert.Equal(t, []string{"deny"}, run())
}

// The directory form runs several entries in one session. One entry's replay
// binds an import the next, compiled, entry then finds already bound.
func TestBytecodeStoreReplayKeepsImportTypes(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"lib.buzz": "export object Obj { x: int = 7 }\n"})
	store := &memBytecodeStore{}
	const first = "import \"lib\";\nreport(1);\n"
	run := func(second string) []string {
		t.Helper()
		var got []string
		sess := cachingSession(t, store, dir, &got)
		require.NoError(t, sess.Exec(context.Background(), first))
		require.NoError(t, sess.Exec(context.Background(), second))
		return got
	}
	run("import \"lib\";\nreport(lib\\Obj{}.x);\n")
	assert.Equal(t, []string{"1", "8"}, run("import \"lib\";\nreport(lib\\Obj{}.x + 1);\n"))
}

// A module's private names stay hidden from its importer's checker whether
// the module compiled or came from the store.
func TestBytecodeStoreHitKeepsImportPrivate(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"m.buzz": "var secret = 1;\nexport fun open() > int { return secret; }\n"})
	const peek = "import \"m\";\nreport(secret);\n"
	var got []string
	err := cachingSession(t, &memBytecodeStore{}, dir, &got).Exec(context.Background(), peek)
	require.ErrorContains(t, err, "undefined: secret", "cold, the private name is hidden")

	store := &memBytecodeStore{}
	require.NoError(t, cachingSession(t, store, dir, &got).Exec(context.Background(), "import \"m\";\nreport(open());\n"))
	err = cachingSession(t, store, dir, &got).Exec(context.Background(), peek)
	require.ErrorContains(t, err, "undefined: secret", "warm, m's chunk comes from the store")
}

// A stored chunk reports the source lines and file a compiled one does.
func TestBytecodeStoreHitKeepsSourceLines(t *testing.T) {
	dir := t.TempDir()
	store := &memBytecodeStore{}
	const code = "final xs = [1];\n\nfun at(i: int) > int {\n  return xs[i];\n}\nreport(at(0));\n"
	run := func() []string {
		t.Helper()
		var got, lines []string
		sess := cachingSession(t, store, dir, &got)
		sess.SetSourceFile("entry.buzz")
		sess.SetStepHook(vmpackage.MaskLine, func(_ vmpackage.StepEvent, f vmpackage.DebugFrame) {
			lines = append(lines, fmt.Sprintf("%s:%d", f.SourceFile, f.Line))
		})
		require.NoError(t, sess.Exec(context.Background(), code))
		return lines
	}
	cold := run()
	require.Contains(t, cold, "entry.buzz:4")
	assert.Equal(t, cold, run())
}

func TestBytecodeStoreCorruptBlobCompiles(t *testing.T) {
	store := &memBytecodeStore{}
	sess := NewSession(context.Background(), WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	sess.SetBytecodeStore(store)
	require.NoError(t, sess.Exec(context.Background(), "var n = 1;"))
	require.NotEmpty(t, store.blobs)
	for k := range store.blobs {
		store.blobs[k] = []byte("not a record")
	}
	counter := &compileCounter{}
	again := NewSession(context.Background(), WithEmbedded())
	t.Cleanup(func() { _ = again.Close() })
	again.SetBytecodeStore(store)
	again.SetCompileObserver(counter)
	require.NoError(t, again.Exec(context.Background(), "var n = 1;"))
	assert.Positive(t, counter.n)
}

// A count is checked against the bytes left before anything is allocated for it.
func TestDecodeBytecodeRecordRejectsAnOversizedCount(t *testing.T) {
	blob := append([]byte{}, bytecodeRecordMagic[:]...)
	blob = binary.LittleEndian.AppendUint16(blob, bytecodeRecordVersion)
	blob = binary.LittleEndian.AppendUint32(blob, 0xFFFFF)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := decodeBytecodeRecord(blob)
	runtime.ReadMemStats(&after)
	require.Error(t, err)
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20))
}

func TestDiskBytecodeStoreRoundTrip(t *testing.T) {
	store := NewDiskBytecodeStore(t.TempDir())
	require.NoError(t, store.Store("abc", []byte("chunk")))
	blob, err := store.Load("abc")
	require.NoError(t, err)
	assert.Equal(t, []byte("chunk"), blob)
	_, err = store.Load("missing")
	require.ErrorIs(t, err, fs.ErrNotExist)
}
