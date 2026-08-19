// Command magus-skilldocs generates the agent-skill reference: one page per
// embedded skill showing both curated permutations, plus an index.
//
//	go run ./cmd/magus-skilldocs -out ./docs/reference/skills
//
// The pages are generated from the same embedded bodies `magus agent install`
// writes, so a skill edit cannot leave the documentation describing a version
// nobody installs. That is the whole reason this is a generator and not a
// hand-written page: the alternative is a copy that silently rots, which is the
// failure this repo builds generators to avoid.
//
// The rendered skill text is fenced verbatim so a reader can copy it, but the page
// says plainly that `magus agent install` is the supported way to get it - a copied
// skill carries no provenance stamp, so `magus graph verify` cannot tell whether it
// is current.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/types"
)

func main() {
	out := flag.String("out", "", "Directory to write the skill reference into (required)")
	// The skill bodies are embedded in cmd/magus, and go:embed cannot reach across
	// package directories - so they are read from the tree, exactly as
	// magus-spelldocs reads -spells. The path is the directory CONTAINING skills/,
	// because that is what the catalog's body paths are relative to.
	src := flag.String("src", "../cmd/magus", "Directory containing the skills/ tree")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "magus-skilldocs: -out is required")
		os.Exit(2)
	}
	if err := run(*out, *src); err != nil {
		fmt.Fprintf(os.Stderr, "magus-skilldocs: %v\n", err)
		os.Exit(1)
	}
}

// run binds the embedded skill sources the same way cmd/magus does.
//
// The real KnowledgeSchemaVersion is threaded through, not zero. It used to be
// zero on the grounds that it only feeds the install stamp and these pages did
// not reproduce the stamp - but they do now (writeStampTable), and a page
// claiming `knowledge-schema-version: 0` would be documenting a stamp no install
// ever writes. A generated page showing a value that cannot occur is worse than
// no page.
func run(outDir, srcDir string) error {
	// The agents section is part of the content digest, so it has to be the REAL
	// one. Passing "" produced a digest no install ever writes, which the stamp
	// table would then have published as fact.
	section, err := os.ReadFile(filepath.Join(srcDir, "agents-section.md"))
	if err != nil {
		return fmt.Errorf("read agents-section.md: %w", err)
	}
	cat := agent.NewCatalog(os.DirFS(srcDir), string(section), types.KnowledgeSchemaVersion)
	defs, err := cat.EmbeddedSkills()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	full := make([]agent.AgentSkill, 0, len(defs))
	simple := make([]agent.AgentSkill, 0, len(defs))
	for _, def := range defs {
		f, err := cat.Render(def, agent.VariantFull)
		if err != nil {
			return err
		}
		s, err := cat.Render(def, agent.VariantSimple)
		if err != nil {
			return err
		}
		full = append(full, f)
		simple = append(simple, s)
		page := renderSkill(cat, f, s)
		if err := emit.File(filepath.Join(outDir, f.Name+".md"), []byte(page)); err != nil {
			return err
		}
	}
	if err := emit.File(filepath.Join(outDir, "index.md"), []byte(renderIndex(full, simple))); err != nil {
		return err
	}
	return prune(outDir, full)
}

// prune deletes pages for skills this binary no longer ships.
//
// A generator that owns its writes but not its deletions cannot rename anything.
// Renaming a skill wrote the new page and left the old one - still published,
// still linked from nothing, still describing a skill nobody can install - and no
// gate reported it, because a drift check compares a generator's DECLARED outputs
// against what it wrote and an extra file is in neither set. The magus-docs
// generator has a test for exactly this orphan; this one had neither the test nor
// the pruning.
//
// Scoped to the pages this generator emits (<skill>.md plus index.md) rather than
// to the whole directory, so a hand-written page dropped in here is not collateral.
func prune(outDir string, shipped []agent.AgentSkill) error {
	keep := make(map[string]bool, len(shipped)+1)
	keep["index.md"] = true
	for _, s := range shipped {
		keep[s.Name+".md"] = true
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || keep[name] || !strings.HasPrefix(name, "magus-") || !strings.HasSuffix(name, ".md") {
			continue
		}
		if err := os.Remove(filepath.Join(outDir, name)); err != nil {
			return fmt.Errorf("remove stale skill page %s: %w", name, err)
		}
		fmt.Fprintf(os.Stderr, "magus-skilldocs: removed stale page %s\n", name)
	}
	return nil
}

