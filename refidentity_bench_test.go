package magus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// newIdentitySweepWorkspace builds a cache-backed workspace sized to reproduce the
// shape measured on this repo: nProjects projects, each with targetsPerProject
// targets from one spell (all sharing the SAME step.Sources baseline, as buildStep
// gives every target absent a TargetInputs override - see run.go:334) and
// filesPerProject source files on disk. At nProjects=9, targetsPerProject=11,
// filesPerProject=340 this lands at ~99 targets and ~3060 files, matching the
// ~94-target, ~3000-file, 36.7s-sweep measurement behind this benchmark. defaultCharms
// becomes the workspace's configured default_charms (m.cfg.DefaultCharms), what
// IdentifyRef now reads directly instead of taking as a parameter.
func newIdentitySweepWorkspace(b *testing.B, nProjects, targetsPerProject, filesPerProject int, defaultCharms []string) *Magus {
	b.Helper()
	root := b.TempDir()
	require.NoError(b, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	for i := 0; i < nProjects; i++ {
		projPath := fmt.Sprintf("svc%02d", i)
		spellName := fmt.Sprintf("zzz-bench-identity-spell-%02d", i)

		targets := make([]string, targetsPerProject)
		for t := range targets {
			targets[t] = fmt.Sprintf("target%02d", t)
		}
		// Unprefixed glob: baseStep joins it with the project path, mirroring how a
		// real spell declares sources relative to the project it binds to.
		s := spells.NewSpell(spellName,
			spells.WithTargets(targets...),
			spells.WithSources("**/*.go"),
		)
		project.DefaultSpellRegistry().RegisterSpell(s)
		b.Cleanup(func(name string) func() {
			return func() { project.DefaultSpellRegistry().UnregisterSpell(name) }
		}(spellName))

		projDir := filepath.Join(root, projPath)
		require.NoError(b, os.MkdirAll(projDir, 0o755))
		require.NoError(b, os.WriteFile(filepath.Join(projDir, "magusfile.buzz"), []byte(""), 0o644))
		for f := 0; f < filesPerProject; f++ {
			body := fmt.Sprintf("package p\n\nvar V%d = %d\n", f, f)
			require.NoError(b, os.WriteFile(filepath.Join(projDir, fmt.Sprintf("f%04d.go", f)), []byte(body), 0o644))
		}

		reg.RegisterProject(projPath, WithSpell(spellName))
	}

	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg), WithLoadedConfig(config.Config{DefaultCharms: defaultCharms}))
	require.NoError(b, err, "Open")
	b.Cleanup(func() { _ = m.Close() })
	return m
}

// BenchmarkIdentifyRefSweep measures a full no-match sweep - the shape `magus query
// output <ref-not-found>` hits on the error path IdentifyRef exists for. "outdeadbeef" is
// ref-shaped (so LooksLikeRef doesn't short-circuit it) but matches no live key, so every
// candidate target/charm combination is tried and none returns early.
func BenchmarkIdentifyRefSweep(b *testing.B) {
	m := newIdentitySweepWorkspace(b, 9, 11, 340, []string{"rw"})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		matches, err := m.IdentifyRef(ctx, "outdeadbeef0000")
		if err != nil {
			b.Fatal(err)
		}
		if len(matches) != 0 {
			b.Fatalf("expected no matches, got %d", len(matches))
		}
	}
}
