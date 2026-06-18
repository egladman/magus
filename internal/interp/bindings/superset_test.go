package bindings

import (
	"context"
	"testing"

	buzzeng "github.com/egladman/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupersetModules verifies that registerHostModules exposes the magus host
// methods under the same bare names as Buzz's stdlib (a superset), and that the
// old magus/extra aggregate is gone.
func TestSupersetModules(t *testing.T) {
	ctx := context.Background()
	sess := buzzeng.NewSession(ctx, buzzeng.WithEmbedded())
	defer sess.Close()
	registerHostModules(ctx, sess)

	hasKey := func(t *testing.T, module, key string) {
		t.Helper()
		mod, ok := sess.SyntheticModule(module)
		require.True(t, ok, "module %q not registered", module)
		_, ok = mod.MapGet(key)
		assert.True(t, ok, "module %q missing key %q", module, key)
	}

	// os: Buzz stdlib (env, execute) and magus (exec, which) coexist on one module.
	hasKey(t, "os", "env")     // Buzz stdlib
	hasKey(t, "os", "execute") // Buzz stdlib
	hasKey(t, "os", "exec")    // magus
	hasKey(t, "os", "which")   // magus

	// fs: Buzz stdlib (makeDirectory) plus magus (glob, readFile).
	hasKey(t, "fs", "makeDirectory") // Buzz stdlib
	hasKey(t, "fs", "glob")          // magus
	hasKey(t, "fs", "readFile")      // magus

	// crypto: Buzz stdlib (hash) plus magus digests and the byte-level companions.
	hasKey(t, "crypto", "hash")       // Buzz stdlib
	hasKey(t, "crypto", "sha256Hex")  // magus
	hasKey(t, "crypto", "hmacSha256") // magus/extra companion merged in

	// Modules Buzz's stdlib lacks become new bare imports.
	hasKey(t, "http", "get")         // magus
	hasKey(t, "http", "download")    // magus/extra http companion merged in
	hasKey(t, "vcs", "shortHash")    // magus
	hasKey(t, "archive", "compress") // magus
	hasKey(t, "time", "format")      // magus
	hasKey(t, "markdown", "toHtml")  // magus

	// The aggregate import and its byte-level siblings are gone.
	for _, gone := range []string{"magus/extra", "magus/extra/http", "magus/extra/crypto"} {
		_, ok := sess.SyntheticModule(gone)
		assert.False(t, ok, "module %q should no longer be registered", gone)
	}
}
