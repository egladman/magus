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

// catalog binds the embedded skill sources the same way cmd/magus does. The
// schema version is irrelevant here (it only feeds the install stamp, which these
// pages deliberately do not reproduce), so it is passed as zero rather than
// threaded from a package this tool has no other reason to import.
func run(outDir, srcDir string) error {
	cat := agent.NewCatalog(os.DirFS(srcDir), "", 0)
	full, err := cat.EmbeddedSkills(agent.VariantFull)
	if err != nil {
		return err
	}
	simple, err := cat.EmbeddedSkills(agent.VariantSimple)
	if err != nil {
		return err
	}
	if len(full) != len(simple) {
		return fmt.Errorf("permutation count mismatch: %d full, %d simple", len(full), len(simple))
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for i, f := range full {
		s := simple[i]
		if f.Name != s.Name {
			return fmt.Errorf("permutation order mismatch at %d: %q vs %q", i, f.Name, s.Name)
		}
		page := renderSkill(f, s)
		if err := emit.File(filepath.Join(outDir, f.Name+".md"), []byte(page)); err != nil {
			return err
		}
	}
	return emit.File(filepath.Join(outDir, "index.md"), []byte(renderIndex(full, simple)))
}

// renderSkill writes one skill's page: what it is for, then both permutations
// fenced verbatim.
//
// The full form comes first and unwrapped because it is the default install; the
// simple form follows in a <details> so the page does not read as two walls of
// near-identical text. Only the simple one is collapsed - the comparison a reader
// wants is "what did the short one drop", and that reads better by expanding the
// short one against the long one already on screen.
func renderSkill(full, simple agent.AgentSkill) string {
	var b strings.Builder
	// The two byte counts are stated as FACTS; the SSG turns them into a percentage
	// and a <progress> bar (engine/meta.buzz:insertSizeRatio). Presentation is the
	// renderer's job, and a generated page hands it data rather than markup.
	fmt.Fprintf(&b, "---\ntitle: %s\ndescription: %q\ntags: [agents, skills, %s]\n"+
		"skill_full_bytes: %d\nskill_simple_bytes: %d\n---\n\n",
		full.Name, firstSentence(full.Description), full.Name, len(full.Body), len(simple.Body))
	fmt.Fprintf(&b, "# %s\n\n", full.Name)
	fmt.Fprintf(&b, "%s\n\n", full.Description)
	fmt.Fprintf(&b, "Install it, rather than copying from this page:\n\n")
	fmt.Fprintf(&b, "```sh\nmagus agent install .claude/skills            # the full form below\nmagus agent install .claude/skills --simple   # the short form below\n```\n\n")
	fmt.Fprintf(&b, "An installed copy carries a provenance stamp, so `magus graph verify` can tell "+
		"you when a magus upgrade has made it stale. Text copied from this page carries none.\n\n")

	b.WriteString("## Full form\n\nThe default: the steps plus the rationale for each.\n\n")
	writeFenced(&b, full.Body)
	fmt.Fprintf(&b, "## Short form (`--simple`)\n\nThe same steps with the rationale withheld; the bar under "+
		"the heading above shows by how much. Both are hand-authored from one source body; see "+
		"[Agents](../../guides/integrations/agents.md) for when to prefer which.\n\n")
	b.WriteString("<details>\n<summary>Show the short form</summary>\n\n")
	writeFenced(&b, simple.Body)
	b.WriteString("\n</details>\n")
	return b.String()
}

// writeFenced wraps body in a code fence LONGER than any backtick run inside it.
//
// A fixed three-backtick fence is broken here, and it shipped that way: every skill
// body contains its own fenced examples (18 of them in magus-buzz), and Markdown
// fences do not nest - the body's first ```sh closed the wrapper, so most of the page
// rendered as live Markdown instead of verbatim text. CommonMark closes a fence only
// on a run at least as long as the opener, so measuring the longest run and adding
// one is the fix.
func writeFenced(w *strings.Builder, body string) {
	longest := 0
	for _, line := range strings.Split(body, "\n") {
		run := 0
		for run < len(line) && line[run] == '`' {
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
	b.WriteString("bodies embedded in the binary. Each ships in two hand-authored permutations: the\n")
	b.WriteString("default carries the rationale behind each step, and `--simple` withholds it.\n")
	b.WriteString("See [Agents](../../guides/integrations/agents.md) for how to choose.\n\n")

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
