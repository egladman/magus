// Command magus-examples keeps the worked examples in the docs honest: it builds the
// current magus binary, runs curated retrieval-verb invocations against a small fixture
// workspace, captures their ACTUAL stdout, and injects each into docs/knowledge.md
// between HTML markers (<!-- example:<slug> --> ... <!-- /example -->). So the example
// output is never hand-typed - it is exactly what `magus explain`/`path` print, from a
// controlled fixture with stable IDs (same approach the txtar tests use, just captured
// instead of asserted). It mirrors magus-spelldocs (derive committed Markdown from a
// source of truth) and rides the same generate + vcs.isDirty drift gate: change the
// renderer, forget to regenerate, and CI fails.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// fixtureFiles is the curated workspace the examples run against: it declares the
// built-in go spell (so the output carries realistic, recognizable IDs - tool:go,
// op:go:go-test) but only a couple of targets, so the output stays small and stable
// across releases (it depends on the go spell's fixed op set, not on whatever this
// repo happens to contain).
var fixtureFiles = map[string]string{
	"magus.yaml": "concurrency: 4\n",
	// A source file so the review example has a changed file to describe. Its CONTENT never
	// shows in the docs - the prompt names paths and annotations, not lines - so it only has to
	// be something the go spell would claim.
	"main.go": "package main\n\nfunc main() {}\n",
	"magusfile.buzz": `import "magus";
import "magus/spell/go";
magus.project({ "spells": [go] });

// Format the Go sources.
export fun format(ctx: magus\Context, args: [str]) > void { go["go-fmt"](); }

// Run the Go test suite; formats first.
export fun test(ctx: magus\Context, args: [str]) > void {
    ctx.needs(format);
    go["go-test"]();
}
`,
}

// example is one worked example: the page it belongs on, its marker slug, and the magus argv to
// run. The displayed command line is derived from the argv, so the two never disagree.
type example struct {
	docs string // the Markdown file, relative to -dir, carrying this example's markers
	slug string
	argv []string
}

func (e example) command() string { return "magus " + strings.Join(e.argv, " ") }

const (
	knowledgeDoc = "concepts/knowledge.md"
	reviewDoc    = "concepts/review.md"
)

var examples = []example{
	{docs: knowledgeDoc, slug: "explain-tool-go", argv: []string{"explain", "tool:go"}},
	{docs: knowledgeDoc, slug: "explain-target-test", argv: []string{"explain", "target:.:test"}},
	{docs: knowledgeDoc, slug: "path-test-to-tool", argv: []string{"path", "target:.:test", "tool:go"}},
	// The review prompt is captured rather than transcribed for the reason every example here is:
	// it is prose magus assembles, so a hand-typed copy in the docs would describe a version
	// nobody gets. It runs against the same fixture, which the setup below makes a repository
	// with one uncommitted edit so there is a changeset to describe.
	{docs: reviewDoc, slug: "diff-prompt", argv: []string{"diff", "--prompt"}},
}

func main() {
	docsDir := flag.String("dir", "docs", "the directory holding the Markdown files whose <!-- example:<slug> --> blocks to fill")
	flag.Parse()

	rendered, err := renderExamples()
	if err != nil {
		fatalf("%v", err)
	}
	// Grouped by page, because inject treats a rendered example with no marker as a hard error -
	// which is what keeps the docs and the example set in lockstep, and would otherwise fire for
	// every example that belongs on a different page.
	byDoc := map[string]map[string]string{}
	for _, ex := range examples {
		if byDoc[ex.docs] == nil {
			byDoc[ex.docs] = map[string]string{}
		}
		byDoc[ex.docs][ex.slug] = rendered[ex.slug]
	}
	for doc, snippets := range byDoc {
		if err := inject(filepath.Join(*docsDir, doc), snippets); err != nil {
			fatalf("%v", err)
		}
	}
}

