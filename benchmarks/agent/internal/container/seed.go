package container

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
)

// goModuleMagusfile is the one magusfile Seed writes beside every go.mod. It is not
// tailored to any repository, so both arms of every task see the same projection.
const goModuleMagusfile = `import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"](ctx); }
export fun lint(ctx: magus\Context, args: [str]) > void { go["go-vet"](ctx); }
export fun test(ctx: magus\Context, args: [str]) > void { go["go-test"](ctx, {"args": ["./..."]}); }
export fun ci(ctx: magus\Context, args: [str]) > void { ctx.needs(lint, build, test); }
`

const seedMagusYAML = "telemetry:\n  enabled: false\n"

// seedExcludes keep everything Seed and an arm's provisioning add out of git status,
// so the agent starts from a clean tree and Diff carries only the agent's edits.
const seedExcludes = `/magus.yaml
magusfile.buzz
.magus/
/.benchmark/
/.claude/
/CLAUDE.md
/MAGUS.md
`

// Seed makes the checkout at Testbed a magus workspace with one Go project per
// module, and returns the module directories, relative to Testbed. It is the one
// step that brings a foreign repository into magus; the Go workspace provider
// (decision 107) replaces its body, so callers depend only on this signature.
//
// Modules are the tracked go.mod files outside vendor and testdata trees, which the
// go command ignores too. A checkout with no root go.mod is refused: the workspace
// root must be a project.
func Seed(ctx context.Context, c *Container) ([]string, error) {
	var log bytes.Buffer
	if err := c.run(ctx, []string{"git", "config", "--global", "--add", "safe.directory", Testbed}, &log); err != nil {
		return nil, fmt.Errorf("%w: %s", err, log.Bytes())
	}
	var listed, stderr bytes.Buffer
	code, err := c.Exec(ctx, []string{"git", "ls-files", "-z", "--", ":(glob)**/go.mod"}, &listed, &stderr)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("container: git ls-files exited %d: %s", code, stderr.Bytes())
	}
	modules := goModules(listed.String())
	if !slices.Contains(modules, ".") {
		return nil, fmt.Errorf("container: %s has no go.mod at its root, so it has no root project to seed", Testbed)
	}
	if err := c.WriteFile(ctx, Testbed+"/magus.yaml", []byte(seedMagusYAML), 0o644); err != nil {
		return nil, err
	}
	for _, dir := range modules {
		if err := c.WriteFile(ctx, path.Join(Testbed, dir, "magusfile.buzz"), []byte(goModuleMagusfile), 0o644); err != nil {
			return nil, err
		}
	}
	appendExclude := []string{"sh", "-c", `cat "$1" >> .git/info/exclude`, "sh", "/tmp/magus-bench/exclude"}
	if err := c.WriteFile(ctx, "/tmp/magus-bench/exclude", []byte(seedExcludes), 0o644); err != nil {
		return nil, err
	}
	if err := c.run(ctx, appendExclude, &log); err != nil {
		return nil, fmt.Errorf("%w: %s", err, log.Bytes())
	}
	return modules, nil
}

// goModules reads git ls-files -z output into sorted module directories.
func goModules(listed string) []string {
	var dirs []string
	for _, file := range strings.Split(listed, "\x00") {
		if file == "" {
			continue
		}
		dir := path.Dir(file)
		if slices.ContainsFunc(strings.Split(dir, "/"), func(seg string) bool { return seg == "vendor" || seg == "testdata" }) {
			continue
		}
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	return slices.Compact(dirs)
}
