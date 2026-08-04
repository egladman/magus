// Command magus-spelldocs generates Markdown reference documentation for every
// built-in spell in the internal/spellruntime registry. It mirrors cmd/magus-docs: walk
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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"

	"github.com/egladman/magus/internal/docs"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/spells"
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
	aliases     []string // old clean URLs that redirect here (the short spell names pre-rename)
}

// spellMeta is the authored metadata keyed by runtime spell name (Descriptor.Name).
// Every built-in must have an entry; main() exits non-zero on a spell with no
// entry, so a newly added spell can't ship an unlabeled page.
var spellMeta = map[string]spellInfo{
	"go": {
		dir: "golang", language: "Go",
		description: "Go toolchain spell: build, test, vet, fmt, gofumpt, mod-tidy, golangci-lint, and govulncheck as magus ops.",
		intro:       "The `go` spell wires the Go toolchain into a magusfile: each op forks a `go` (or `gofmt`/`gofumpt`) subcommand directly, with no shell. golangci-lint and govulncheck run from PATH - pinned by whatever the workspace uses (mise, asdf, a system package) - and the spell's named version probes record which, so a linter upgrade moves the cache key.",
		tags:        []string{"go", "golang", "build", "test", "lint"},
	},
	"rust": {
		dir: "rust", language: "Rust",
		description: "Rust toolchain spell: cargo build, check, test, clippy, fmt, run, doc, bench, audit, and deny as magus ops.",
		intro:       "The `rust` spell wires Cargo into a magusfile. Each op forks a `cargo` subcommand directly with no baked-in flags: the build profile (`--release`) and lint policy (`-- -D warnings`) are the magusfile's to append through the op's args option.",
		tags:        []string{"rust", "cargo", "build", "test"},
		aliases:     []string{"concepts/spells/rs"},
	},
	"typescript": {
		dir: "typescript", language: "TypeScript",
		description: "TypeScript toolchain spell: tsc, eslint, prettier, vitest, jest, install, and audit run through the project package manager.",
		aliases:     []string{"concepts/spells/ts"},
		intro:       "The `typescript` spell wires a TypeScript project's tooling into a magusfile, forking each tool through the project package manager. Its ops record `pnpm`, and the engine substitutes each project's detected manager (npm, yarn, or bun) at fork time: the project's `package_manager` option first, then `package.json`'s `packageManager` field, then the lockfile, then pnpm. It is an opaque spell: `preflight` composes the individual checks into one target.",
		tags:        []string{"typescript", "node", "eslint", "vitest"},
	},
	"python": {
		dir: "python", language: "Python",
		description: "Python toolchain spell: pytest, ruff check/format, mypy, pyright, black, pip-audit, and uv build/sync as magus ops.",
		intro:       "The `python` spell wires a Python project's tooling into a magusfile through `uv`. Tests, linting (ruff), type-checking (mypy or pyright), and formatting (ruff-format or black) run as `uv run` subcommands so they resolve from the project's locked environment; uv-sync installs that environment for a preflight target.",
		tags:        []string{"python", "uv", "pytest", "ruff"},
		aliases:     []string{"concepts/spells/py"},
	},
	"markdown": {
		dir: "markdown", language: "Markdown",
		description: "Markdown docs spell: markdownlint, prettier, and typos for linting, formatting, and spell-checking prose.",
		intro:       "The `markdown` spell lints and formats Markdown. `markdownlint` enforces style, `prettier` checks formatting, and `typos` catches known misspellings; the `rw` charm turns each check into an in-place fix.",
		tags:        []string{"markdown", "docs", "prettier", "lint"},
		aliases:     []string{"concepts/spells/md"},
	},
	"docker": {
		dir: "docker", language: "Docker",
		description: "Docker spell: image build, build-check, buildx, hadolint linting, and trivy/scout image scanning.",
		intro:       "The `docker` spell forks the `docker` CLI (and `hadolint`) to build images and lint Dockerfiles. `docker-build-check` runs the builder's `--check` preflight without producing an image, and `trivy`/`docker-scout` scan a built image for the `security` target.",
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
		description: "Cosign spell: sign, attest, verify, verify-attestation, and the blob pair for artifacts and images.",
		intro:       "The `cosign` spell forks the Sigstore `cosign` CLI to sign, attest, and verify artifacts - registry images and plain files (the blob pair) alike. Signing and attestation pass `--yes` for non-interactive (CI) use. Credentials ride the environment, and the sandbox scrubs it: pass `COSIGN_*` (and, for keyless OIDC in GitHub Actions, `ACTIONS_ID_TOKEN_REQUEST_URL`/`ACTIONS_ID_TOKEN_REQUEST_TOKEN`) through `sandbox.env.passthrough` in magus.yaml, or signing fails with an auth error that never names the scrub.",
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
		description: "Bash spell: shellcheck linting, shfmt formatting, and bats tests for shell scripts.",
		intro:       "The `bash` spell covers the shell lifecycle: `shellcheck` lints every `.sh`/`.bash` file, `shfmt` formats them (diff by default, rewrite under `rw`), and `bats` runs a test suite. Extensionless shebang scripts are a known limit - list them explicitly via op args.",
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

	builtins := spellruntime.Builtins()
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

	// Orphan sweep: every .md in the output dir must be a page this run wrote.
	// The dir is wholly generated, so a leftover page is a stranded artifact the
	// drift gate cannot see (the generator no longer writes it, so it never
	// changes) - exactly how the pre-rename ts/py/rs/md pages survived for
	// months asserting spell names that no longer existed. A rename now fails
	// here instead of stranding a page.
	entries, err := os.ReadDir(*outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus-spelldocs: read %s: %v\n", *outDir, err)
		os.Exit(1)
	}
	written := make(map[string]bool, len(names))
	for _, name := range names {
		written[name+".md"] = true
	}
	var orphans []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || written[e.Name()] {
			continue
		}
		orphans = append(orphans, e.Name())
	}
	if len(orphans) > 0 {
		fmt.Fprintf(os.Stderr, "magus-spelldocs: %s holds page(s) no registered spell produces: %s; delete them (add an alias on the successor page if the URL should redirect)\n",
			*outDir, strings.Join(orphans, ", "))
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "magus-spelldocs: wrote %d spell docs to %s\n", len(names), *outDir)
}