// renderExamples builds the current magus binary, writes the fixture, and captures
// each example's stdout by running the binary against the fixture.
func renderExamples() (map[string]string, error) {
	dir, err := os.MkdirTemp("", "magus-examples-")
	if err != nil {
		return nil, fmt.Errorf("temp fixture: %w", err)
	}
	defer os.RemoveAll(dir)
	for name, body := range fixtureFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return nil, fmt.Errorf("write fixture %s: %w", name, err)
		}
	}

	if err := initFixtureRepo(dir); err != nil {
		return nil, err
	}

	// Build HEAD's magus so the captured output reflects the current renderer, not a
	// release on PATH - the whole point of the drift gate. The module path (not a
	// relative ./cmd/magus) so this works whatever directory the generator runs from.
	// OUTSIDE the fixture. Built into it, the binary is an untracked file in the fixture's
	// repository, so `magus diff` reported it as part of the changeset and the harness leaked
	// into published documentation as a changed file named magus-bin.
	binDir, err := os.MkdirTemp("", "magus-examples-bin-")
	if err != nil {
		return nil, fmt.Errorf("temp bin dir: %w", err)
	}
	defer os.RemoveAll(binDir)
	bin := filepath.Join(binDir, "magus-bin")
	build := exec.Command("go", "build", "-o", bin, "github.com/egladman/magus/cmd/magus")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return nil, fmt.Errorf("build magus: %w", err)
	}

	out := make(map[string]string, len(examples))
	for _, ex := range examples {
		text, err := capture(bin, dir, ex.argv)
		if err != nil {
			return nil, fmt.Errorf("example %s (%s): %w", ex.slug, ex.command(), err)
		}
		out[ex.slug] = "```console\n$ " + ex.command() + "\n" + text + "```\n"
	}
	return out, nil
}

// initFixtureRepo makes the fixture a repository with exactly one uncommitted edit, so the
// review-prompt example has a changeset to describe.
//
// Every knob that would otherwise vary by machine is pinned, because the captured output is
// COMMITTED and compared by the drift gate: the branch name (git's default is a local setting),
// and the identity (a developer's global config would put their name in published docs, and a
// runner with no config would fail the commit outright). The same reasoning as the XDG_STATE_HOME
// redirect below - anything read from the environment makes the same command produce two pages.
func initFixtureRepo(dir string) error {
	for _, argv := range [][]string{
		{"init", "-b", "base"},
		{"add", "-A"},
		{"-c", "user.name=magus", "-c", "user.email=magus@example.invalid",
			"commit", "-m", "the state this change is compared against"},
	} {
		cmd := exec.Command("git", argv...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("fixture repo (git %s): %w\n%s", strings.Join(argv, " "), err, out)
		}
	}
	// The edit itself, after the commit: this is what `magus diff` reports.
	edited := "package main\n\nfunc main() { println(\"hello\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(edited), 0o644); err != nil {
		return fmt.Errorf("fixture edit: %w", err)
	}
	return nil
}

// capture runs the magus binary with argv in the fixture dir and returns its stdout.
// Diagnostics ([warn]/[note]) go to stderr, so stdout is the clean command output;
// the daemon is disabled so a shared background daemon cannot influence the result.
//
// XDG_STATE_HOME is redirected into the fixture for a reason MAGUS_DAEMON_ENABLED
// does not cover: `explain` ends with a Graph Explorer deep-link, and that link
// carries the daemon auth token, which auth.Load reads from a FILE in the state dir
// whether or not a daemon is running. Captured on a developer's machine the examples
// therefore embedded a real token in committed, published documentation; captured on
// a runner they did not, so the same command produced two different pages and the
// drift gate failed on CI alone. An empty state dir gives a machine-independent link
// and nothing to leak.
func capture(bin, dir string, argv []string) (string, error) {
	cmd := exec.Command(bin, argv...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"MAGUS_DAEMON_ENABLED=false",
		"XDG_STATE_HOME="+filepath.Join(dir, "state"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w\n%s", err, stderr.String())
	}
	text := stdout.String()
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text, nil
}

// inject replaces the content between each example's markers with its snippet. A
// slug with no marker pair, or a rendered example with no marker, is a hard error:
// the docs and the example set must stay in lockstep.
func inject(path string, rendered map[string]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	content := string(raw)
	for slug, snippet := range rendered {
		start := "<!-- example:" + slug + " -->"
		end := "<!-- /example -->"
		si := strings.Index(content, start)
		if si < 0 {
			return fmt.Errorf("%s: no marker %q (add it where the example belongs)", path, start)
		}
		after := si + len(start)
		rel := strings.Index(content[after:], end)
		if rel < 0 {
			return fmt.Errorf("%s: marker %q has no closing %q", path, start, end)
		}
		ei := after + rel
		// A BLANK line on BOTH sides of the fence, not just a newline: dprint's markdown formatter
		// wants one between an HTML comment and an adjacent fenced block, and without them the
		// generator and the formatter each undo the other on every run - the oscillation that
		// makes a page a hybrid nobody can gate.
		content = content[:after] + "\n\n" + snippet + "\n" + content[ei:]
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "magus-examples: "+format+"\n", args...)
	os.Exit(1)
}
