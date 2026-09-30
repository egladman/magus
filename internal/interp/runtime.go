package interp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: a content fingerprint; see ContentID
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/engine"
	buzzengine "github.com/egladman/magus/internal/interp/engine/buzz"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/parsecache"
	"github.com/egladman/magus/internal/sandbox"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

type sourceCtxKey struct{}

type projectPathCtxKey struct{}

type overlayCtxKey struct{}

type sourceReaderCtxKey struct{}

// TargetContextGlobal is the session-global name under which the bindings layer
// stashes the shared magus.Context value (see bindings.registerAllBuzz). A target
// function receives it as its first argument; execBuzzSrc fetches it with GetGlobal and
// prepends it at dispatch. The double underscore keeps it out of the way of any
// magusfile identifier.
const TargetContextGlobal = "__magus_target_context"

// CtxFormTargetKeys returns the normalized keys of the exported functions in src whose
// FIRST parameter is annotated `magus\Context` (types.ContextParamAnnotation): the target
// contract. execBuzzSrc uses it to enforce that contract at load (an exported function
// missing the context is rejected with MGS1008) and to prepend the context at dispatch.
// It does NOT build the graph: the dependency graph is read statically by
// describe.Extract, which sees ctx-form declarations directly. Best-effort: a parse
// failure yields nil, matching the extractor's never-error contract.
func CtxFormTargetKeys(src string) map[string]bool {
	prog, err := parsecache.Shared().ParseEmbedded(src)
	if err != nil || prog == nil {
		return nil
	}
	out := map[string]bool{}
	for _, stmt := range prog.Stmts {
		fd, ok := stmt.(*ast.FunDecl)
		if !ok || !fd.IsExported {
			continue
		}
		if len(fd.ParamAnnots) > 0 && fd.ParamAnnots[0] == types.ContextParamAnnotation {
			out[types.Normalize(fd.Name)] = true
		}
	}
	return out
}

// removedMagusfileAPI lists the magusfile calls magus no longer binds, each paired
// with the call that replaced it. Removing a binding is not self-reporting: Buzz
// reads a missing member as null rather than erroring, so every one of these still
// parses, still loads, and still passes `magus ls`; the magusfile only breaks when
// the target finally runs, as a bare "null is not callable" that names neither the
// call nor the migration. The needs family is worse than that, because the static
// target graph is built from `ctx.needs`: a magusfile still calling `magus.needs`
// reports NO dependency edge, so `explain`, `affected`, and scheduling all quietly
// act on a graph with the edge missing long before anything fails.
//
// Ordered longest-prefix first so `magus.target.literal` is reported as itself
// rather than as its `magus.target` prefix.
var removedMagusfileAPI = []struct {
	path        []string // member path after the `magus` root, e.g. target.literal
	replacement string
}{
	{[]string{"target", "literal"}, "pass the target function itself to ctx.needs(<target>)"},
	{[]string{"project", "register"}, `call magus\project({...}) at the top level`},
	{[]string{"needs"}, "call ctx.needs(<target>)"},
	{[]string{"glob"}, `call ctx.glob("<pattern>")`},
	{[]string{"insightMarkdown"}, `build the document from magus\insight()'s typed report`},
	{[]string{"ledger", "clear"}, `call magus\job.clear()`},
	{[]string{"ledger", "list"}, `call magus\job.list()`},
	{[]string{"ledger", "put"}, `call magus\job.put()`},
	{[]string{"ledger", "register"}, `call magus\job.register()`},
	{[]string{"ledger"}, `use magus\job, which replaced it`},
}

// RemovedAPINames returns the dotted member path of every removed call, without the
// `magus.` root (e.g. "project.register"). The surface lock test uses it to assert the
// table never names something the namespace still binds.
func RemovedAPINames() []string {
	out := make([]string, 0, len(removedMagusfileAPI))
	for _, r := range removedMagusfileAPI {
		out = append(out, strings.Join(r.path, "."))
	}
	return out
}

// RemovedAPICall reports the first removed magusfile API call in src, with the call
// that replaced it. ok is false when src uses none of them.
//
// Two readings, because a stale magusfile fails in two different places. When src
// parses, the AST is authoritative and a mention inside a comment or string literal
// cannot fake a hit. When src does NOT parse, the removed shape may be the very
// reason (`magus.project.register(fun(p, cb) ...)` predates required parameter
// annotations, so it dies in the parser with a message about parameter "p"), and a
// textual scan is the only thing left; it is reached only for a file that is already
// failing, so at worst it re-explains a broken magusfile with the wrong migration.
func RemovedAPICall(src string) (call, replacement string, ok bool) {
	prog, err := parsecache.Shared().ParseEmbedded(src)
	if err != nil || prog == nil {
		for _, r := range removedMagusfileAPI {
			if text := "magus." + strings.Join(r.path, "."); strings.Contains(src, text+"(") {
				return text, r.replacement, true
			}
		}
		return "", "", false
	}
	for _, stmt := range prog.Stmts {
		fd, isFun := stmt.(*ast.FunDecl)
		if !isFun || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if ok {
				return false
			}
			ce, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			for _, r := range removedMagusfileAPI {
				if magusRooted(ce.Callee, r.path) {
					call, replacement, ok = "magus."+strings.Join(r.path, "."), r.replacement, true
					return false
				}
			}
			return true
		})
		if ok {
			return call, replacement, true
		}
	}
	return "", "", false
}

// magusRooted reports whether e is the member path `magus.<path...>`, written with
// either separator: the dot and backslash forms are the same access, and a stale
// magusfile predates the backslash spelling entirely.
func magusRooted(e ast.Node, path []string) bool {
	for i := len(path) - 1; i >= 0; i-- {
		me, isMember := e.(*ast.MemberExpr)
		if !isMember || me.Name != path[i] {
			return false
		}
		e = me.Object
	}
	id, isIdent := e.(*ast.IdentExpr)
	return isIdent && id.Name == "magus"
}

// removedAPIErr reports a magusfile still calling an API magus no longer binds.
func removedAPIErr(call, replacement string) error {
	return types.DiagnosticErrorf(types.MagusfileAPIRemoved,
		"%s was removed: %s instead", call, replacement)
}

// WithSource stores src in ctx so that bindings (e.g. ctx.needs) can
// retrieve the active magusfile source for pool lookup.
func WithSource(ctx context.Context, src *Source) context.Context {
	return context.WithValue(ctx, sourceCtxKey{}, src)
}

// SourceFromContext retrieves the Source stored by WithSource, or nil.
func SourceFromContext(ctx context.Context) *Source {
	v, _ := ctx.Value(sourceCtxKey{}).(*Source)
	return v
}

