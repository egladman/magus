package spell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/parsecache"
	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
)

//go:generate go run ../../cmd/magus-utils types -type Path -out gen/types/path.buzz
//go:generate go run ../../cmd/magus-utils types -type Install -out gen/types/install.buzz
//go:generate go run ../../cmd/magus-utils types -type Manifest -out gen/types/manifest.buzz
//go:generate go run ../../cmd/magus-utils types -type Target -out gen/types/target.buzz
//go:generate go run ../../cmd/magus-utils types -type PatchOp -out gen/types/patchop.buzz
//go:generate go run ../../cmd/magus-utils types -type Charm -out gen/types/charm.buzz
//go:generate go run ../../cmd/magus-utils types -type VersionKey -out gen/types/versionkey.buzz
//go:generate go run ../../cmd/magus-utils types -type VersionBounds -out gen/types/versionbounds.buzz
//go:generate go run ../../cmd/magus-utils types -type Tool -out gen/types/tool.buzz
//go:generate go run ../../cmd/magus-utils types -type SandboxAllow -out gen/types/sandboxallow.buzz
//go:generate go run ../../cmd/magus-utils types -type SandboxEnv -out gen/types/sandboxenv.buzz
//go:generate go run ../../cmd/magus-utils types -type SandboxCache -out gen/types/sandboxcache.buzz
//go:generate go run ../../cmd/magus-utils types -type Sandbox -out gen/types/sandbox.buzz
//go:generate go run ../../cmd/magus-utils types -type CommentBlock -out gen/types/commentblock.buzz
//go:generate go run ../../cmd/magus-utils types -type Quote -out gen/types/quote.buzz
//go:generate go run ../../cmd/magus-utils types -type CommentSyntax -out gen/types/commentsyntax.buzz
//go:generate go run ../../cmd/magus-utils types -type Language -out gen/types/language.buzz
//go:generate go run ../../cmd/magus-utils types -type Secret -out gen/types/secret.buzz
//go:generate go run ../../cmd/magus-utils types -type Hint -out gen/types/hint.buzz
//go:generate go run ../../cmd/magus-utils types -type Command -out gen/types/command.buzz
//go:generate go run ../../cmd/magus-utils types -type Service -out gen/types/service.buzz
//go:generate go run ../../cmd/magus-utils types -type SymbolIndexer -out gen/types/symbolindexer.buzz
//go:generate go run ../../cmd/magus-utils types -type Project -out gen/types/project.buzz
//go:generate go run ../../cmd/magus-utils types -type ReleaseCycle -out gen/types/releasecycle.buzz
//go:generate go run ../../cmd/magus-utils types -type Lifecycle -out gen/types/lifecycle.buzz

// Builtins is the built-in spell registry, keyed by runtime spell name (spells.Descriptor.Name,
// e.g. "go"), loaded once. This is the registry callers use: users refer to a
// spell by its name, and registration is by name. The source directory a spell was
// authored in (e.g. "golang" for "go") has no runtime presence.
func Builtins() map[string]spells.Descriptor { return shipped().builtins }

// ShippedDir returns the directory of spells.Shipped() that holds the spell name. A
// built-in is found by its registered name (go is golang); a spell that ships only as
// source is found by its directory path under spells/ (endoflife-date, harness/cursor).
func ShippedDir(name string) (string, bool) {
	dirs := shipped().dirs
	if dir, ok := dirs[name]; ok {
		return dir, true
	}
	if slices.Contains(slices.Collect(maps.Values(dirs)), name) {
		return "", false // golang is the go built-in's directory, not a spell named golang
	}
	if _, err := fs.Stat(spells.Shipped(), name+"/spell.buzz"); err != nil {
		return "", false
	}
	return name, true
}

// BuiltinsHash is the SHA-256 of a stable serialization of the built-in registry,
// hex-encoded; it changes when any built-in spell's spec changes (mixed into cache
// keys). Hashing the resolved registry rather than the source keeps it tied to spell
// semantics, not to a comment edit.
var BuiltinsHash = sync.OnceValue(func() string {
	b, err := json.Marshal(Builtins())
	if err != nil {
		panic("magus/spell: marshal builtin spells: " + err.Error())
	}
	h := sha256.New()
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
})

