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

// example is one worked example: its marker slug and the magus argv to run. The
// displayed command line is derived from the argv, so the two never disagree.
type example struct {
	slug string
	argv []string
}

func (e example) command() string { return "magus " + strings.Join(e.argv, " ") }

var examples = []example{
	{slug: "explain-tool-go", argv: []string{"explain", "tool:go"}},
	{slug: "explain-target-test", argv: []string{"explain", "target:.:test"}},
	{slug: "path-test-to-tool", argv: []string{"path", "target:.:test", "tool:go"}},
}

func main() {
	docsPath := flag.String("docs", "docs/knowledge.md", "the Markdown file whose <!-- example:<slug> --> blocks to fill")
	flag.Parse()

	rendered, err := renderExamples()
	if err != nil {
		fatalf("%v", err)
	}
	if err := inject(*docsPath, rendered); err != nil {
		fatalf("%v", err)
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

	// Build HEAD's magus so the captured output reflects the current renderer, not a
	// release on PATH - the whole point of the drift gate. The module path (not a
	// relative ./cmd/magus) so this works whatever directory the generator runs from.
	bin := filepath.Join(dir, "magus-bin")
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

// capture runs the magus binary with argv in the fixture dir and returns its stdout.
// Diagnostics ([warn]/[note]) go to stderr, so stdout is the clean command output;
// the daemon is disabled so a shared background daemon cannot influence the result.
func capture(bin, dir string, argv []string) (string, error) {
	cmd := exec.Command(bin, argv...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MAGUS_DAEMON_ENABLED=false")
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
		content = content[:after] + "\n" + snippet + content[ei:]
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
