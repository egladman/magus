package guard

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// An interpreter rewriting a file the tree already carries.
//
// `sed -i` has been refused for a while, and that denial sends the reader to their editor
// tool. The same act through an interpreter was refused only when the script both
// SUBSTITUTED and wrote, so the whole-file rewrite a merge conflict invites walked through,
// and a script arriving as a HEREDOC was never read at all.
//
// It keys on the WRITE rather than on the interpreter's flags, because the flags are the
// part with a new spelling every time: -c, -e, -pi, a bare argument, a heredoc. Two things
// stay allowed on purpose, since a rule that refused them is one people turn off: a script
// writing to a scratch or temp path, and a script creating a file that is not there yet.

// scriptWriteCalls name a write in the interpreters' own vocabularies.
var scriptWriteCalls = []string{
	".write(", ".writelines(", ".write_text(", ".write_bytes(",
	"writeFileSync", "writeFile(", "File.write", "IO.write", "fputs",
}

// openWriteRe matches an open() whose mode asks to write, so the read that merely names a
// file is left alone.
var openWriteRe = regexp.MustCompile(`open\s*\([^)]*['"][rbt+]*[wa][rbt+]*['"]`)

// scriptWrites reports a program that would write a file, in the spelling name uses.
func scriptWrites(name, script string, args []string) bool {
	switch name {
	case "perl", "ruby":
		if hasFlag(args, 'i', "in-place") {
			return true
		}
	case "awk":
		// awk redirects in its own language, so the write is inside the program text.
		if strings.Contains(script, ">") {
			return true
		}
	}
	if openWriteRe.MatchString(script) {
		return true
	}
	return slices.ContainsFunc(scriptWriteCalls, func(c string) bool { return strings.Contains(script, c) })
}

// denyInterpreterRewrite is the reason an inline interpreter would rewrite a file already
// in this workspace, or "" when it would not.
//
// It walks the AST itself rather than reading writeTargetCandidates, because it needs the
// SCRIPT as one text to ask whether it writes at all, and the heredoc body is a redirect
// rather than an argument.
func denyInterpreterRewrite(location location, command string) string {
	if location.workspace == "" {
		return ""
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return ""
	}
	hit := ""
	syntax.Walk(f, func(n syntax.Node) bool {
		if hit != "" {
			return false
		}
		st, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		call, ok := st.Cmd.(*syntax.CallExpr)
		if !ok {
			return true
		}
		for _, c := range peelWrappers(literalWords(call.Args)) {
			name := path.Base(c.Name)
			if !scriptedRewriteInterpreters[name] && name != "awk" {
				continue
			}
			script := strings.Join(c.Args, "\n") + "\n" + heredocText(st)
			if !scriptWrites(name, script, c.Args) {
				continue
			}
			if rel := rewrittenWorkspaceFile(location, script, c.Args); rel != "" {
				hit = rel
				return false
			}
		}
		return true
	})
	if hit == "" {
		return ""
	}
	return interpreterRewriteDenial(hit)
}

// rewrittenWorkspaceFile names the first file already in the tree that the script would
// write, or "".
//
// Existence is the discriminator: rewriting is what this refuses, and a path that is not
// there yet is a script producing output. A scratch or temp destination is skipped for the
// same reason, whether or not something sits at it.
func rewrittenWorkspaceFile(location location, script string, args []string) string {
	for _, candidate := range append(quotedLiterals(script), args...) {
		if candidate == "" || throwawayDirRe.MatchString(candidate) {
			continue
		}
		rel, inside := workspaceRelative(location.workspace, candidate)
		if !inside || rel == "." {
			continue
		}
		abs := candidate
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(location.workspace, candidate)
		}
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			return rel
		}
	}
	return ""
}

// interpreterRewriteDenial names the editor tool, in the shape the raw-tool denials use to
// name a magus target: a refusal that does not hand back the verb it wanted is one the
// reader routes around.
func interpreterRewriteDenial(rel string) string {
	return fmt.Sprintf("magus guard denied an inline interpreter rewriting %s, a file this tree already carries.\n\n"+
		"Use your editor tool instead: it reads the file, applies an exact replacement, and reports what changed. A script that rewrites a file it never read cannot tell a symbol from a word that looks like one, and what it mangles arrives with no record of what it matched. The heredoc spelling is the same act as `sed -i` and is refused for the same reason.\n"+
		"For a whole-tree mechanical edit, `"+hint.Refs.With("<symbol>", "--occurrences")+"` gives column-precise sites rather than a pattern that also matches the comment about it.\n"+
		"Writing to a scratch or temp path is untouched, and so is a script that CREATES a file.", rel)
}

// rankInterpreterRewrite ranks this reason against the verdict the other command rules
// reached. It fills a silence and outranks an advisory, but never replaces a deny: `sed -i`
// and the substitute-then-write rule both refuse the same act in their own words.
func rankInterpreterRewrite(v BashVerdict, reason string) BashVerdict {
	if reason == "" || v.Deny != "" {
		return v
	}
	return BashVerdict{Deny: reason, Rule: denyRule{Name: denyRuleInterpreterRewrite}}
}
