// Command magus-spelldocs generates Markdown reference documentation for every
// built-in spell in the internal/spell registry. It mirrors cmd/magus-docs: walk
// the registry, emit one page per spell to docs/spells/<name>.md (injecting a
// per-op example from spells/examples/<name>/<op>.buzz), and refresh the
// built-in-spell table on the /spells/ landing (docs/spells.md) between its marker
// comments.
//
//	go run ./cmd/magus-spelldocs -out ./docs/spells
//
// The output under docs/spells is committed; re-run this (via `magus run
// content-generate docs`) after changing a spell. Spell examples are marked runnable as
// dry-run (<!-- magus-run-recorder -->): the playground's recorder (Eval + WithTracer)
// records the tool invocations a target would trigger instead of executing them,
// so a reader sees the plan without the toolchain installed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"

	"github.com/egladman/magus/internal/docs"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/types"
)

// spellInfo is the authored, editorial metadata for a built-in spell (purpose,
// intro, tags, source directory) that isn't derivable from the Descriptor. The
// ops table and per-op detail are generated; this only supplies the prose.
type spellInfo struct {
	dir         string   // source directory under spells/ (e.g. "golang" for the "go" spell)
	language    string   // human language/toolchain label, or "" for language-agnostic tools
	description string   // one-sentence frontmatter description
	intro       string   // one-paragraph page intro
	tags        []string // extra frontmatter tags (name + "spell" are always added)
}

// spellMeta is the authored metadata keyed by runtime spell name (Descriptor.Name).
// Every built-in must have an entry; main() exits non-zero on a spell with no
// entry, so a newly added spell can't ship an unlabeled page.
var spellMeta = map[string]spellInfo{
	"go": {
		dir: "golang", language: "Go",
		description: "Go toolchain spell: build, test, vet, fmt, mod-tidy, golangci-lint, and govulncheck as magus ops.",
		intro:       "The `go` spell wires the Go toolchain into a magusfile: each op forks a `go` (or `gofmt`) subcommand directly, with no shell. Lint and vulnerability scanning run as `go tool` invocations so they resolve from the module's tool block rather than PATH.",
		tags:        []string{"go", "golang", "build", "test", "lint"},
	},
	"rs": {
		dir: "rust", language: "Rust",
		description: "Rust toolchain spell: cargo build, test, clippy, fmt, and clean as magus ops.",
		intro:       "The `rs` spell wires Cargo into a magusfile. Each op forks a `cargo` subcommand directly; `cargo-build` builds in release mode and `cargo-clippy` denies warnings, matching a CI-gating default.",
		tags:        []string{"rust", "cargo", "build", "test"},
	},
	"ts": {
		dir: "typescript", language: "TypeScript",
		description: "TypeScript toolchain spell: tsc, eslint, prettier, and vitest run through the project package manager.",
		intro:       "The `ts` spell wires a TypeScript project's tooling into a magusfile, forking each tool through the project package manager (`pnpm exec`). It is an opaque spell: `preflight` composes the individual checks into one target.",
		tags:        []string{"typescript", "node", "eslint", "vitest"},
	},
	"py": {
		dir: "python", language: "Python",
		description: "Python toolchain spell: pytest, ruff check/format, and uv build/clean as magus ops.",
		intro:       "The `py` spell wires a Python project's tooling into a magusfile through `uv`. Tests, linting (ruff), and formatting run as `uv run` subcommands so they resolve from the project's locked environment.",
		tags:        []string{"python", "uv", "pytest", "ruff"},
	},
	"md": {
		dir: "markdown", language: "Markdown",
		description: "Markdown docs spell: markdownlint and prettier for linting and formatting prose.",
		intro:       "The `md` spell lints and formats Markdown. `markdownlint` enforces style, and `prettier` checks formatting; the `rw` charm turns the check into an in-place rewrite.",
		tags:        []string{"markdown", "docs", "prettier", "lint"},
	},
	"docker": {
		dir: "docker", language: "Docker",
		description: "Docker spell: image build, build-check, buildx, and hadolint Dockerfile linting.",
		intro:       "The `docker` spell forks the `docker` CLI (and `hadolint`) to build images and lint Dockerfiles. `docker-build-check` runs the builder's `--check` preflight without producing an image.",
		tags:        []string{"docker", "container", "image", "hadolint"},
	},
	"buf": {
		dir: "buf", language: "Protobuf",
		description: "Buf spell: protobuf build, lint, format, and code generation.",
		intro:       "The `buf` spell forks the `buf` CLI to build, lint, format, and generate from Protobuf sources. It declares `gen/**` as its outputs so magus caches generated code correctly.",
		tags:        []string{"protobuf", "buf", "codegen", "lint"},
	},
	"cosign": {
		dir: "cosign", language: "",
		description: "Cosign spell: keyless sign, attest, and verify for container artifacts.",
		intro:       "The `cosign` spell forks the Sigstore `cosign` CLI to sign, attest, and verify artifacts. Signing and attestation pass `--yes` for non-interactive (CI) use.",
		tags:        []string{"cosign", "sigstore", "signing", "supply-chain"},
	},
	"buzz": {
		dir: "buzz", language: "Buzz",
		description: "Buzz spell: check and test .buzz sources, plus run them through the magus interpreter.",
		intro:       "The `buzz` spell checks and tests Buzz sources. Each op finds every `.buzz` file and runs `buzz --check`, `buzz --test`, or the magus interpreter over it.",
		tags:        []string{"buzz", "gopherbuzz", "check", "test"},
	},
	"bash": {
		dir: "bash", language: "Shell",
		description: "Bash spell: shellcheck linting for shell scripts.",
		intro:       "The `bash` spell lints shell scripts. Its single op finds every `.sh`/`.bash` file and runs `shellcheck` over the set.",
		tags:        []string{"bash", "shell", "shellcheck", "lint"},
	},
}