// WithOverlay supplies magusfile CONTENT for absolute paths, read instead of the file on
// disk. Paths not named here are read normally, and no overlay is the ordinary load.
//
// It exists for the one caller that has to load a workspace whose magusfile it cannot read:
// `magus vcs resolve`, during a merge that left conflict markers in the magusfile itself.
// Everything that command does rests on the declarations (which target rebuilds which
// output), so a magusfile it cannot parse leaves it unable to settle even the conflicts it
// does own. The committed side of the merge is a complete, parseable copy of exactly those
// declarations, and reading it here is what lets the command do its half of the work while
// the hand-written conflict waits for a human.
//
// An overlay, rather than swapping the file on disk and restoring it: a crash between the
// two would leave the user's conflict replaced by one side of it, and the merge state is
// not something a tool should be able to lose on the way to reporting an error.
func WithOverlay(ctx context.Context, files map[string]string) context.Context {
	return context.WithValue(ctx, overlayCtxKey{}, files)
}

// WithSourceReader has every magusfile source and every Buzz file import a load reads come
// from read instead of the disk. The file search still decides which paths a load reads.
//
// It exists to evaluate the magusfile as a revision holds it without materializing that
// revision: the agent guard judges a spawn by both the committed and the working-tree
// magus\guard.spawn rule. An overlay entry still wins over it.
func WithSourceReader(ctx context.Context, read func(path string) ([]byte, error)) context.Context {
	return context.WithValue(ctx, sourceReaderCtxKey{}, read)
}

func sourceReaderFrom(ctx context.Context) func(path string) ([]byte, error) {
	read, _ := ctx.Value(sourceReaderCtxKey{}).(func(path string) ([]byte, error))
	return read
}

// SourceFile is one file a magusfile load read, named by the ContentID of the bytes it
// read, which costs no process to record.
type SourceFile struct {
	Path      string
	ContentID string
}

// SourceLog collects every file a load reads: its magusfile sources and every Buzz file
// they import. Safe for concurrent use.
type SourceLog struct {
	mu    sync.Mutex
	files map[string]string
}

// Files returns what the load read, sorted by path.
func (l *SourceLog) Files() []SourceFile {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]SourceFile, 0, len(l.files))
	for path, id := range l.files {
		out = append(out, SourceFile{Path: path, ContentID: id})
	}
	slices.SortFunc(out, func(a, b SourceFile) int { return strings.Compare(a.Path, b.Path) })
	return out
}

func (l *SourceLog) record(path string, data []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.files == nil {
		l.files = map[string]string{}
	}
	l.files[path] = ContentID(data)
}

type sourceLogCtxKey struct{}

// WithSourceLog records every file a load under ctx reads into log.
func WithSourceLog(ctx context.Context, log *SourceLog) context.Context {
	return context.WithValue(ctx, sourceLogCtxKey{}, log)
}

// ContentID is a stable fingerprint of data, computed in-process without asking any VCS.
// It uses git's SHA-1 blob formula, so the value is the one ids already recorded carry,
// and equals the blob id a git checkout would give the same bytes.
func ContentID(data []byte) string {
	h := sha1.New() //nolint:gosec // G401: a content fingerprint, not a security primitive
	fmt.Fprintf(h, "blob %d\x00", len(data))
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// loadReader is how a load reads a source file: the caller's reader or the disk, and
// recorded into the load's SourceLog when it has one. Nil when neither applies, which
// leaves a Buzz session on its own os.ReadFile.
func loadReader(ctx context.Context) func(path string) ([]byte, error) {
	read := sourceReaderFrom(ctx)
	log, _ := ctx.Value(sourceLogCtxKey{}).(*SourceLog)
	if log == nil {
		return read
	}
	if read == nil {
		read = os.ReadFile
	}
	return func(path string) ([]byte, error) {
		data, err := read(path)
		if err == nil {
			log.record(path, data)
		}
		return data, err
	}
}

// readSource reads path, preferring an overlay entry when the caller supplied one.
func readSource(ctx context.Context, path string) ([]byte, error) {
	if files, _ := ctx.Value(overlayCtxKey{}).(map[string]string); files != nil {
		if content, ok := files[path]; ok {
			return []byte(content), nil
		}
	}
	if read := loadReader(ctx); read != nil {
		return read(path)
	}
	return os.ReadFile(path)
}

type guardRulesKey struct{}

// WithGuardRules marks a magusfile load that collects guard rules. The session
// then limits the entry file to the magus\guard registrations and what they
// reference; see guardRulesFilter.
func WithGuardRules(ctx context.Context) context.Context {
	return context.WithValue(ctx, guardRulesKey{}, true)
}

func guardRules(ctx context.Context) bool {
	v, _ := ctx.Value(guardRulesKey{}).(bool)
	return v
}

// staleStampAge is how long a compiler stamp's directory may go unused before
// a new build removes it. Two builds used side by side (a checkout's ./magus
// and the one on PATH) each keep their own.
const staleStampAge = 24 * time.Hour

// guardBytecodeStore is where a guard-rules load keeps compiled chunks for the
// workspace at root: <user cache>/magus/buzz-bytecode/<compiler stamp>/<root
// hash>. It is outside the workspace because a stored chunk runs as it is
// read back, and the working tree is writable by the agents the guard judges.
//
// A cache is not a place only this user writes: CI restores it, a sync can fill
// it, and a spell's sandbox can be granted part of it. So every chunk is sealed
// with a key kept in the user's state dir, and one that does not verify is
// compiled over.
//
// Nil when the compiler cannot be identified or the cache or the key is
// unavailable, and the load compiles every time.
func guardBytecodeStore(root string) buzz.BytecodeStore {
	stamp := compilerStamp()
	if root == "" || stamp == "" {
		return nil
	}
	base, err := config.UserCacheDir()
	if err != nil {
		return nil
	}
	secret, err := bytecodeSecret()
	if err != nil {
		return nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	stamps := filepath.Join(base, "magus", "buzz-bytecode")
	dir := filepath.Join(stamps, stamp)
	now := time.Now()
	if err := os.Chtimes(dir, now, now); errors.Is(err, fs.ErrNotExist) {
		if os.MkdirAll(dir, 0o700) != nil {
			return nil
		}
		pruneStamps(stamps, stamp, now.Add(-staleStampAge))
	}
	sum := sha256.Sum256([]byte(abs))
	dir = filepath.Join(dir, hex.EncodeToString(sum[:]))
	return sealedBytecodeStore{inner: buzz.NewDiskBytecodeStore(dir), secret: secret, scope: dir}
}

// sealedBytecodeStore prefixes each chunk with an HMAC over the directory it is
// kept in, its key and its bytes, so a chunk moved from another key, workspace
// or compiler fails exactly as a forged one does.
type sealedBytecodeStore struct {
	inner  buzz.BytecodeStore
	secret []byte
	scope  string
}

var errUnsealedChunk = errors.New("bytecode chunk does not verify")

func (s sealedBytecodeStore) Load(key string) ([]byte, error) {
	sealed, err := s.inner.Load(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < sha256.Size || !hmac.Equal(sealed[:sha256.Size], s.seal(key, sealed[sha256.Size:])) {
		return nil, errUnsealedChunk
	}
	return sealed[sha256.Size:], nil
}

func (s sealedBytecodeStore) Store(key string, blob []byte) error {
	return s.inner.Store(key, append(s.seal(key, blob), blob...))
}

func (s sealedBytecodeStore) seal(key string, blob []byte) []byte {
	m := hmac.New(sha256.New, s.secret)
	for _, part := range []string{s.scope, key} {
		_, _ = m.Write([]byte(part))
		_, _ = m.Write([]byte{0})
	}
	_, _ = m.Write(blob)
	return m.Sum(nil)
}

// bytecodeSecret reads the key chunks are sealed with, creating it on first use.
// It lives in the state dir, apart from the cache it protects.
func bytecodeSecret() ([]byte, error) {
	base, err := config.UserStateDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "magus")
	path := filepath.Join(dir, "buzz-bytecode.key")
	read := func() ([]byte, error) {
		secret, err := os.ReadFile(path)
		if err == nil && len(secret) != sha256.Size {
			return nil, fmt.Errorf("%s: want %d bytes, found %d", path, sha256.Size, len(secret))
		}
		return secret, err
	}
	if secret, err := read(); !errors.Is(err, fs.ErrNotExist) {
		return secret, err
	}
	secret := make([]byte, sha256.Size)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, ".buzz-bytecode.key-")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	_, werr := f.Write(secret)
	if err := errors.Join(werr, f.Close()); err != nil {
		return nil, err
	}
	// A link, not a rename: it refuses an existing key, so two processes starting
	// together both end up using whichever key was placed first.
	if err := os.Link(tmp, path); errors.Is(err, fs.ErrExist) {
		return read()
	} else if err != nil {
		return nil, err
	}
	return secret, nil
}

// pruneStamps removes the chunk directories of other compilers last used
// before cutoff. Best effort: a directory that cannot be removed now is tried
// again by the next new build.
func pruneStamps(stamps, keep string, cutoff time.Time) {
	entries, err := os.ReadDir(stamps)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() == keep || !e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(stamps, e.Name()))
		}
	}
}

