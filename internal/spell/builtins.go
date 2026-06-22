package spell

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"strings"
	"sync"

	"github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/internal/codec"
)

//go:generate go run ../../cmd/magus-utils spells -spells ../../spells -out gen

// builtinFS holds the compiled bytecode of every built-in spell, one <name>.bo
// per spell, generated from spells/<name>/spell.buzz by magus-utils spells.
//
//go:embed gen/*.bo
var builtinFS embed.FS

// Builtins is the built-in spell registry, keyed by runtime spell name (Descriptor.Name,
// e.g. "go", "js"), loaded once. This is the registry callers use: users refer to a
// spell by its name, and registration is by name. The source directory (e.g.
// "golang") is an internal detail of how the bytecode is generated and embedded —
// see builtinsByDir.
var Builtins = sync.OnceValue(func() map[string]Descriptor {
	byDir := builtinsByDir()
	out := make(map[string]Descriptor, len(byDir))
	for _, s := range byDir {
		out[s.Name] = s
	}
	return out
})

// builtinsByDir is the registry keyed by source directory (e.g. "golang"), the
// shape loadBuiltins recovers from the embedded <dir>.bo blobs. It stays internal:
// the only things that need the directory keying are BuiltinsHash (whose bytes feed
// cache keys, so they must stay stable) and the in-package golden test.
var builtinsByDir = sync.OnceValue(loadBuiltins)

// BuiltinsHash is the SHA-256 of a stable serialization of the built-in registry,
// hex-encoded; it changes when any built-in spell's spec changes (mixed into cache
// keys). Hashing the resolved registry rather than the raw bytecode keeps it tied
// to spell semantics, not the Buzz compiler's output.
var BuiltinsHash = sync.OnceValue(func() string {
	b, err := codec.Marshal(builtinsByDir())
	if err != nil {
		panic("magus/spell: marshal builtin spells: " + err.Error())
	}
	h := sha256.New()
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
})

// loadBuiltins recovers every embedded built-in's bytecode, runs it, and resolves
// the exported mgs_ functions into a Descriptor, keyed by source dir name (e.g. "golang";
// Descriptor.Name is the runtime name). It backs builtinsByDir.
//
// It panics on failure: the .bo blobs are a trusted build artifact, so a failure
// here is a broken build (stale bytecode, a compiler/format mismatch), not bad
// user input — the same severity as a missing embedded asset.
func loadBuiltins() map[string]Descriptor {
	entries, err := builtinFS.ReadDir("gen")
	if err != nil {
		panic("magus/spell: read embedded built-ins: " + err.Error())
	}
	ctx := context.Background()
	out := make(map[string]Descriptor, len(entries))
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".bo")
		blob, err := builtinFS.ReadFile("gen/" + e.Name())
		if err != nil {
			panic("magus/spell: read built-in " + e.Name() + ": " + err.Error())
		}
		chunk, err := vm.UnmarshalChunk(blob)
		if err != nil {
			panic("magus/spell: unmarshal built-in " + name + ": " + err.Error())
		}
		sess := buzz.NewSession(ctx, buzz.WithEmbedded())
		if err := sess.ExecChunk(ctx, chunk); err != nil {
			_ = sess.Close()
			panic("magus/spell: exec built-in " + name + ": " + err.Error())
		}
		spec, err := Resolve(ctx, sess, CommandOps)
		_ = sess.Close()
		if err != nil {
			panic("magus/spell: resolve built-in " + name + ": " + err.Error())
		}
		out[name] = spec
	}
	return out
}