// repoRoot is the module root, resolved once so the args table's source links
// resolve line numbers from the working tree. spellOptsSource is the file whose
// spellOptsFromBuzz parses each op options-map key.
var repoRoot = docs.RepoRoot()

const spellOptsSource = "internal/interp/bindings/spell_object.go"

// spellsDir is the path to the repo's spells/ directory, relative to the caller's
// cwd. It is explicit (the -spells flag) rather than discovered by walking up to a
// module root: the caller knows where spells live in its own context and says so,
// which keeps resolution contextual instead of imposing a global root.
var spellsDir = "spells"

func main() {
	outDir := flag.String("out", "docs/spells", "output directory for spell docs")
	spellsFlag := flag.String("spells", "spells", "path to the repo's spells/ directory, relative to cwd")
	flag.Parse()
	spellsDir = *spellsFlag

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "magus-spelldocs: mkdir %s: %v\n", *outDir, err)
		os.Exit(1)
	}

	builtins := ispell.Builtins()
	names := make([]string, 0, len(builtins))
	for name := range builtins {
		if _, ok := spellMeta[name]; !ok {
			fmt.Fprintf(os.Stderr, "magus-spelldocs: built-in spell %q has no spellMeta entry; add one\n", name)
			os.Exit(1)
		}
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		d := builtins[name]
		if err := os.WriteFile(filepath.Join(*outDir, name+".md"), []byte(renderSpell(d)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "magus-spelldocs: %s: %v\n", name, err)
			os.Exit(1)
		}
	}
	// The at-a-glance list of all spells lives on the concept page (docs/spells.md,
	// the /spells/ landing) rather than a separate index, so /spells/ both explains
	// what a spell is and lists every one. Injected between markers so it stays
	// generated (drift-gated), not hand-maintained. docs/spells.md is the sibling
	// of the per-spell output dir.
	spellsMd := filepath.Join(filepath.Dir(*outDir), "spells.md")
	if err := injectSpellList(spellsMd, builtins, names); err != nil {
		fmt.Fprintf(os.Stderr, "magus-spelldocs: inject spell list: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "magus-spelldocs: wrote %d spell docs to %s\n", len(names), *outDir)
}

// resolvedArgv joins an op's command and arguments into the shell-free argv a
// target forks (`go tool golangci-lint run ./...`). Empty for a marker op with no
// command (an opaque spell's aggregate target).
func resolvedArgv(op types.SpellOp) string {
	if op.Bin == "" {
		return ""
	}
	return strings.TrimSpace(op.Bin + " " + strings.Join(op.Args, " "))
}

func renderSpell(d ispell.Descriptor) string {
	meta := spellMeta[d.Name]
	var b strings.Builder

	tags := append([]string{d.Name, "spell"}, meta.tags...)
	tags = append(tags, "tools")
	docs.WriteFrontmatter(&b, docs.Frontmatter{
		Title:       d.Name + " spell",
		Description: meta.description,
		Tags:        dedupe(tags),
	})

	fmt.Fprintf(&b, "# %s\n\n", d.Name)
	fmt.Fprintf(&b, "%s\n\n", meta.intro)

	// Facts derivable from the Descriptor, as a short definition list.
	fmt.Fprintf(&b, "**Runtime name:** `%s` (source `spells/%s/`)\n\n", d.Name, meta.dir)
	if len(d.VersionCmd) > 0 {
		fmt.Fprintf(&b, "**Version probe:** `%s`\n\n", strings.Join(d.VersionCmd, " "))
	} else {
		fmt.Fprintf(&b, "**Version probe:** none\n\n")
	}
	if len(d.Provides) > 0 {
		fmt.Fprintf(&b, "**Provides:** %s\n\n", codeList(d.Provides))
	}
	if d.Opaque {
		fmt.Fprintf(&b, "**Opaque:** yes (its outputs are not enumerable, so magus treats the whole workspace as the cache input).\n\n")
	}

	opDocs := parseOpDocs(meta.dir)
	ops := d.OpNames()

	writeArgsSection(&b, d.Name)

	// Per-op detail sections (each op is its own h2, so they land in the page TOC).
	for _, name := range ops {
		op := d.Ops[name]
		fmt.Fprintf(&b, "## %s\n\n", name)
		if doc := opDocs[name]; doc != "" {
			fmt.Fprintf(&b, "%s\n\n", doc)
		}
		if cmd := resolvedArgv(op); cmd != "" {
			fmt.Fprintf(&b, "**Command:** `%s`\n\n", cmd)
		} else {
			fmt.Fprintf(&b, "**Command:** none; this op composes the spell's other ops (see the intro).\n\n")
		}
		// Each charm gets its own subheading (### <name>) with a plain-English
		// summary of what it does to the argv, then a collapsible block with the
		// exact JSON Patch for readers who want the raw notation.
		for _, cn := range sortedCharmNames(op.Charms) {
			charm := op.Charms[cn]
			fmt.Fprintf(&b, "### %s\n\n", cn)
			fmt.Fprintf(&b, "%s\n\n", describeCharm(charm, op.Args))
			fmt.Fprintln(&b, "<details class=\"charm-patch\">")
			fmt.Fprintln(&b, "<summary>JSON Patch</summary>")
			fmt.Fprintln(&b)
			fmt.Fprintln(&b, "```json")
			fmt.Fprintln(&b, charmPatchJSON(charm))
			fmt.Fprintln(&b, "```")
			fmt.Fprintln(&b)
			fmt.Fprintln(&b, "</details>")
			fmt.Fprintln(&b)
		}
		if ex := readExample(d.Name, name); ex != "" {
			fmt.Fprintln(&b, "### Example")
			fmt.Fprintln(&b)
			// The recorder marker turns this block into a Run button that dry-runs
			// the example under the playground's recording host (see
			// dry.Eval + WithTracer), tracing the ops the target would
			// fork rather than executing them.
			fmt.Fprintln(&b, "<!-- magus-run-recorder -->")
			fmt.Fprintln(&b, "```buzz")
			b.WriteString(ex)
			if !strings.HasSuffix(ex, "\n") {
				b.WriteByte('\n')
			}
			fmt.Fprintln(&b, "```")
			fmt.Fprintln(&b)
		}
	}

	return b.String()
}

// writeArgsSection emits the "Passing arguments to ops" reference: the options
// map every op invocation accepts, keyed by type. invoker is the binding a caller
// writes before the op index - a spell's own name on its page (`go["<op>"]`), or
// the generic `spell` on the index. One helper keeps the per-spell pages and the
// index identical, mirroring the real contract in
// internal/interp/bindings.spellOptsFromBuzz.
func writeArgsSection(b *strings.Builder, invoker string) {
	// src links a key to the line in spell_object.go where it is parsed, mirroring
	// the source links on the module docs' method signatures.
	src := func(key string) string {
		return fmt.Sprintf("[source](%s)", docs.SourceURL(repoRoot, spellOptsSource, `MapGet("`+key+`")`))
	}
	fmt.Fprintf(b, "## Passing arguments to ops\n\n")
	fmt.Fprintf(b, "Every op is invoked as `%s[\"<op>\"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:\n\n", invoker)
	fmt.Fprintln(b, "| Key | Type | Description | Source |")
	fmt.Fprintln(b, "|-----|------|-------------|--------|")
	fmt.Fprintf(b, "| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `%s[\"<op>\"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | %s |\n", invoker, src("args"))
	fmt.Fprintf(b, "| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | %s |\n", src("cwd"))
	fmt.Fprintf(b, "| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | %s |\n", src("env"))
	fmt.Fprintf(b, "| `stdin` | `str` | Data written to the command's standard input. | %s |\n", src("stdin"))
	fmt.Fprintln(b)
	fmt.Fprintf(b, "Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).\n\n")
}

// Markers bounding the generated built-in-spell table inside docs/spells.md. The
// generator rewrites everything between them; the hand-written concept prose around
// them is untouched.
const (
	spellListBegin = "<!-- BEGIN SPELL LIST"
	spellListEnd   = "<!-- END SPELL LIST -->"
)

// injectSpellList rewrites the generated spell table between the marker comments in
// docs/spells.md (the /spells/ landing), so that page both explains the spell
// concept and lists every built-in with a link to its reference page. It preserves
// the marker lines and everything outside them, so re-running is idempotent.
func injectSpellList(path string, builtins map[string]ispell.Descriptor, names []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	src := string(data)

	bi := strings.Index(src, spellListBegin)
	if bi < 0 {
		return fmt.Errorf("%s: missing %q marker", path, spellListBegin)
	}
	// Keep the whole BEGIN marker line (up to and including its newline).
	afterBegin := strings.IndexByte(src[bi:], '\n')
	if afterBegin < 0 {
		return fmt.Errorf("%s: malformed begin marker", path)
	}
	headEnd := bi + afterBegin + 1
	ei := strings.Index(src, spellListEnd)
	if ei < 0 || ei < headEnd {
		return fmt.Errorf("%s: missing %q marker after begin", path, spellListEnd)
	}

	var table strings.Builder
	fmt.Fprintln(&table, "| Spell | Language | Ops | Purpose |")
	fmt.Fprintln(&table, "|-------|----------|-----|---------|")
	for _, name := range names {
		d := builtins[name]
		meta := spellMeta[name]
		lang := meta.language
		if lang == "" {
			lang = "-"
		}
		// Link is relative to docs/spells.md: spells/<name>.md -> /spells/<name>/.
		fmt.Fprintf(&table, "| [`%s`](spells/%s.md) | %s | %d | %s |\n", name, name, lang, len(d.Ops), meta.description)
	}

	rebuilt := src[:headEnd] + table.String() + src[ei:]
	if rebuilt == src {
		return nil // already up to date; avoid a needless write
	}
	return os.WriteFile(path, []byte(rebuilt), 0o644)
}

// parseOpDocs reads spells/<dir>/spell.buzz and returns op-name -> doc-comment,
// recovered from the source AST. Built-in Descriptors carry no per-op Doc (the
// bytecode strips it), so the source is the only place the handler comments
// survive. It maps each op key to its handler via the mgs_listTargets return map,
// then reads that handler's FunDecl.Doc. Any read/parse miss yields an empty map,
// so a spell with no source-side docs renders no op descriptions.
func parseOpDocs(dir string) map[string]string {
	src, err := os.ReadFile(filepath.Join(spellsDir, dir, "spell.buzz"))
	if err != nil {
		return nil
	}
	prog, err := buzz.ParseEmbedded(string(src))
	if err != nil {
		return nil
	}

	funcDoc := map[string]string{}     // handler function name -> its doc comment
	opToHandler := map[string]string{} // op key -> handler function name
	for _, stmt := range prog.Stmts {
		fd, ok := stmt.(*ast.FunDecl)
		if !ok {
			continue
		}
		if fd.Doc != "" {
			funcDoc[fd.Name] = cleanDoc(fd.Doc)
		}
		if fd.Name == "mgs_listTargets" {
			collectOpHandlers(fd, opToHandler)
		}
	}

	out := make(map[string]string, len(opToHandler))
	for op, handler := range opToHandler {
		if doc := funcDoc[handler]; doc != "" {
			out[op] = doc
		}
	}
	return out
}

// collectOpHandlers walks mgs_listTargets's body for its `return {"op": handler}`
// map and records each op key -> handler function name. Non-identifier values
// (none exist in the built-ins) are skipped.
func collectOpHandlers(fd *ast.FunDecl, out map[string]string) {
	if fd.Body == nil {
		return
	}
	for _, stmt := range fd.Body.Stmts {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		m, ok := ret.Value.(*ast.MapExpr)
		if !ok {
			continue
		}
		for i := range m.Keys {
			key, ok := m.Keys[i].(*ast.StringLit)
			if !ok {
				continue
			}
			ident, ok := m.Values[i].(*ast.IdentExpr)
			if !ok {
				continue
			}
			out[key.Val] = ident.Name
		}
	}
}

// cleanDoc normalizes a Buzz doc comment (already stripped of // markers by the
// lexer) into a single flowed paragraph: it joins wrapped lines and collapses
// blank-line-separated paragraphs into one, so it drops cleanly into a Markdown
// sentence. Plain ASCII in, plain ASCII out.
func cleanDoc(doc string) string {
	fields := strings.Fields(strings.ReplaceAll(doc, "\n", " "))
	return strings.Join(fields, " ")
}

// describeCharm renders a one-sentence, human summary of a charm's JSON-Patch ops
// against the op's base argv (args), so a reader sees what the charm does without
// reading JSON Patch. It resolves each patch's target index back to the original
// argument (e.g. "replaces `-l` with `-w`") when it can. It handles only the shapes
// the built-ins use (add/remove/replace on argv indices); anything unusual falls
// back to naming the op count.
func describeCharm(c types.Charm, args []string) string {
	parts := make([]string, 0, len(c.Ops))
	for _, p := range c.Ops {
		switch p.Op {
		case "add":
			if strings.HasSuffix(p.Path, "/-") {
				parts = append(parts, "appends `"+p.Value+"`")
			} else {
				parts = append(parts, "inserts `"+p.Value+"`")
			}
		case "remove":
			if orig := argAt(args, p.Path); orig != "" {
				parts = append(parts, "drops `"+orig+"`")
			} else {
				parts = append(parts, "drops an argument")
			}
		case "replace":
			if orig := argAt(args, p.Path); orig != "" {
				parts = append(parts, "replaces `"+orig+"` with `"+p.Value+"`")
			} else {
				parts = append(parts, "sets an argument to `"+p.Value+"`")
			}
		case "move":
			parts = append(parts, "moves an argument")
		case "copy":
			parts = append(parts, "copies an argument")
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("A %d-op argv patch.", len(c.Ops))
	}
	return capitalize(strings.Join(parts, ", ")) + "."
}

// argAt resolves a JSON Pointer path ("/1") to the base argv element it targets, or
// "" when the path is not a plain in-range index (e.g. "/-" append, or out of
// range). Used to name the argument a replace/remove charm acts on.
func argAt(args []string, path string) string {
	if !strings.HasPrefix(path, "/") {
		return ""
	}
	i, err := strconv.Atoi(path[1:])
	if err != nil || i < 0 || i >= len(args) {
		return ""
	}
	return args[i]
}

// charmPatchJSON renders a charm's ops as indented JSON (RFC 6902), the exact
// notation the docs expose behind a details dropdown.
func charmPatchJSON(c types.Charm) string {
	data, err := json.MarshalIndent(c.Ops, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(data)
}

// capitalize upper-cases the first byte of s (ASCII), leaving the rest unchanged.
func capitalize(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}

func sortedCharmNames(charms map[string]types.Charm) []string {
	names := make([]string, 0, len(charms))
	for n := range charms {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// readExample reads spells/examples/<name>/<op>.buzz, or "" when absent so a
// missing example simply skips the Example section (same contract as
// cmd/magus-docs.readExample).
func readExample(spell, op string) string {
	data, err := os.ReadFile(filepath.Join(spellsDir, "examples", spell, op+".buzz"))
	if err != nil {
		return ""
	}
	return string(data)
}

// codeList renders a slice as comma-separated code spans.
func codeList(items []string) string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = "`" + s + "`"
	}
	return strings.Join(out, ", ")
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	out := items[:0:0]
	for _, s := range items {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