// compilerStamp identifies the build that compiles a chunk, so a chunk is only
// ever run by the compiler that wrote it. It is the executable's Go build ID,
// which the toolchain derives from the build's inputs and output: a rebuild of
// the same source matches (a fresh CI binary reuses the last run's chunks) and
// any other build does not, dirty tree or not. Empty when the executable cannot
// be read or carries no build ID.
func compilerStamp() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return compilerStampOf(exe)
}

func compilerStampOf(exe string) string {
	id := goBuildID(exe)
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])[:16]
}

// goBuildID reads the build ID the Go linker writes at the start of the text
// segment, which lies in the file's first 32 KiB (cmd/internal/buildid reads the
// same window), or from ELF's .note.go.buildid when an external link moved text.
func goBuildID(exe string) string {
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 32<<10)
	n, _ := io.ReadFull(f, head)
	const marker = "\xff Go build ID: \""
	if i := bytes.Index(head[:n], []byte(marker)); i >= 0 {
		rest := head[i+len(marker) : n]
		if j := bytes.IndexByte(rest, '"'); j > 0 {
			return string(rest[:j])
		}
	}
	ef, err := elf.NewFile(f)
	if err != nil {
		return ""
	}
	note := ef.Section(".note.go.buildid")
	if note == nil {
		return ""
	}
	// namesz, descsz, type, then "Go\x00\x00" and the ID.
	data, err := note.Data()
	if err != nil || len(data) < 16 {
		return ""
	}
	size := int(ef.ByteOrder.Uint32(data[4:8]))
	if len(data) < 16+size {
		return ""
	}
	return string(data[16 : 16+size])
}

// WithProjectPath stores the workspace-relative path of the project whose
// magusfile is being parsed, so magus.project(fn) (the contextual form with no
// explicit path) can default to "this project".
func WithProjectPath(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, projectPathCtxKey{}, path)
}

// ProjectPathFromContext returns the project path stored by WithProjectPath, and
// whether one was set.
func ProjectPathFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(projectPathCtxKey{}).(string)
	return v, ok
}

// Source describes a located magusfile source.
type Source struct {
	Dir    string   // absolute directory containing the magusfile
	Files  []string // absolute source paths in load order
	Engine string   // engine name ("buzz"); inferred from file extensions by Find
}

// Target is a single invocable target discovered in a magusfile.
type Target struct {
	Key  string // lowercase dispatch identifier
	Name string // mixed-case display name
}

// RunDir runs target for the project in dir. Returns ErrNoMagusfile or
// ErrUnknownTarget when not found.
//
// Buzz is the only engine today, so FindAll yields a single source. The loop is
// the seam a second engine would extend: each source is fully executed (including
// top-level declarations such as magus.project) before its target registry is
// consulted, and an unknown target falls through to the next source.
func RunDir(ctx context.Context, dir, target string, extraArgs []string) (any, error) {
	srcs, err := FindAll(dir)
	if err != nil {
		return nil, err
	}

	for _, src := range srcs {
		var val any
		val, err = runBuzz(ctx, src, target, extraArgs, dir)
		if errors.Is(err, ErrUnknownTarget) {
			continue
		}
		return val, err
	}
	return nil, ErrUnknownTarget
}

// Parse executes src in parse mode (name discovery only) and returns discovered targets.
func Parse(ctx context.Context, src *Source) ([]Target, error) {
	// Carry the source so bindings resolve paths relative to the magusfile's own
	// directory (local-spell require/import), not the process cwd, the same context
	// Run establishes. Without this, preloading a magusfile from outside its dir
	// fails to find its ./spells.
	ctx = WithSource(ctx, src)
	return parseBuzz(ctx, src)
}

// BuzzHostBindingsFn registers Go-backed host modules into a Buzz session.
// targets is the session's dispatchable target registry; exports maps each
// canonical target key to the exported function value itself, so ctx.needs
// can verify a passed function IS the exported target (nil when the session
// has no export discovery, e.g. the REPL). parseMode=true collects names only.
type BuzzHostBindingsFn func(ctx context.Context, sess *buzz.Session, targets map[string]vm.Callable, exports map[string]vm.Value, parseMode bool)

var buzzHostBindingsFn BuzzHostBindingsFn

// RegisterBuzzHostBindings stores the Buzz host-binding function. Called from bindings init().
func RegisterBuzzHostBindings(fn BuzzHostBindingsFn) {
	if buzzHostBindingsFn != nil {
		panic("interp: Buzz host bindings already registered")
	}
	buzzHostBindingsFn = fn
}

// buzzSpellImportCheckFn validates the handles a magusfile imports via
// `import "magus/spell/<handle>"`. The bindings package owns the spell registry,
// so it supplies the check; nil until it registers one. See
// RegisterBuzzSpellImportCheck and spellImportNames.
var buzzSpellImportCheckFn func(handles []string) error