// resolvedArgv joins an op's command and arguments into the shell-free argv a
// target forks (`go tool golangci-lint run ./...`). Empty for a marker op with no
// command (an opaque spell's aggregate target).
func resolvedArgv(op spells.Op) string {
	if op.Bin == "" {
		return ""
	}
	return strings.TrimSpace(op.Bin + " " + strings.Join(op.Args, " "))
}

func renderSpell(d spells.Descriptor) string {
	meta := spellMeta[d.Name]
	var b strings.Builder

	tags := append([]string{d.Name, "spell"}, meta.tags...)
	tags = append(tags, "tools")
	docs.WriteFrontmatter(&b, docs.Frontmatter{
		Title:       d.Name + " spell",
		Aliases:     meta.aliases,
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
	// Named probes for the secondary tools a spell drives (hadolint, shellcheck,
	// cargo-audit, ...). Invisible before this line existed: docker.md showed only
	// `docker --version` while the hadolint probe silently fed the cache key.
	if len(d.VersionCmds) > 0 {
		names := make([]string, 0, len(d.VersionCmds))
		for n := range d.VersionCmds {
			names = append(names, n)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, n := range names {
			parts = append(parts, fmt.Sprintf("`%s` (`%s`)", n, strings.Join(d.VersionCmds[n], " ")))
		}
		fmt.Fprintf(&b, "**Named probes:** %s - each records UNPROBED when the tool is absent, and moves the cache key when installed.\n\n", strings.Join(parts, ", "))
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
			fmt.Fprintf(&b, "%s\n\n", spellruntime.DescribeCharm(charm, op.Args))
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
	fmt.Fprintf(b, "Every op is invoked as `%s[\"<op>\"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:\n\n", invoker)
	fmt.Fprintln(b, "| Key | Type | Description | Source |")
	fmt.Fprintln(b, "|-----|------|-------------|--------|")
	fmt.Fprintf(b, "| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `%s[\"<op>\"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | %s |\n", invoker, src("args"))
	fmt.Fprintf(b, "| `stdin` | `str` | Data written to the command's standard input. | %s |\n", src("stdin"))
	fmt.Fprintln(b)
	fmt.Fprintln(b)
	fmt.Fprintf(b, "Working directory and environment are NOT options: they ride the context, as `%s[\"<op>\"](ctx.withCwd(\"sub\"))` and `%s[\"<op>\"](ctx.withEnv({\"CGO_ENABLED\": \"0\"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.\n\n", invoker, invoker)
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
func injectSpellList(path string, builtins map[string]spells.Descriptor, names []string) error {
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
	// Engineering notes stay in the source: a TODO block (and everything after
	// it) is a maintainer conversation, not op reference - bash.md once carried
	// the shellcheck op's whole internal TODO, resolve.go pointers and all.
	if i := strings.Index(doc, "TODO"); i >= 0 {
		doc = doc[:i]
	}
	fields := strings.Fields(strings.ReplaceAll(doc, "\n", " "))
	return strings.Join(fields, " ")
}

// charmPatchJSON renders a charm's ops as indented JSON (RFC 6902), the exact
// notation the docs expose behind a details dropdown.
func charmPatchJSON(c spells.Charm) string {
	data, err := json.MarshalIndent(c.Ops, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(data)
}

func sortedCharmNames(charms map[string]spells.Charm) []string {
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