// renderSkill writes one skill's page: what it is for, then both permutations
// fenced verbatim.
//
// The full form comes first and unwrapped because it is the default install; the
// simple form follows in a <details> so the page does not read as two walls of
// near-identical text. Only the simple one is collapsed - the comparison a reader
// wants is "what did the short one drop", and that reads better by expanding the
// short one against the long one already on screen.
func renderSkill(cat *agent.Catalog, full, simple agent.AgentSkill) string {
	var b strings.Builder
	// The two byte counts are stated as FACTS; the SSG turns them into a percentage
	// and a <progress> bar (engine/meta.buzz:insertSizeRatio). Presentation is the
	// renderer's job, and a generated page hands it data rather than markup.
	var aliases string
	// A renamed skill keeps its old page URL working. The rename is recorded on the
	// catalog row rather than in a redirect list beside the site, so the page that
	// carries the redirect is generated from the same fact that caused it.
	for _, old := range agent.FormerNames(full.Name) {
		aliases += fmt.Sprintf("aliases:\n  - reference/skills/%s\n", old)
	}
	fmt.Fprintf(&b, "---\ntitle: %s\ngenerated_from: cmd/magus/skills/%s/SKILL.md\ndescription: %q\ntags: [agents, skills, %s]\n%s"+
		"skill_full_bytes: %d\nskill_simple_bytes: %d\n---\n\n",
		full.Name, full.Name, firstSentence(full.Description), full.Name, aliases, len(full.Body), len(simple.Body))
	fmt.Fprintf(&b, "# %s\n\n", full.Name)
	fmt.Fprintf(&b, "%s\n\n", full.Description)
	fmt.Fprintf(&b, "Install it, rather than copying from this page:\n\n")
	fmt.Fprintf(&b, "```sh\nmagus agent install .claude/skills   # writes both forms below\n```\n\n")
	fmt.Fprintf(&b, "An installed copy carries a provenance stamp, so `magus graph verify` can tell "+
		"you when a magus upgrade has made it stale. Text copied from this page carries none.\n\n")

	writeStampTable(&b, cat, full)

	b.WriteString("## Full form\n\nEvery mechanical step spelled out, plus the rationale for each. Installed as the " +
		"`<name>-full` twin: loaded by name rather than always, so a reader who needs the long " +
		"form can ask for it without every session carrying it.\n\n")
	writeFenced(&b, full.Body)
	fmt.Fprintf(&b, "## Short form\n\nThe enumeration dropped, the judgment kept - for the most "+
		"capable readers, not the least; the bar under the heading above shows by how much. This is "+
		"the always-loaded primary. Both are hand-authored from one source body; see "+
		"[Skills](../../guides/integrations/agents/skills.md) for the difference.\n\n")
	b.WriteString("<details>\n<summary>Show the short form</summary>\n\n")
	writeFenced(&b, simple.Body)
	b.WriteString("\n</details>\n")
	return b.String()
}