// RegisterBuzzSpellImportCheck stores the validator for `magus/spell/*` imports.
// Called from bindings init(), the same seam as RegisterBuzzHostBindings.
func RegisterBuzzSpellImportCheck(fn func(handles []string) error) {
	if buzzSpellImportCheckFn != nil {
		panic("interp: Buzz spell-import check already registered")
	}
	buzzSpellImportCheckFn = fn
}

// spellImportNames returns the <handle> of every `import "magus/spell/<handle>"`
// statement in src. It reads the parsed AST, not the raw text, so a commented-out
// or string-literal import never counts.
//
// It parses with ParseEmbedded, the same lenient mode magusfiles load under (Exec
// uses WithEmbedded). Strict buzz.Parse rejects top-level statements, which real
// magusfiles use freely (the repo's own has a top-level `if`), so parsing strict
// here would error and silently skip the check on exactly those files. A parse
// error still yields nil: Exec re-parses and reports the real syntax error with
// position. The substring gate skips the parse for the common magusfile that
// imports no spell (Exec is about to parse the source anyway).
func spellImportNames(src string) []string {
	if !strings.Contains(src, "magus/spell/") {
		return nil
	}
	prog, err := parsecache.Shared().ParseEmbedded(src)
	if err != nil {
		return nil
	}
	var handles []string
	for _, stmt := range prog.Stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok {
			continue
		}
		if handle, ok := strings.CutPrefix(imp.Path, "magus/spell/"); ok {
			handles = append(handles, handle)
		}
	}
	return handles
}

// checkRemoteSpellImports refuses a registry-path import magus.yaml does not declare,
// before Exec runs any top-level statement, so the load stops naming the entry to add
// rather than at an unbound name. Declared spells were pulled and verified when the
// workspace loaded, so this touches no network and no file. paths are the source's
// registry-path imports, as remoteImportPaths reads them.
func checkRemoteSpellImports(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	im := remotespell.ImportsFromContext(ctx)
	var errs []error
	for _, path := range paths {
		if _, err := im.Dir(path); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// remoteImportPaths returns the registry-path imports src makes. A parse error yields
// nil: Exec re-parses and reports it with position.
func remoteImportPaths(src string) []string {
	if !mentionsRemoteImport(src) {
		return nil
	}
	prog, err := parsecache.Shared().ParseEmbedded(src)
	if err != nil {
		return nil
	}
	var paths []string
	for _, stmt := range prog.Stmts {
		if imp, ok := stmt.(*ast.ImportStmt); ok && spells.IsRemoteImport(imp.Path) {
			paths = append(paths, imp.Path)
		}
	}
	return paths
}

// mentionsRemoteImport is the cheap textual gate in front of the parse: whether any
// quoted string following an import keyword reads as a registry path. A false
// positive only costs the parse; the AST check is the one that decides.
func mentionsRemoteImport(src string) bool {
	for rest := src; ; {
		i := strings.Index(rest, `import "`)
		if i < 0 {
			return false
		}
		rest = rest[i+len(`import "`):]
		path, _, ok := strings.Cut(rest, `"`)
		if ok && spells.IsRemoteImport(path) {
			return true
		}
	}
}

// importErrors collects the failures a module resolver hits while one magusfile
// executes. The resolver has no error channel of its own, so without this a spell that
// fails to bind surfaces later as an unrelated null.
type importErrors struct {
	mu   sync.Mutex
	errs []error
}

type importErrorsKey struct{}

// ReportImportError records err against the magusfile load on ctx, which fails with it
// once the file finishes executing. It returns false when no load is collecting, so the
// caller can log instead.
func ReportImportError(ctx context.Context, err error) bool {
	sink, _ := ctx.Value(importErrorsKey{}).(*importErrors)
	if sink == nil {
		return false
	}
	sink.mu.Lock()
	sink.errs = append(sink.errs, err)
	sink.mu.Unlock()
	return true
}

func (s *importErrors) take() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := errors.Join(s.errs...)
	s.errs = nil
	return err
}

// importBoundNames maps each import's bound namespace identifier to its path. A flat
// import (`as _`) binds no name; an alias binds itself; a plain import binds the path's
// last segment. Returns nil on a parse error (Exec re-parses and reports it).
func importBoundNames(src string) map[string]string {
	prog, err := parsecache.Shared().ParseEmbedded(src)
	if err != nil {
		return nil
	}
	names := map[string]string{}
	for _, stmt := range prog.Stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok || imp.Alias == "_" {
			continue
		}
		bound := imp.Alias
		if bound == "" {
			bound = imp.Path
			if i := strings.LastIndex(bound, "/"); i >= 0 {
				bound = bound[i+1:]
			}
		}
		names[bound] = imp.Path
	}
	return names
}

// magusfileFacts is what execBuzzSrc reads from a magusfile's syntax before running it.
// Every field is a function of the source bytes and this build's parser alone.
type magusfileFacts struct {
	Imports       map[string]string `json:"imports,omitempty"`
	CtxForm       []string          `json:"ctx_form,omitempty"`
	SpellHandles  []string          `json:"spell_handles,omitempty"`
	RemoteImports []string          `json:"remote_imports,omitempty"`
	RemovedCall   string            `json:"removed_call,omitempty"`
	Replacement   string            `json:"replacement,omitempty"`
}

func factsOf(code string) magusfileFacts {
	f := magusfileFacts{
		Imports:       importBoundNames(code),
		SpellHandles:  spellImportNames(code),
		RemoteImports: remoteImportPaths(code),
	}
	f.CtxForm = slices.Sorted(maps.Keys(CtxFormTargetKeys(code)))
	f.RemovedCall, f.Replacement, _ = RemovedAPICall(code)
	return f
}

// magusfileFactsOf returns code's facts, kept in store beside the compiled chunks when
// there is one.
//
// optimization: a guard hook whose chunk is stored never parses the entry magusfile
// except to answer these checks, so their answers are stored too.
//
//	measured: BenchmarkMagusfileFacts on this repo's root magusfile, reparse to stored
//	  -95.7% sec/op (10.7ms to 0.47ms), -97.9% B/op (11.0 MB), -99.9% allocs/op
//	  (benchstat, n=10).
//	trade-off: one more file per magusfile version in the guard's chunk store.
//	assumes:  store is scoped to one compiler build, as guardBytecodeStore's is.
func magusfileFactsOf(store buzz.BytecodeStore, code string) magusfileFacts {
	if store == nil {
		return factsOf(code)
	}
	sum := sha256.Sum256([]byte(code))
	key := "facts-" + hex.EncodeToString(sum[:])
	if blob, err := store.Load(key); err == nil {
		var f magusfileFacts
		if json.Unmarshal(blob, &f) == nil {
			return f
		}
	}
	f := factsOf(code)
	if blob, err := json.Marshal(f); err == nil {
		_ = store.Store(key, blob)
	}
	return f
}

// importTargetCollisionErr reports a target whose name shadows a same-named import,
// which makes the module read null on member access.
func importTargetCollisionErr(name, importPath string) error {
	return fmt.Errorf("magusfile: target %q shadows the module import %q, so %s.<member> "+
		"reads null; rename the target or alias the import", name, importPath, name)
}

