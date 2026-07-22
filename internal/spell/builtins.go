package spell

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"sync"
	"time"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

//go:generate go run ../../cmd/magus-utils spells -spells ../../spells -out gen

// builtinFS holds the compiled bytecode of every built-in spell, one <name>.bo per
// spell keyed by runtime spell name (e.g. go.bo, ts.bo), generated from
// spells/<dir>/spell.buzz by magus-utils spells. The source directory (e.g. "golang")
// is only a filesystem location; the blob and the registry key are the runtime name.
//
//go:embed gen/*.bo
var builtinFS embed.FS

// Builtins is the built-in spell registry, keyed by runtime spell name (Descriptor.Name,
// e.g. "go", "ts"), loaded once. This is the registry callers use: users refer to a
// spell by its name, and registration is by name. The source directory a spell was
// authored in (e.g. "golang" for "go") has no runtime presence.
var Builtins = sync.OnceValue(loadBuiltins)

// BuiltinsHash is the SHA-256 of a stable serialization of the built-in registry,
// hex-encoded; it changes when any built-in spell's spec changes (mixed into cache
// keys). Hashing the resolved registry rather than the raw bytecode keeps it tied
// to spell semantics, not the Buzz compiler's output.
var BuiltinsHash = sync.OnceValue(func() string {
	b, err := codec.Marshal(Builtins())
	if err != nil {
		panic("magus/spell: marshal builtin spells: " + err.Error())
	}
	h := sha256.New()
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
})

// BuiltinOps returns each built-in spell's op names keyed by runtime spell name. It is
// the surface the dry-run tracer needs to build spell stubs without depending on the
// full Descriptor; derived from Builtins() so it cannot drift from the registry.
func BuiltinOps() map[string][]string {
	b := Builtins()
	out := make(map[string][]string, len(b))
	for name, spec := range b {
		out[name] = spec.OpNames()
	}
	return out
}

// loadBuiltins recovers every embedded built-in's bytecode, runs it, and resolves
// the exported mgs_ functions into a Descriptor, keyed by runtime spell name
// (Descriptor.Name). It backs Builtins.
//
// It panics on failure: the .bo blobs are a trusted build artifact, so a failure
// here is a broken build (stale bytecode, a compiler/format mismatch), not bad
// user input — the same severity as a missing embedded asset.
func loadBuiltins() map[string]Descriptor {
	entries, err := builtinFS.ReadDir("gen")
	if err != nil {
		panic("magus/spell: read embedded built-ins: " + err.Error())
	}
	// loadBuiltins runs once, lazily, off a process-init background ctx that carries
	// no telemetry provider, so the warm recording below is nil-gated to a no-op
	// today; withBuiltinResolve keeps the resolve "builtin" attribute correct should
	// a provider-carrying ctx ever drive this path.
	ctx := withBuiltinResolve(context.Background())
	p := providerFrom(ctx)
	out := make(map[string]Descriptor, len(entries))
	for _, e := range entries {
		blob, err := builtinFS.ReadFile("gen/" + e.Name())
		if err != nil {
			panic("magus/spell: read built-in " + e.Name() + ": " + err.Error())
		}
		chunk, err := vm.UnmarshalChunk(blob)
		if err != nil {
			panic("magus/spell: unmarshal built-in " + e.Name() + ": " + err.Error())
		}
		start := time.Now()
		sess := buzz.NewSession(ctx, buzz.WithEmbedded())
		if err := sess.ExecChunk(ctx, chunk); err != nil {
			_ = sess.Close()
			panic("magus/spell: exec built-in " + e.Name() + ": " + err.Error())
		}
		spec, err := Resolve(ctx, sess)
		_ = sess.Close()
		if err != nil {
			panic("magus/spell: resolve built-in " + e.Name() + ": " + err.Error())
		}
		if p != nil {
			p.RecordBuzzSpellBuiltinsWarm(ctx, time.Since(start).Seconds(), spec.Name)
		}
		out[spec.Name] = spec
	}
	return out
}