// writeFenced wraps body in a code fence LONGER than any backtick run inside it.
//
// A fixed three-backtick fence is broken here, and it shipped that way: every skill
// body contains its own fenced examples (18 of them in magus-buzz-write), and Markdown
// fences do not nest - the body's first ```sh closed the wrapper, so most of the page
// rendered as live Markdown instead of verbatim text. CommonMark closes a fence only
// on a run at least as long as the opener, so measuring the longest run and adding
// one is the fix.
func writeFenced(w *strings.Builder, body string) {
	longest := 0
	for _, line := range strings.Split(body, "\n") {
		start := 0
		for start < len(line) && start < 3 && line[start] == ' ' {
			start++
		}
		run := 0
		for start+run < len(line) && line[start+run] == '`' {
			run++
		}
		if run > longest {
			longest = run
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	w.WriteString(fence + "markdown\n" + body + "\n" + fence + "\n\n")
}

// renderIndex is the section landing: a table of every skill with what each
// permutation costs, so the choice can be made on numbers rather than vibes.
func renderIndex(full, simple []agent.AgentSkill) string {
	var b strings.Builder
	b.WriteString("---\ntitle: Agent skills\n")
	b.WriteString("description: \"Every skill magus installs, in both curated permutations, generated from the embedded bodies.\"\n")
	b.WriteString("tags: [agents, skills, reference]\npage_type: overview\n---\n\n")
	b.WriteString("# Agent skills\n\n")
	b.WriteString("These are the skills `magus agent install` writes, reproduced verbatim from the\n")
	b.WriteString("bodies embedded in the binary. Each ships in two hand-authored permutations, and\n")
	b.WriteString("install writes both: the short form is the always-loaded primary, and the full form\n")
	b.WriteString("is its `<name>-full` twin, loaded by name when a reader needs the rationale.\n")
	b.WriteString("See [Skills](../../guides/integrations/agents/skills.md) for the difference.\n\n")

	var tf, ts int
	b.WriteString("| skill | full | short | saved | what it is for |\n| --- | --- | --- | --- | --- |\n")
	for i, f := range full {
		s := simple[i]
		tf += len(f.Body)
		ts += len(s.Body)
		saved := 0
		if len(f.Body) > 0 {
			saved = (len(f.Body) - len(s.Body)) * 100 / len(f.Body)
		}
		fmt.Fprintf(&b, "| [%s](%s.md) | %d | %d | %d%% | %s |\n",
			f.Name, f.Name, len(f.Body), len(s.Body), saved, firstSentence(f.Description))
	}
	saved := 0
	if tf > 0 {
		saved = (tf - ts) * 100 / tf
	}
	fmt.Fprintf(&b, "| **all %d** | **%d** | **%d** | **%d%%** | |\n", len(full), tf, ts, saved)
	return b.String()
}

// firstSentence trims a skill's description to its opening claim, for a table cell
// and a frontmatter line where the full trigger text would not fit.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

// writeStampTable renders the provenance frontmatter an INSTALLED copy carries.
//
// The page already tells the reader that an installed skill is stamped and that
// copied text is not, then never showed the stamp - so the one concrete reason to
// prefer `magus agent install` over copy-paste was an assertion rather than
// something you could look at. These are the exact fields `magus graph verify`
// reads to decide "up to date" or "STALE".
//
// The values come from the catalog rather than a literal, so the table cannot
// drift from what install actually writes. skill-content fingerprints THIS skill
// alone; both of its permutations report the same one, which is why they go
// stale together and why an edit to a different skill leaves this page alone.
func writeStampTable(b *strings.Builder, cat *agent.Catalog, skill agent.AgentSkill) {
	body := string(cat.StampSkill(skill.Name, cat.RenderSkill(skill), agent.VariantFull))

	b.WriteString("## What an installed copy carries\n\n")
	b.WriteString("`magus agent install` writes this frontmatter above the body. " +
		"`magus graph verify` reads it to report whether your installed skills are current.\n\n")
	b.WriteString("| field | value |\n| --- | --- |\n")
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || key == "---" {
			continue
		}
		// Stop at the body: the closing --- ends the frontmatter, and the long
		// description is already the page's subtitle.
		if key == "name" || key == "description" {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		fmt.Fprintf(b, "| `%s` | `%s` |\n", key, value)
		if key == "skill-variant" {
			break
		}
	}
	b.WriteString("\nThe `skill-content` digest covers this skill alone, and both permutations below " +
		"report it: they go stale together, never one silently, and a change to another skill does not move it.\n\n")
}