// runBuzz executes src on a fresh Buzz session and invokes target.
func runBuzz(ctx context.Context, src *Source, target string, extraArgs []string, workDir string) (any, error) {
	// Carry the target's directory on the context instead of os.Chdir-ing the whole
	// process. The host modules (std.*) resolve relative paths against this cwd, so
	// magusfile targets across projects (including a cross-project ctx.needs that
	// re-enters the interpreter) execute concurrently without corrupting a shared
	// process working directory.
	if workDir != "" {
		ctx = std.WithCwd(ctx, workDir)
	}
	slog.DebugContext(ctx, "interp: run magusfile target", "target", target, "dir", workDir)

	load, err := execBuzzSrc(ctx, src, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = load.Session.Close() }()

	key := types.Normalize(target)
	fn, ok := load.Targets[key]
	if !ok {
		var names []string
		for k := range load.Targets {
			names = append(names, k)
		}
		slices.Sort(names)
		// Carries the MGS1006 code for lookup, but still Unwraps to ErrUnknownTarget so the fan-out
		// suppression (errors.Is(err, ErrUnknownTarget)) that skips projects lacking this target keeps working.
		return nil, types.WrapDiagnostic(types.UnknownTarget, ErrUnknownTarget,
			"magusfile: unknown target %q (registered: %s)", target, strings.Join(names, ", "))
	}
	// A target's signature is (ctx: magus\Context, args: [str]): ONE context and
	// ONE list. Spreading the strings as separate positional arguments bound
	// `args` to the first string and silently dropped the rest, so
	// `-- alpha beta` arrived as the str "alpha" rather than ["alpha", "beta"].
	// Always pass the list, empty included, so `args` is a [str] in every target
	// rather than null when no extra args were given.
	items := make([]vm.Value, len(extraArgs))
	for i, s := range extraArgs {
		items[i] = vm.StrValue(s)
	}
	buzzArgs := []vm.Value{vm.ListValue(items)}
	ctx, exitCode := types.WithExitCapture(ctx)
	ctx = WithSource(ctx, src)
	// Seed the dispatch ancestor stack with the entry target. It is called here
	// directly rather than dispatched, so the pool never saw it: a dependency that
	// needed the entry target back was not a cycle to anyone, and it re-ran the
	// entry target's body inside its own dependency. With two dependencies in
	// flight that second execution then parked on one dependency's memo entry
	// while that dependency parked on the entry target: a hang instead of the
	// cycle diagnostic the pool produces one level deeper.
	ctx = buzz.WithAncestors(ctx, []string{key})
	val, err := fn(ctx, buzzArgs)
	// The typed error is the answer whenever it survived the VM; the capture is only the
	// fallback for an engine that stringifies ExitError away. Consulting the capture
	// before err was looked at at all let an earlier os\exit(0) (an op's clean early
	// return) report success for a genuine failure raised after it, so a captured ZERO
	// is never taken as that fallback: the error in hand says the run did not succeed.
	if code, ok := exitCode(); ok {
		var ex types.ExitError
		switch {
		case errors.As(err, &ex):
			return nil, ex
		case err == nil, code != 0:
			return nil, types.ExitError{Code: code}
		}
	}
	if err != nil {
		// Deliberately unwrapped: every caller reaches a user through
		// types.SpellErrors, which already names the spell and the target. Adding
		// them here too produced "[magusfile] magusfile: target lint: ...", the
		// spell twice, the target twice, before the actual failure.
		return nil, err
	}
	// The value was discarded here for as long as targets existed, which is what
	// made `> void` look like a rule rather than a convention.
	ret, err := buzzReturnToGo(val)
	if err != nil {
		// Deliberately unwrapped: every caller reaches a user through
		// types.SpellErrors, which already names the spell and the target. Adding
		// them here too produced "[magusfile] magusfile: target lint: ...", the
		// spell twice, the target twice, before the actual failure.
		return nil, err
	}
	return ret, nil
}

// parseBuzz executes src in parse mode to collect target names.
func parseBuzz(ctx context.Context, src *Source) ([]Target, error) {
	load, err := execBuzzSrc(ctx, src, true)
	if err != nil {
		return nil, err
	}
	_ = load.Session.Close()

	var names []string
	for name := range load.Targets {
		names = append(names, name)
	}
	slices.Sort(names)

	out := make([]Target, len(names))
	for i, name := range names {
		out[i] = Target{Key: name, Name: name}
	}
	return out, nil
}

// targetCollisionErr reports two source target names that normalize to the same
// canonical key. Used by the Buzz registration path so the message
// stays identical across engines.
func targetCollisionErr(prev, cur, key string) error {
	return fmt.Errorf("magusfile: targets %q and %q both normalize to %q; "+
		"target names are matched case- and delimiter-insensitively, so rename one", prev, cur, key)
}

// ctxlessTargetErr reports an exported magusfile function missing the magus.Context
// first parameter every target must declare. The signature is the contract magus reads
// statically to build the graph, so the ctx-less form is rejected at load.
func ctxlessTargetErr(name string) error {
	return types.DiagnosticErrorf(types.TargetMissingContext,
		"target %q must receive a %s as its first parameter: "+
			"change its signature to export fun %s(ctx: %s, args: [str])",
		name, types.ContextParamAnnotation, name, types.ContextParamAnnotation)
}

// execBuzzSrc creates a Buzz Session, registers bindings, and executes source files.
// loadedBuzz is a magusfile load: a freshly-executed session plus its exposed
// target map. The two are produced together and always returned together,
// so the old (*Session, map[string]vm.Callable, error) signature carried
// a nil-nil-error shape on every failure; bundling them here reduces the
// return to the idiomatic (value, error) pair.
type loadedBuzz struct {
	Session *buzz.Session
	Targets map[string]vm.Callable
}

