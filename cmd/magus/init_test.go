package main

import (
	"context"
	"io"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStarterMagusfileNoRemovedAPI guards P3-23/P3-24: starterMagusfileBuzz is the
// canonical example magusfile referenced from the docs (see its doc comment in
// init.go), so a removed spelling here doesn't just fail to load; it teaches new
// users something MGS1025 will reject. RemovedAPINames comes from
// removedMagusfileAPI in internal/interp/runtime.go, the same table MGS1025 checks
// against, so this list can't drift out of sync with what actually fires the
// diagnostic. The scan covers comments too, since a real magusfile parse would
// ignore a removed name inside one (see MGS1025.md's Detection section) but the
// starter's comments are exactly what a new user reads.
func TestStarterMagusfileNoRemovedAPI(t *testing.T) {
	for _, name := range interp.RemovedAPINames() {
		removed := "magus." + name
		assert.NotContains(t, starterMagusfileBuzz, removed,
			"starter magusfile.buzz uses removed API %q (see MGS1025)", removed)
	}
}

// loadStarterEmbedded runs src through the same embedded-mode session the magusfile
// engine uses (the mode `magus buzz --embedded` drives): parse, check, and run the
// top level. It returns the diagnostic Exec raises, if any. This is the real loader,
// not a string scan: it is what surfaces a checker diagnostic like BZZ1006 that a
// parse (buzz.ParseEmbedded) and a grep both miss.
// TestStarterMagusfileChecksClean is the enforcement point the string scan in
// TestStarterMagusfileNoRemovedAPI cannot be: it LOADS and CHECKS the embedded starter
// through the magusfile engine and requires zero error-level diagnostics, so the
// scaffold `magus init` writes is one that `magus run build` (the exact next command
// init suggests) can load. A BZZ1006 (a proc\exec under `> void` missing `!> any`)
// shipped once precisely because the scan never compiled the template. Unused-import
// warnings (BZZ3001) are tolerated here; the hard requirement is no error diagnostic.
// The guard that this check cannot pass vacuously is TestScripts/init_scaffold_runs,
// which runs the scaffolded targets rather than only loading them.
func TestStarterMagusfileChecksClean(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	bindings.RegisterModules(ctx, sess, bindings.WithScriptOutput(io.Discard))
	bindings.RegisterMagusNamespace(ctx, sess)
	bindings.RegisterSpellDecls(sess)
	require.NoError(t, bindings.DeclareMagusTypes(sess))
	require.NoError(t, sess.Exec(ctx, starterMagusfileBuzz),
		"embedded starter magusfile must check clean under the magusfile engine")
}
