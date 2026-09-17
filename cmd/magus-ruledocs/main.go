// Command magus-ruledocs generates the guard-rule reference: one page per rule this
// binary enforces, plus an index.
//
//	go run ./cmd/magus-ruledocs -out ./docs/reference/rules
//
// The pages are generated from the SAME catalog `magus describe rules` reads and the
// same slug a verdict reports, so a rule cannot be documented under a name nothing
// fires, and a rule cannot fire under a name nothing documents. Hand-writing 46 pages
// beside a table that already holds the facts is the copy that silently rots, which is
// the failure this repo builds generators to avoid.
//
// The pages are deliberately thin. A rule's one-line "catches" is the whole answer for
// most of them; the ones carrying a "why" carry the rationale that used to sit inside
// the verdict, where it was read under interruption by someone who wanted the command.
// Here it is read by someone who came looking.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/internal/guard"
)

func main() {
	out := flag.String("out", "", "Directory to write the rule reference into (required)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "magus-ruledocs: -out is required")
		os.Exit(2)
	}
	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "magus-ruledocs: %v\n", err)
		os.Exit(1)
	}
}

func run(outDir string) error {
	rules := guard.Rules()
	if len(rules) == 0 {
		return fmt.Errorf("the catalog is empty; the guard rules moved and this generator stopped seeing them")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, r := range rules {
		if err := emit.File(filepath.Join(outDir, r.Name+".md"), []byte(renderRule(r))); err != nil {
			return err
		}
	}
	if err := emit.File(filepath.Join(outDir, "index.md"), []byte(renderIndex(rules))); err != nil {
		return err
	}
	return prune(outDir, rules)
}

// renderRule is one rule's page.
func renderRule(r guard.RuleDoc) string {
	var b strings.Builder
	title := r.Name + ": " + r.Catches
	fmt.Fprintf(&b, "---\ntitle: %q\n", title)
	// Quoted, like the title: a description carrying a colon is a YAML mapping to the
	// frontmatter parser, and every one of these does.
	fmt.Fprintf(&b, "description: %q\n", describe(r))
	fmt.Fprintf(&b, "tags: [guard, rules, %s, %s]\n---\n\n", r.Name, r.Decision)

	fmt.Fprintf(&b, "# %s\n\n", r.Name)
	fmt.Fprintf(&b, "%s\n\n", describe(r))

	b.WriteString("## What it catches\n\n")
	fmt.Fprintf(&b, "%s.\n\n", upperFirst(r.Catches))

	if r.Why != "" {
		b.WriteString("## Why\n\n")
		fmt.Fprintf(&b, "%s\n\n", r.Why)
	}

	b.WriteString("## Seeing it\n\n")
	b.WriteString("A verdict names its rule in brackets, which is how you got here:\n\n")
	b.WriteString("```text\n")
	fmt.Fprintf(&b, "%s [%s]: ...\n", r.Decision, r.Name)
	b.WriteString("```\n\n")
	fmt.Fprintf(&b, "`magus describe rule %s` prints the same entry at a terminal, and\n", r.Name)
	b.WriteString("`magus describe rules` lists every rule this workspace enforces.\n\n")

	b.WriteString("## See also\n\n")
	b.WriteString("- [All rules](index.md) - what this workspace enforces, deny first\n")
	b.WriteString("- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired\n")
	return b.String()
}

// describe is the one-sentence summary the page and its frontmatter share, so they
// cannot disagree.
func describe(r guard.RuleDoc) string {
	if r.Decision == "deny" {
		return "A deny rule: it refuses " + r.Catches + ", and names what to run instead."
	}
	return "An advisory: it explains, and blocks nothing, on " + r.Catches + "."
}

// renderIndex is the whole catalog as one table, denies first, matching what
// `magus describe rules` prints.
func renderIndex(rules []guard.RuleDoc) string {
	var b strings.Builder
	b.WriteString("---\ntitle: \"Guard rules\"\n")
	b.WriteString("description: \"Every rule the guard enforces: what each one catches, whether it refuses or explains, and where the reasoning lives.\"\n")
	b.WriteString("tags: [guard, rules, reference]\n---\n\n")
	b.WriteString("# Guard rules\n\n")
	b.WriteString("What this workspace enforces. A **deny** refuses the call and names the\n")
	b.WriteString("replacement; an **advise** attaches context and blocks nothing.\n\n")
	b.WriteString("Every verdict names its rule in brackets (`deny [stage-all]: ...`), and that\n")
	b.WriteString("name is the entry below. `magus describe rules` prints the same list.\n\n")

	denies, advisories := split(rules)
	writeTable(&b, "Refuses", denies)
	writeTable(&b, "Explains", advisories)
	// The last table's own trailing blank line would leave the file ending in two
	// newlines, which the formatter trims: one more way regeneration would not be a
	// fixed point.
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// writeTable emits one table, columns already padded to their widest cell.
//
// Pre-aligned because the docs formatter aligns markdown tables itself: a generator
// emitting ragged pipes writes a file the formatter immediately rewrites, so `generate`
// and `format` disagree forever and the drift gate is red on every run. Matching the
// formatter's own output is what makes regeneration a fixed point.
func writeTable(b *strings.Builder, heading string, rules []guard.RuleDoc) {
	fmt.Fprintf(b, "## %s\n\n", heading)

	const nameHead, catchHead = "Rule", "Catches"
	names := make([]string, len(rules))
	catches := make([]string, len(rules))
	nameWidth, catchWidth := len(nameHead), len(catchHead)
	for i, r := range rules {
		names[i] = fmt.Sprintf("[%s](%s.md)", r.Name, r.Name)
		catches[i] = escapePipes(r.Catches)
		nameWidth = max(nameWidth, len(names[i]))
		catchWidth = max(catchWidth, len(catches[i]))
	}

	fmt.Fprintf(b, "| %s | %s |\n", pad(nameHead, nameWidth), pad(catchHead, catchWidth))
	fmt.Fprintf(b, "| %s | %s |\n", strings.Repeat("-", nameWidth), strings.Repeat("-", catchWidth))
	for i := range rules {
		fmt.Fprintf(b, "| %s | %s |\n", pad(names[i], nameWidth), pad(catches[i], catchWidth))
	}
	b.WriteString("\n")
}

// pad right-pads a cell to width. Every cell here is ASCII (a slug, or prose this
// repository holds to plain ASCII), so a byte count is the display width.
func pad(s string, width int) string {
	return s + strings.Repeat(" ", width-len(s))
}

func split(rules []guard.RuleDoc) (denies, advisories []guard.RuleDoc) {
	for _, r := range rules {
		if r.Decision == "deny" {
			denies = append(denies, r)
			continue
		}
		advisories = append(advisories, r)
	}
	return denies, advisories
}

// escapePipes keeps a Catches containing a shell pipe from splitting its table cell.
func escapePipes(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	// Only when the first character is a letter: several rows open with a backtick-quoted
	// command (`git add -A`), and capitalising inside that would misquote the command.
	r := []rune(s)
	if r[0] < 'a' || r[0] > 'z' {
		return s
	}
	r[0] = r[0] - 'a' + 'A'
	return string(r)
}

// prune deletes pages for rules this binary no longer enforces, the same reason
// magus-skilldocs prunes: a generator that owns its writes but not its deletions cannot
// rename anything, and a renamed rule would leave its old page published, linked from
// nothing, describing a rule that can never fire.
func prune(outDir string, shipped []guard.RuleDoc) error {
	keep := make(map[string]bool, len(shipped)+1)
	keep["index.md"] = true
	for _, r := range shipped {
		keep[r.Name+".md"] = true
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || keep[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(outDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