// BuiltinOps returns each built-in spell's op names keyed by runtime spell name. It is
// the surface the dry-run tracer needs to build spell stubs without depending on the
// full spells.Descriptor; derived from Builtins() so it cannot drift from the registry.
func BuiltinOps() map[string][]string {
	b := Builtins()
	out := make(map[string][]string, len(b))
	for name, spec := range b {
		out[name] = spec.OpNames()
	}
	return out
}

// shippedSpells is what loading spells.Shipped() found.
type shippedSpells struct {
	builtins map[string]spells.Descriptor
	dirs     map[string]string // built-in name -> its directory
}

var shipped = sync.OnceValue(loadShipped)

// loadShipped compiles every shipped spell that is a built-in and resolves its mgs_
// functions into a spells.Descriptor.
//
// A built-in is a spell whose source compiles against the spell type modules alone. One
// that imports a host module (endoflife-date imports http) fails in that session with
// BZZ2001 and ships as source only: `magus spell pull` copies it into a workspace, which
// loads it with the host surface. Nothing lists which spell is which.
//
// Only a top-level directory is a candidate. A top-level directory with no spell.buzz
// holds nested spells (harness/cursor), which are providers a workspace wires by import
// path, so they ship as source only even when they would compile here.
//
// It panics on any other failure: the sources are compiled into the binary, so a spell
// that does not load is a broken build, the same severity as a missing embedded asset.
func loadShipped() shippedSpells {
	entries, err := fs.ReadDir(spells.Shipped(), ".")
	if err != nil {
		panic("magus/spell: read shipped spells: " + err.Error())
	}
	// This runs once, lazily, off a process-init background ctx that carries no
	// telemetry provider, so the warm recording below is nil-gated to a no-op today;
	// withBuiltinResolve keeps the resolve "builtin" attribute correct should a
	// provider-carrying ctx ever drive this path.
	ctx := withBuiltinResolve(context.Background())
	p := providerFrom(ctx)
	out := shippedSpells{builtins: map[string]spells.Descriptor{}, dirs: map[string]string{}}
	for _, e := range entries {
		dir := e.Name()
		src, err := fs.ReadFile(spells.Shipped(), dir+"/spell.buzz")
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			panic("magus/spell: read shipped spell " + dir + ": " + err.Error())
		}
		start := time.Now()
		spec, err := compileBuiltin(ctx, string(src))
		if errors.As(err, new(hostImportError)) {
			continue
		}
		if err != nil {
			panic("magus/spell: load built-in " + dir + ": " + err.Error())
		}
		if prev, dup := out.dirs[spec.Name]; dup {
			panic(fmt.Sprintf("magus/spell: spells/%s and spells/%s both register %q", prev, dir, spec.Name))
		}
		if p != nil {
			p.RecordBuzzSpellBuiltinsWarm(ctx, time.Since(start).Seconds(), spec.Name)
		}
		out.builtins[spec.Name] = spec
		out.dirs[spec.Name] = dir
	}
	return out
}

// hostImportError is a spell source importing a module the built-in session does not
// offer. It wraps the session's BZZ2001.
type hostImportError struct {
	module string
	err    error
}

func (e hostImportError) Error() string {
	return fmt.Sprintf("imports %q, a host module, so it is not a built-in: %v", e.module, e.err)
}

func (e hostImportError) Unwrap() error { return e.err }

// compileBuiltin runs src in a session offering only the spell type modules, with no
// file imports, and resolves it. An import of anything else is a hostImportError.
func compileBuiltin(ctx context.Context, src string) (spells.Descriptor, error) {
	sess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithoutFileImports(), buzz.WithParseCache(parsecache.Shared()))
	defer func() { _ = sess.Close() }()
	sess.SetModuleDecls(SpellModulePath, SpellModuleSource)
	sess.SetModuleDecls(CharmModulePath, CharmModuleSource)
	sess.SetModuleDecls(LintModulePath, LintModuleSource)
	var unresolved string
	sess.SetModuleResolver(func(importPath string) (vm.Value, bool) {
		if unresolved == "" {
			unresolved = importPath
		}
		return vm.Null, false
	})
	if err := sess.Exec(ctx, src); err != nil {
		if unresolved != "" && errors.Is(err, buzz.UnresolvedImport) {
			return spells.Descriptor{}, hostImportError{module: unresolved, err: err}
		}
		return spells.Descriptor{}, err
	}
	return Resolve(ctx, sess)
}