func execBuzzSrc(ctx context.Context, src *Source, parseMode bool) (*loadedBuzz, error) {
	// Carry the magusfile source on the context for the whole load, so the module
	// resolver registered below (which captures this ctx) can resolve top-level
	// `import "project/<path>"` cross-project handles during execution. Run mode
	// otherwise reaches here with a nil source (runBuzz sets it only for target
	// dispatch), and such an import would resolve to nothing. Parse mode already sets
	// it upstream; re-setting is idempotent.
	ctx = WithSource(ctx, src)
	// Installed before the bindings capture ctx, so their module resolver can report.
	importErrs := &importErrors{}
	ctx = context.WithValue(ctx, importErrorsKey{}, importErrs)
	// The buzz path uses the standalone interpreter's concrete API (Exec, Targets,
	// CallVal) directly; the engine.Session adapter is only for generic registry
	// consumers, so there's no need to round-trip through engine.Lookup. Confine
	// imports to the magusfiles layout (see magusSearchPaths); WithSearchPaths
	// replaces gopherbuzz's upstream default so a magusfile resolves siblings the same
	// way regardless of the process cwd, and cannot escape via BUZZ_INCLUDE_PATH.
	buzzSess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithParseCache(parsecache.Shared()), buzz.WithSearchPaths(magusSearchPaths(ctx, src.Dir)...))
	// NewSession seeds includeDirs from BUZZ_INCLUDE_PATH; clear them so resolution
	// stays limited to the magusfiles search paths above.
	buzzSess.SetIncludeDirs(nil)
	if read := confineImportReads(ctx, src.Dir, loadReader(ctx)); read != nil {
		buzzSess.SetSourceReader(read)
	}
	// Magusfiles run as whole files, not incrementally, so a non-exported,
	// non-captured top-level var is chunk-private and can use a fast stack slot
	// instead of an Env binding. The cross-file/cross-target surface is `export`ed
	// functions, which stay Env-bound. The REPL (NewBuzzReplSession) deliberately
	// does not enable this: there a later line must resolve earlier names.
	buzzSess.SetPromoteTopLevel(true)
	// Feed this session's compile phases, imports, and VM faults to the spine. A
	// no-op (session left unobserved) when telemetry is disabled on ctx.
	// A profile on ctx records the same events for a human reading a trace.
	AttachSessionObservers(ctx, buzzSess, ModeMagusfile)
	buzzSess.AddCompileObserver(buzz.ProfileFromContext(ctx))
	var store buzz.BytecodeStore
	if guardRules(ctx) {
		buzzSess.SetEntryFilter(guardRulesFilterID, guardRulesFilter)
		// A hook loads these rules in a fresh process, and compiling them is the
		// part of that load the source bytes do not have to repeat. Ordinary
		// magusfile loads are left alone, and so is a load of approved sources:
		// it exists to trust nothing but the bytes its reader returns.
		if sourceReaderFrom(ctx) == nil {
			store = guardBytecodeStore(src.Dir)
			if store != nil {
				buzzSess.SetBytecodeStore(store)
			}
		}
	}

	targetMap := buzzSess.Targets()
	// exportVals is filled by the export-discovery loop below, after the files
	// execute; the bindings close over the map reference, so ctx.needs sees the
	// populated registry by the time any target body runs.
	exportVals := map[string]vm.Value{}
	if buzzHostBindingsFn != nil {
		buzzHostBindingsFn(ctx, buzzSess, targetMap, exportVals, parseMode)
	}

	// Import names across all the project's magusfiles, to catch a target that shadows one.
	importNames := map[string]string{}
	// ctx-form target keys across all the project's magusfiles: these receive a
	// magus.Context first argument at dispatch (injected below) instead of running
	// on the bare `args` signature the old form uses.
	ctxForm := map[string]bool{}
	for _, path := range src.Files {
		// rel names the offending file relative to its project dir, so a magusfiles/
		// directory (several files) is unambiguous; the project itself is already in
		// the surrounding "preload <project>" context. path is built from src.Dir, so
		// Rel is a pure-lexical relativize with no I/O and no symlink/escape pitfall.
		rel, relErr := filepath.Rel(src.Dir, path)
		if relErr != nil {
			rel = path
		}
		data, err := readSource(ctx, path)
		if err != nil {
			_ = buzzSess.Close()
			return nil, fmt.Errorf("magusfile: read %s: %w", rel, err)
		}
		code := string(data)
		facts := magusfileFactsOf(store, code)
		maps.Copy(importNames, facts.Imports)
		for _, key := range facts.CtxForm {
			ctxForm[key] = true
		}
		// Validate spell handles before Exec: an unknown `magus/spell/<handle>`
		// resolves to nothing (gopherbuzz skips an unresolved import) and would
		// otherwise surface much later as a disconnected "undefined" error. Fail fast,
		// naming the file, with a did-you-mean instead.
		if buzzSpellImportCheckFn != nil {
			if err := buzzSpellImportCheckFn(facts.SpellHandles); err != nil {
				_ = buzzSess.Close()
				return nil, fmt.Errorf("magusfile: %s: %w", rel, err)
			}
		}
		// The module resolver has no error channel, so an undeclared remote spell import
		// is refused here, where the load can stop naming the entry to add.
		if err := checkRemoteSpellImports(ctx, facts.RemoteImports); err != nil {
			_ = buzzSess.Close()
			return nil, &ImportError{Path: path, rel: rel, Err: err}
		}
		// Reject a call magus no longer binds before Exec, so the magusfile fails at
		// load naming the migration rather than at run time as "null is not callable"
		// (or, for a shape that predates required parameter annotations, as a parser
		// complaint about the callback's parameter).
		if facts.RemovedCall != "" {
			_ = buzzSess.Close()
			return nil, fmt.Errorf("magusfile: %s: %w", rel, removedAPIErr(facts.RemovedCall, facts.Replacement))
		}
		execErr := TimeExec(ctx, ModeMagusfile, func() error { return buzzSess.Exec(ctx, code) })
		// An import that failed to bind is the cause of whatever Exec tripped on next,
		// so it wins over Exec's own error.
		if err := importErrs.take(); err != nil {
			_ = buzzSess.Close()
			return nil, &ImportError{Path: path, rel: rel, Err: err}
		}
		if execErr != nil {
			_ = buzzSess.Close()
			return nil, &ExecError{Path: path, rel: rel, Err: hint.ExplainImplicitMagus(execErr)}
		}
	}

	// Discover targets from exported functions (export fun name ...). The
	// normalizer is many-to-one (go_build, goBuild, GoBuild all give go-build), so two
	// exports that normalize to the same canonical name are a hard error rather
	// than a silent last-write-wins clobber. Iterate in sorted order so the
	// reported pair is deterministic.
	exports := buzzSess.Exports()
	names := make([]string, 0, len(exports))
	for name := range exports {
		names = append(names, name)
	}
	slices.Sort(names)
	seen := make(map[string]string, len(names)) // canonical key -> source name
	// The shared magus.Context value ctx-form targets receive as their first
	// argument (stashed by the bindings layer). Null for a session without the
	// bindings (e.g. a bare test), in which case no ctx-form target is dispatchable.
	targetCtxVal := buzzSess.GetGlobal(TargetContextGlobal)
	for _, name := range names {
		val := exports[name]
		if !val.IsFun() {
			continue
		}
		// A target that shadows a same-named import makes the module read null; fail at load.
		if importPath, clash := importNames[name]; clash {
			_ = buzzSess.Close()
			return nil, importTargetCollisionErr(name, importPath)
		}
		key := types.Normalize(name)
		if prev, dup := seen[key]; dup {
			_ = buzzSess.Close()
			return nil, targetCollisionErr(prev, name, key)
		}
		// Every target must receive a magus.Context as its first parameter: the
		// signature IS the contract magus reads statically to build the graph. Reject the
		// old ctx-less form at load rather than dispatching it with the wrong arguments.
		if !ctxForm[key] {
			_ = buzzSess.Close()
			return nil, ctxlessTargetErr(name)
		}
		seen[key] = name
		captured := val
		exportVals[key] = val
		dir := src.Dir
		targetMap[key] = func(ctx context.Context, args []vm.Value) (vm.Value, error) {
			// A ctx.needs dependency is dispatched with no arguments, inline or through
			// the pool; its `args` is the empty list runBuzz gives an entry target, not null.
			if len(args) == 0 {
				args = []vm.Value{vm.ListValue([]vm.Value{})}
			}
			// Every body gets a dependency-wait accumulator, timeout or not. Scoping it
			// to the deadline instead leaves an uncapped body writing into its nearest
			// ceilinged ancestor, whose own ctx.needs span already counts that whole
			// child once: `ci` composes lint, format and generate, none of which declare
			// a timeout, so each level re-adds time the level above already has.
			ctx = types.WithDependencyWait(ctx)
			ctx = withDeclaredStep(ctx, dir, key)
			// The DURATION, not a context carrying it: runTargetBody applies it per
			// resume, so it measures the body's own execution and not the time its
			// dependencies take.
			ceiling := declaredTimeout(ctx, dir, key)
			started := time.Now()
			v, err := TimeCall(ctx, ModeMagusfile, func() (vm.Value, error) {
				// Prepend the magus.Context so the body's `ctx` parameter binds it; the
				// user args (the `[str]` second parameter) ride along after.
				return runTargetBody(ctx, buzzSess, captured,
					append([]vm.Value{targetCtxVal}, args...), ceiling)
			})
			elapsed := time.Since(started)
			// Emitted on every ceiling-bearing body, not only the ones that expire. A
			// ceiling that fires reports this split in its error, but the number worth
			// having is from the runs that PASS: whether a declared timeout is measuring
			// the target or measuring the queue is a question about the steady state, and
			// by the time one expires the answer is already too late to be a measurement.
			logCeiling(ctx, key, ceiling, elapsed)
			return v, types.CeilingExceededError(ctx, err, key, ceiling, elapsed)
		}
	}

	return &loadedBuzz{Session: buzzSess, Targets: targetMap}, nil
}

