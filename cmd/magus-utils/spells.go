// Subcommand `spells` compiles each built-in spell's Buzz source
// (spells/<dir>/spell.buzz) to bytecode and writes internal/spell/gen/<name>.bo,
// named by the spell's runtime name (mgs_getName, e.g. "go" for spells/golang) so
// the source directory never enters the runtime registry. The spell package embeds
// the blobs at build time; the runtime loader recovers each with UnmarshalChunk and
// runs it to extract the spell's mgs_ functions — so the .buzz files are the source
// of truth and the committed .bo blobs are a generated build artifact.
//
// Only self-contained spells are compiled: a spell whose source imports a host
// module (e.g. spells/github/actions, a function-op spell) needs bindings a bare
// compile can't resolve, so it is not a built-in and is skipped.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/libs/gopherbuzz"
)

func runSpells(args []string) error {
	// Defaults assume invocation from the spell package dir via go:generate.
	fs := flag.NewFlagSet("spells", flag.ExitOnError)
	spellsDir := fs.String("spells", "../../spells", "directory of <name>/spell.buzz spell sources")
	outDir := fs.String("out", "gen", "directory to write <name>.bo bytecode into")
	if err := fs.Parse(args); err != nil {
		return err
	}

	entries, err := os.ReadDir(*spellsDir)
	if err != nil {
		return fmt.Errorf("read spells dir: %w", err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir out: %w", err)
	}

	ctx := context.Background()
	var built []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		srcPath := filepath.Join(*spellsDir, dir, "spell.buzz")
		src, err := os.ReadFile(srcPath)
		if err != nil {
			continue // not a spell dir
		}
		// A built-in may import only the pure-types magus/target module; we
		// inline its source so the compiled chunk is self-contained (imports
		// leave no bytecode, so the type would otherwise be absent at load).
		// A spell importing any host module (e.g. spells/github) can't be a
		// bare-compiled built-in and is skipped.
		combined, ok := ispell.SelfContainedBuiltinSource(string(src))
		if !ok {
			continue
		}
		sess := buzz.NewSession(ctx, buzz.WithEmbedded())
		chunk, err := sess.Compile(combined)
		if err != nil {
			_ = sess.Close()
			return fmt.Errorf("compile %s: %w", srcPath, err)
		}
		// Resolve the spell so the blob is named by its runtime name (mgs_getName,
		// e.g. "go"), not its source directory (e.g. "golang"): the loader keys the
		// registry on the runtime name, so the dir never enters the runtime.
		if err := sess.ExecChunk(ctx, chunk); err != nil {
			_ = sess.Close()
			return fmt.Errorf("exec %s: %w", srcPath, err)
		}
		spec, err := ispell.Resolve(ctx, sess)
		if err != nil {
			_ = sess.Close()
			return fmt.Errorf("resolve %s: %w", srcPath, err)
		}
		blob, err := chunk.Marshal()
		_ = sess.Close()
		if err != nil {
			return fmt.Errorf("marshal %s: %w", srcPath, err)
		}
		outPath := filepath.Join(*outDir, spec.Name+".bo")
		if err := os.WriteFile(outPath, blob, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		built = append(built, spec.Name)
	}
	if len(built) == 0 {
		return fmt.Errorf("no built-in spells found under %s", *spellsDir)
	}
	// Remove stale blobs (a renamed spell, a removed source dir) so the embedded set
	// is exactly the current built-ins - the .bo names are runtime names, not dirs.
	keep := make(map[string]bool, len(built))
	for _, n := range built {
		keep[n] = true
	}
	existing, err := filepath.Glob(filepath.Join(*outDir, "*.bo"))
	if err != nil {
		return fmt.Errorf("glob out: %w", err)
	}
	for _, p := range existing {
		if !keep[strings.TrimSuffix(filepath.Base(p), ".bo")] {
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("remove stale %s: %w", p, err)
			}
		}
	}
	sort.Strings(built)
	fmt.Printf("spells: compiled %d built-ins -> %s: %s\n", len(built), *outDir, strings.Join(built, " "))
	return nil
}
