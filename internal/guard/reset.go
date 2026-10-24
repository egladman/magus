package guard

import (
	"bytes"
	"context"
	"path"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// hardResetAsSwitch is line with each `git reset --hard <rev>` spelled `git switch --detach
// <rev>` when every one runs in a clean checkout no live job was taken in, where both move
// HEAD and discard nothing, so they are judged alike. Otherwise, or when anything about a
// reset cannot be read, line comes back unchanged and whole-tree judges it.
func hardResetAsSwitch(ctx context.Context, deps Dependencies, at location, callDir, line string, d Dialect) string {
	if !strings.Contains(line, "reset") || callDir == "" {
		return line
	}
	calls, ok := locateCalls(line, d, callDir, isHardReset, false)
	if !ok || len(calls) == 0 {
		return line
	}
	rows, err := jobRowsAt(ctx, at)
	if err != nil {
		return line
	}
	for _, c := range calls {
		if c.unfollowed || c.wrapped != nil || c.args == nil || !c.at.known || !cleanUnboundCheckout(ctx, deps, rows, c.at.dir) {
			return line
		}
	}
	f, err := parseFile(line, d)
	if err != nil {
		return line
	}
	rewritten := 0
	syntax.Walk(f, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		words := literalWords(call.Args)
		if len(words) == 0 || !isHardReset(hint.Invocation{Name: path.Base(words[0]), Args: words[1:]}) {
			return true
		}
		g := parseGit(words[1:])
		call.Args[1+g.at] = litWord("switch")
		for i := 2 + g.at; i < len(words); i++ {
			if words[i] == "--hard" {
				call.Args[i] = litWord("--detach")
			}
		}
		rewritten++
		return true
	})
	var out bytes.Buffer
	if rewritten != len(calls) || syntax.NewPrinter().Print(&out, f) != nil {
		return line
	}
	return strings.TrimSpace(out.String())
}

// isHardReset reports `git reset --hard <rev>` run where the shell stands: no -C,
// --git-dir or --work-tree, one revision and no pathspec.
func isHardReset(c hint.Invocation) bool {
	if c.Name != "git" {
		return false
	}
	g := parseGit(c.Args)
	if g.sub != "reset" || !slices.Contains(g.rest, "--hard") || len(g.dirs) > 0 || g.opaque || g.configured || slices.Contains(g.rest, "--") {
		return false
	}
	return positionals(g.rest) == 1
}

// cleanUnboundCheckout reports dir inside a git checkout with nothing modified, staged or
// untracked, that no live job in rows was taken in.
func cleanUnboundCheckout(ctx context.Context, deps Dependencies, rows []types.Job, dir string) bool {
	root, _, ok := gitCheckout(dir)
	if !ok {
		return false
	}
	if slices.ContainsFunc(rows, func(row types.Job) bool { return row.State.Live() && samePath(row.CheckoutRoot, root) }) {
		return false
	}
	opts := deps.VCS
	opts.Name = "git"
	res, err := vcs.Resolve(ctx, root, "", opts)
	if err != nil || res.VCS == nil {
		return false
	}
	dirty, err := res.VCS.DirtyFiles(ctx, root, nil)
	return err == nil && len(dirty) == 0
}

func litWord(s string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: s}}}
}
