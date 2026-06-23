package spell

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltins_NonEmpty(t *testing.T) {
	m := Builtins()
	require.NotEmpty(t, m, "Builtins() returned empty map")
	for key, s := range m {
		assert.NotEmptyf(t, s.Name, "Builtins()[%q].Name is empty", key)
		// The registry is keyed by runtime name, so the key is the spell's Name.
		assert.Equalf(t, key, s.Name, "Builtins() key %q != Descriptor.Name %q", key, s.Name)
	}
}

func TestBuiltins_KeyedByName(t *testing.T) {
	m := Builtins()
	// The golang spell renames itself to "go": it must be reachable by name…
	assert.Contains(t, m, "go", `Builtins()["go"] not found`)
	// …and not by its source directory.
	assert.NotContains(t, m, "golang", `Builtins()["golang"] present — registry is keyed by name, not source dir`)
}

func TestBuiltinsHash_Format(t *testing.T) {
	h := BuiltinsHash()
	assert.Len(t, h, 64, "BuiltinsHash() should be 64 chars (SHA-256 hex)")
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			assert.Failf(t, "non-hex character", "BuiltinsHash() contains non-hex character %q", c)
			break
		}
	}
}

func TestBuiltinsHash_Stable(t *testing.T) {
	h1, h2 := BuiltinsHash(), BuiltinsHash()
	assert.Equal(t, h1, h2, "BuiltinsHash() not stable")
}

// TestBuiltinBytecodeParity proves the embedded-bytecode pipeline end to end for
// every self-contained built-in: authored .buzz -> Compile -> Marshal ->
// UnmarshalChunk -> ExecChunk -> Resolve yields a Descriptor identical to the one in
// the in-process registry (Builtins(), keyed by runtime name). It walks the spells/
// source tree, skipping function-op spells (e.g. github) that import host modules
// and so are not bare-compilable built-ins.
func TestBuiltinBytecodeParity(t *testing.T) {
	want := Builtins()
	ctx := context.Background()
	// spellsDir is relative to this package (internal/spell).
	const spellsDir = "../../spells"
	dirs, err := os.ReadDir(spellsDir)
	require.NoError(t, err)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := d.Name()
		src, err := os.ReadFile(filepath.Join(spellsDir, dir, "spell.buzz"))
		if err != nil {
			continue // not a spell dir
		}
		// Build-time half: inline the magus/target module exactly as the built-in
		// generator does so the chunk is self-contained. A spell that imports host
		// modules (e.g. github) is not a bare-compilable built-in — skip it.
		combined, ok := SelfContainedBuiltinSource(string(src))
		if !ok {
			continue
		}
		t.Run(dir, func(t *testing.T) {
			cs := buzz.NewSession(ctx, buzz.WithEmbedded())
			defer cs.Close()
			chunk, err := cs.Compile(combined)
			require.NoError(t, err, "compile")
			blob, err := chunk.Marshal()
			require.NoError(t, err, "marshal")
			// Runtime half: recover from bytecode and resolve the spec.
			rechunk, err := vm.UnmarshalChunk(blob)
			require.NoError(t, err, "unmarshal")
			rs := buzz.NewSession(ctx, buzz.WithEmbedded())
			defer rs.Close()
			require.NoError(t, rs.ExecChunk(ctx, rechunk), "exec chunk")
			got, err := Resolve(ctx, rs, CommandOps)
			require.NoError(t, err, "resolve")
			w, ok := want[got.Name]
			require.Truef(t, ok, "built-in %q (name %q) not in registry", dir, got.Name)
			assert.Equalf(t, w, got, "Descriptor mismatch for %q", dir)
		})
	}
}

func TestGoSpell_TidyTarget(t *testing.T) {
	goSpell := Builtins()["go"]
	tidy, ok := goSpell.Ops["go-mod-tidy"]
	require.Truef(t, ok, "go spell has no go-mod-tidy target; targets: %v", goSpell.OpNames())
	// Default (no write charm): check mode via --diff (non-zero exit if changes
	// are needed — safe for CI gating).
	assert.Equal(t, "go", tidy.Cmd)
	assert.Equal(t, []string{"mod", "tidy", "--diff"}, tidy.Args)
	// rw charm drops --diff (remove /2) so tidy actually applies the changes.
	w, ok := tidy.Charms["rw"]
	require.True(t, ok, "tidy has no rw charm")
	assert.Equal(t, []types.PatchOp{{Op: "remove", Path: "/2"}}, w.Ops)
}