// withDeclaredStep confines ctx to the target key of the project whose magusfile dir is
// dir, the scope runTarget gives a scheduled target. A ctx.needs child never passes
// through runTarget, so this is the only place its own grants apply. It gets its own
// declaration, not the union with its caller's: widening either step to cover the other
// hands grants to processes that never declared them.
func withDeclaredStep(ctx context.Context, dir, key string) context.Context {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return ctx
	}
	p := projectAt(ws, dir)
	if p == nil {
		return ctx
	}
	scope := make([]string, len(p.ResolvedSpells))
	for i, s := range p.ResolvedSpells {
		scope[i] = s.Name()
	}
	return sandbox.WithStep(ctx, scope, p.TargetPolicies[key].Sandbox)
}

// NewBuzzWorkerFunc returns the buzz.WorkerFunc that creates a pre-warmed Buzz
// session for src. Safe to call from multiple goroutines because execBuzzSrc reads
// sources by absolute path and does not acquire chdirMu.
func NewBuzzWorkerFunc(src *Source) buzz.WorkerFunc {
	return func(ctx context.Context) (*buzz.WorkerSession, error) {
		load, err := execBuzzSrc(ctx, src, false)
		if err != nil {
			return nil, err
		}
		return &buzz.WorkerSession{Session: load.Session, Targets: load.Targets}, nil
	}
}

// NewBuzzReplSession creates a Buzz session with host bindings installed, ready
// for the shared REPL. Imports resolve against dir. When autoload is set and dir
// holds a magusfile.buzz, its files are executed first so their top-level
// definitions are available at the prompt.
// The returned engine.Session also satisfies the optional REPL/debug interfaces.
func NewBuzzReplSession(ctx context.Context, dir string, autoload bool) (engine.Session, error) {
	// WithREPL suppresses the BZZ3001 unused-import warning, matching upstream Buzz
	// (Parser.zig gates the same warning on `self.flavor != .Repl`): a REPL evaluates
	// one statement at a time, so an import "unused so far" may just be used by a
	// line not typed yet.
	buzzSess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithREPL(), buzz.WithParseCache(parsecache.Shared()), buzz.WithSearchPaths(magusSearchPaths(ctx, dir)...))
	buzzSess.SetIncludeDirs(nil)
	if read := confineImportReads(ctx, dir, nil); read != nil {
		buzzSess.SetSourceReader(read)
	}
	AttachSessionObservers(ctx, buzzSess, ModeRepl)
	if buzzHostBindingsFn != nil {
		// nil exports: the REPL has no export-discovery pass, so ctx.needs
		// falls back to name-only resolution there.
		buzzHostBindingsFn(ctx, buzzSess, buzzSess.Targets(), nil, false)
	}

	if autoload {
		if src, err := Find(dir); err == nil && src.Engine == "buzz" {
			for _, path := range src.Files {
				data, rerr := os.ReadFile(path)
				if rerr != nil {
					_ = buzzSess.Close()
					return nil, fmt.Errorf("repl: read %s: %w", path, rerr)
				}
				if eerr := TimeExec(ctx, ModeRepl, func() error { return buzzSess.Exec(ctx, string(data)) }); eerr != nil {
					_ = buzzSess.Close()
					return nil, fmt.Errorf("repl: autoload %s: %w", path, eerr)
				}
			}
		} else if err != nil && !errors.Is(err, ErrNoMagusfile) {
			slog.WarnContext(ctx, "interp: buzz repl autoload find failed", slog.String("error", err.Error()))
		}
	}

	return buzzengine.Wrap(buzzSess), nil
}

// magusSearchPaths returns the import search path templates a magusfile resolves
// against (see buzz.WithSearchPaths). `?` is the import name, filled in by the
// resolver. Each of gopherbuzz's upstream PROJECT-RELATIVE layouts (a sibling
// file, or a library directory) is searched, plus magus's own magusfiles/
// convention, in order relative to projectDir, then the workspace root. The
// workspace root is read from ctx (types.WithWorkspace) and omitted when absent or
// identical to projectDir, so the common single-project case yields no duplicate
// entry.
//
// The process cwd is deliberately not a root: `magus --root <tree>` run from
// another checkout would load that checkout's modules.
//
// gopherbuzz's SYSTEM paths (/usr/share/buzz, /usr/local/share/buzz, $BUZZ_PATH)
// are deliberately NOT adopted, and BUZZ_INCLUDE_PATH is cleared at the call sites:
// a magusfile resolves imports only within the workspace, so a build stays hermetic
// and can't pull in arbitrary machine-installed buzz code. Note: an imported
// sibling is not auto-tracked for affected/drift; declare it in the project's
// `sources` so an edit marks the project dirty.
//
// The verified remote spells, laid out by import path, are the last root, so
// `import "ghcr.io/team/spells/lint"` resolves through the same templates as any
// module. They come after the workspace only nominally: a workspace directory at a
// declared remote path is refused at load (MGS1002), so nothing can shadow them.
func magusSearchPaths(ctx context.Context, projectDir string) []string {
	roots := []string{projectDir}
	if ws := types.WorkspaceFromContext(ctx); ws != nil {
		if root := ws.Root(); root != "" && root != projectDir {
			roots = append(roots, root)
		}
	}
	if view := remotespell.ImportsFromContext(ctx).View(); view != "" {
		roots = append(roots, view)
	}
	// Per root: the upstream project-relative layouts, then magus's magusfiles/ form.
	templates := []string{
		"?.buzz",
		filepath.Join("?", "main.buzz"),
		filepath.Join("?", "src", "main.buzz"),
		filepath.Join("?", "src", "?.buzz"),
		filepath.Join("magusfiles", "?.buzz"),
	}
	paths := make([]string, 0, len(roots)*len(templates))
	for _, r := range roots {
		for _, t := range templates {
			paths = append(paths, filepath.Join(r, t))
		}
	}
	return paths
}

