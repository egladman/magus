// Command magus-spells-gen compiles each built-in spell's Buzz source
// (magus/spells/<name>/spell.buzz) to bytecode and writes magus/internal/spell/
// gen/<name>.bo, which the spell package embeds at build time. The runtime
// loader recovers each blob with UnmarshalChunk and runs it to extract the
// spell's mgs_ functions — so the .buzz files are the source of truth and the committed
// .bo blobs are a generated build artifact.
//
// Only self-contained spells are compiled: a spell whose source imports a host
// module (e.g. spells/github/actions, a function-op spell) needs bindings a bare compile
// can't resolve, so it is not a built-in and is skipped.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egladman/gopherbuzz"
	ispell "github.com/egladman/magus/internal/spell"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "magus-spells-gen:", err)
		os.Exit(1)
	}
}

func run() error {
	// Defaults assume invocation from the spell package dir via go:generate.
	spellsDir := flag.String("spells", "../../spells", "directory of <name>/spell.buzz spell sources")
	outDir := flag.String("out", "gen", "directory to write <name>.bo bytecode into")
	flag.Parse()

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
		name := e.Name()
		srcPath := filepath.Join(*spellsDir, name, "spell.buzz")
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
		blob, err := chunk.Marshal()
		_ = sess.Close()
		if err != nil {
			return fmt.Errorf("marshal %s: %w", srcPath, err)
		}
		outPath := filepath.Join(*outDir, name+".bo")
		if err := os.WriteFile(outPath, blob, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		built = append(built, name)
	}
	if len(built) == 0 {
		return fmt.Errorf("no built-in spells found under %s", *spellsDir)
	}
	sort.Strings(built)
	fmt.Printf("magus-spells-gen: compiled %d built-ins -> %s: %s\n", len(built), *outDir, strings.Join(built, " "))
	return nil
}
