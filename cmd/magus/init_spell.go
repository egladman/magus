package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egladman/magus/internal/interactive"
)

// spellHandleRe validates a spell handle: a letter followed by letters, digits,
// '-' or '_'. It mirrors the charset target and charm names use, so a handle
// typed as `import "spells/<name>/spell.buzz"` is always a legal identifier.
var spellHandleRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// initSpellCmd implements `magus init spell <name>`: scaffold a new spell at
// spells/<name>/spell.buzz with the mgs_ contract stubbed, every function
// documented inline, and a runnable test block. It is the authoring on-ramp the
// bundled spells never provided: a new spell starts from a working, tested
// example instead of a blank file and a hunt through docs/spells.md.
func initSpellCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("init spell", flag.ContinueOnError)
	force := fs.Bool("force", false, "Overwrite an existing spell.buzz")
	dir := fs.String("dir", "spells", "Parent directory the spell package is created under")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus init spell <name> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Scaffold a new spell at <dir>/<name>/spell.buzz with the mgs_ contract")
		fmt.Fprintln(os.Stderr, "stubbed, each function documented inline, and a runnable test block.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("init spell: expected exactly one spell name")
	}
	name := fs.Arg(0)
	if !spellHandleRe.MatchString(name) {
		return fmt.Errorf("init spell: %q is not a valid handle: use a letter followed by letters, digits, '-' or '_'", name)
	}

	pkgDir := filepath.Join(*dir, name)
	path := filepath.Join(pkgDir, "spell.buzz")
	if _, err := os.Stat(path); err == nil && !*force {
		return fmt.Errorf("init spell: %s already exists (use --force to overwrite)", path)
	}
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return fmt.Errorf("init spell: create %s: %w", pkgDir, err)
	}
	if err := os.WriteFile(path, []byte(spellScaffold(name)), 0o644); err != nil {
		return fmt.Errorf("init spell: write %s: %w", path, err)
	}
	slog.InfoContext(ctx, "init spell: wrote spell", slog.String("path", path))
	printInitSpellNextSteps(name, pkgDir, path)
	return nil
}

// spellScaffold renders the starter spell.buzz for a handle. It is a complete,
// self-contained built-in-style spell (it imports only the pure magus/target and
// magus/charm modules, so it compiles without host bindings) plus a test block,
// with teaching comments on every contract function.
func spellScaffold(name string) string {
	return strings.ReplaceAll(spellScaffoldTemplate, "SPELLNAME", name)
}

// spellScaffoldTemplate is the scaffold body; SPELLNAME is replaced with the
// handle. Kept as one raw string so the generated file reads exactly as authored.
const spellScaffoldTemplate = `// spells/SPELLNAME/spell.buzz - a magus spell for the SPELLNAME toolchain.
//
// A spell is a library of tool-native operations (ops) your targets compose. See
// docs/spells.md for the full model. magus loads a spell through the exported
// mgs_ contract below; mgs_getName is the only required function.
//
// Bind it from a magusfile (a directory import resolves to this spell.buzz):
//
//     import "spells/SPELLNAME" as SPELLNAME;
//     magus.project({ "spells": [SPELLNAME] });
//
// then compose an op into a target:
//
//     export fun build(args: [str]) > void { SPELLNAME.build(); }
//
// Test this file:  magus buzz -t --embedded spells/SPELLNAME/spell.buzz

import "magus/target";
import "magus/charm";
import "assert";

// mgs_getName is the ONLY required function: the handle a magusfile imports this
// spell under, and the prefix magus shows in ` + "`magus describe`" + `. Keep it short.
export fun mgs_getName() > str { return "SPELLNAME"; }

// mgs_listRequiredGlobs declares the inputs this spell's ops read (its "needs").
// magus hashes every matching file into the cache key, so editing one busts the
// cache and re-runs; a file no glob matches never triggers a rebuild. ` + "`root`" + ` is
// the project directory, usually ignorable. Under-declare here and you replay a
// stale build; see docs/cache.md.
export fun mgs_listRequiredGlobs(root: str) > [str] {
    return ["**/*.SPELLNAME"];
}

// mgs_listProvidedGlobs declares the outputs an op writes (its "provides"). magus
// snapshots these on a miss and replays them on a hit. Omit it (as here) for a
// read-only tool - a linter or formatter check - that writes nothing.
// export fun mgs_listProvidedGlobs() > [str] { return ["dist/**"]; }

// mgs_listClaimedGlobs declares files this spell OWNS, for affected-set
// attribution (which project a changed file belongs to). Unlike needs, claims are
// never hashed or snapshotted. Omit it when needs already covers your files.
// export fun mgs_listClaimedGlobs() > [str] { return ["**/*.SPELLNAME"]; }

// An op is a function returning a Command: the bin and argv magus forks directly
// (no shell, one process). It receives a Target but must NOT read or branch on it
// - the argv has to be static so magus can cache, charm-patch, and preview it
// without running. Branch on runtime state in a magusfile function target instead.
fun build(target: Target) > Command {
    return Command{bin = "SPELLNAME", args = ["build"]};
}

// A charm is a named, shared modifier applied as an RFC 6902 patch over the argv
// (see docs/charms.md). Declare one per op in the ` + "`charms`" + ` table. ` + "`rw`" + ` is the
// built-in read->write toggle. ` + "`after(args, anchor, vals)`" + ` inserts by value, so
// the position survives a later change to the base argv - no counted index to drift.
fun lint(target: Target) > Command {
    final args = ["lint", "."];
    return Command{bin = "SPELLNAME", args = args, charms = {
        "rw": after(args, "lint", ["--fix"]),  // ` + "`SPELLNAME.lint:rw`" + ` applies fixes
    }};
}

// mgs_listTargets registers your ops: a map of op name to its handler. This is
// what makes ops runnable. Omit it and the spell still binds but contributes no
// targets - magus warns when you bind such a spell, since it is almost always a
// forgotten or misnamed mgs_listTargets.
export fun mgs_listTargets() > {str: fun(Target) Command} {
    return {"build": build, "lint": lint};
}

// A test block runs under ` + "`magus buzz -t`" + `. Call an op with a throwaway Target{}
// and assert on the Command it returns: ops are static, so this pins the exact
// argv magus will fork. assert.equal deep-compares lists.
test "getName returns the handle" {
    assert.equal(mgs_getName(), "SPELLNAME", "handle");
}

test "build op forks the expected command" {
    final cmd = build(Target{});
    assert.equal(cmd.bin, "SPELLNAME", "binary");
    assert.equal(cmd.args, ["build"], "argv");
}
`

// printInitSpellNextSteps prints actionable hints after scaffolding a spell.
// Gated on interactive.Enabled() so MAGUS_HINTS_ENABLED=false silences it.
func printInitSpellNextSteps(name, pkgDir, path string) {
	if !interactive.Enabled() {
		return
	}
	// A directory import (pkgDir) resolves to the spell.buzz inside it; the test
	// harness takes the file path directly.
	importPath := filepath.ToSlash(pkgDir)
	interactive.Emit(os.Stderr, fmt.Sprintf("spell scaffolded: %s", path))
	interactive.Emit(os.Stderr, fmt.Sprintf("test it:  magus buzz -t --embedded %s", filepath.ToSlash(path)))
	interactive.Emit(os.Stderr, fmt.Sprintf("bind it:  import \"%s\" as %s;  then  magus.project({ \"spells\": [%s] });", importPath, name, name))
}