// confineImportReads wraps read, the session's import source reader (nil reads the
// disk), so a file the module search resolved outside the workspace root is refused
// with MGS1047 and never read. It answers where the search joins an import onto a
// directory, the only point that sees the final path: gopherbuzz stats each candidate
// before the reader runs, so an escaping candidate is stat'd but its bytes are never
// opened. Allowed: the workspace root, the verified remote-spell view, and dir when
// the importing file itself lives outside the root. With no workspace it returns read
// unchanged.
func confineImportReads(ctx context.Context, dir string, read func(path string) ([]byte, error)) func(path string) ([]byte, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil || ws.Root() == "" {
		return read
	}
	root := absClean(ws.Root())
	bounds := []string{root}
	if view := remotespell.ImportsFromContext(ctx).View(); view != "" {
		bounds = append(bounds, absClean(view))
	}
	if dir != "" && !within(root, absClean(dir)) {
		bounds = append(bounds, absClean(dir))
	}
	return func(path string) ([]byte, error) {
		abs := absClean(path)
		if !slices.ContainsFunc(bounds, func(b string) bool { return within(b, abs) }) {
			err := types.DiagnosticErrorf(types.SpellImportEscapesWorkspace,
				"import resolves outside the workspace: %s is above the workspace root %s, and an import never loads a file outside the root; import a path inside the workspace",
				abs, root)
			ReportImportError(ctx, err)
			return nil, err
		}
		if read == nil {
			return os.ReadFile(path)
		}
		return read(path)
	}
}

func absClean(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// within reports whether the clean absolute path p is root or lies under it.
func within(root, p string) bool {
	rest, ok := strings.CutPrefix(p, root)
	return ok && (rest == "" || rest[0] == filepath.Separator || strings.HasSuffix(root, string(filepath.Separator)))
}

// runTargetBody runs one target body, driving it as a fiber so a ctx.needs inside it
// can PARK rather than block.
//
// Why the body is a fiber at all, stated narrowly because the wider claims did not
// survive measurement: the scheduling slot is ALREADY released while dependencies run
// (Pool.Dispatch yields it), and the body's Buzz session cannot be released, since a
// parked fiber still references it. What parking buys is the declared ceiling: a
// blocking ctx.needs sits inside Exec under one fixed deadline, while a parked body
// re-enters Exec and the driver hands it a fresh one, so a timeout measures the target
// instead of the queue behind it.
//
// The loop is the whole mechanism: resume, and if the body parked with a dependency
// request, run it and resume again. A body that never calls ctx.needs runs to
// completion on the first resume and costs one extra fiber allocation.
//
// Dependencies are themselves target bodies reached through targets, so each one gets
// its own driver and parks on its own ctx.needs. The recursion terminates because a
// dependency graph is acyclic; magus refuses cycles at load.
func runTargetBody(
	ctx context.Context,
	sess *buzz.Session,
	body vm.Value,
	args []vm.Value,
	ceiling time.Duration,
) (vm.Value, error) {
	// base carries NO deadline. Dependencies run under it, so a declared timeout on this
	// target never bounds another target's work; each dependency brings its own ceiling.
	base := ctx
	waiting := types.DependencyWaitFromContext(base)

	fiber, err := sess.NewFiber(base, body, args)
	if err != nil {
		return vm.Null, err
	}
	// remaining is the body's UNSPENT ceiling. Each resume spends only the time the body
	// itself runs, so parking to wait on dependencies costs it nothing.
	remaining := ceiling
	for {
		runCtx, cancel := base, context.CancelFunc(func() {})
		if ceiling > 0 {
			runCtx, cancel = context.WithTimeout(base, remaining)
		}
		startedOwn := time.Now()
		_, err := sess.ResumeFiber(runCtx, fiber)
		ownElapsed := time.Since(startedOwn)
		cancel()
		if err != nil {
			return vm.Null, err
		}
		if ceiling > 0 {
			remaining -= ownElapsed
			if remaining <= 0 {
				remaining = 0
			}
		}
		// COMPLETION IS THE FIBER'S STATUS, never the absence of a parked request. A
		// resume reports null both for "yielded null" and "finished" (see
		// Session.ResumeFiber), and a body can suspend for a reason this driver does
		// not know about; reading "no request" as "done" would abandon it silently.
		if done, ret := fiberDone(fiber); done {
			return ret, nil
		}
		// base, not runCtx: dependency time is not the body's to spend. Do books it
		// against this body, so the split logCeiling reports needs no separate call.
		did, err := waiting.Do(base)
		if err != nil {
			return vm.Null, err
		}
		if !did {
			return vm.Null, fmt.Errorf(
				"target body suspended with no pending work: the driver cannot resume it and will not abandon it")
		}
	}
}

// fiberDone reports whether the body finished, with its return value.
func fiberDone(fiber vm.Value) (bool, vm.Value) {
	fib, ok := vm.AsFiber(fiber)
	if !ok || fib.Status() != vm.FiberDone {
		return false, vm.Null
	}
	return true, fib.Return()
}

// ExecError is a magusfile that failed to evaluate. It renders as it always has; it exists so
// a caller reporting the failure elsewhere (the server's workspace status) can name the file
// without parsing the sentence.
type ExecError struct {
	// Path is the magusfile's absolute path.
	Path string
	rel  string
	Err  error
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("magusfile: exec %s: %v", displayPath(e.rel, e.Path), e.Err)
}

func (e *ExecError) Unwrap() error { return e.Err }

// ImportError is a magusfile whose imports failed to bind. Err may join one error per
// failed import, which is why the file lives here: a caller splitting Err into its
// branches would otherwise lose it.
type ImportError struct {
	// Path is the importing magusfile's absolute path.
	Path string
	rel  string
	Err  error
}

func (e *ImportError) Error() string {
	return fmt.Sprintf("magusfile: %s: %v", displayPath(e.rel, e.Path), e.Err)
}

func (e *ImportError) Unwrap() error { return e.Err }

// displayPath is rel, or abs for an error built without a workspace-relative name.
func displayPath(rel, abs string) string {
	if rel != "" {
		return rel
	}
	return abs
}
