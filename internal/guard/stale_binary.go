package guard

import (
	"bytes"
	"os"
	"path"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/ward"
)

// The reasons the binary judging a call cannot read the tree's guard policy, as
// unloadCause names them.
const (
	// causeStale is a load failure shaped like a binary older than the tree: a name the
	// magusfile reaches for that this build has never heard of.
	causeStale = "stale"
	// causeNoBinary is a load failure in a checkout that holds no ./magus, so whatever
	// judges the call is a magus installed somewhere else.
	causeNoBinary = "no-binary"
)

// guardRuleCall is what a magusfile writes to register a guard rule. A textual probe for it
// needs no Buzz load, which is the point: the load is what failed.
var guardRuleCall = []byte(`magus\guard.`)

// unloadCause says why the working tree's policy did not load in a way only a different
// binary fixes, or "" when the failure is something else (a typo in a magusfile) or the
// tree loaded. A checkout holding no ./magus counts whatever the error says: the judge is
// a PATH binary nobody pinned to this tree.
func unloadCause(loadFailure error, root string) string {
	switch {
	case loadFailure == nil || root == "":
		return ""
	case ward.IsStaleBinary(loadFailure):
		return causeStale
	case !hasMagusBinary(root):
		return causeNoBinary
	}
	return ""
}

// declaresGuardRule reports whether root's magusfile visibly registers a guard rule. It
// stands in for the policy marker that only exists after a rule set loaded once in this
// cache: a fresh worktree has none, and is exactly where a stale binary judges nothing.
func declaresGuardRule(root string) bool {
	if root == "" {
		return false
	}
	files, err := interp.MagusfileCandidates(root)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(files, func(path string) bool {
		body, err := os.ReadFile(path)
		return err == nil && bytes.Contains(body, guardRuleCall)
	})
}

// readOnlyLine reports a shell line that changes nothing: every program it runs reads,
// no redirect writes a file, and nothing in it is computed by a substitution the guard
// cannot see into. A line it cannot prove read-only is not one, so a stale binary errs
// toward refusing.
func readOnlyLine(command string, d Dialect) bool {
	f, err := parseFile(command, d)
	if err != nil {
		return false
	}
	ok := true
	syntax.Walk(f, func(n syntax.Node) bool {
		if !ok {
			return false
		}
		switch n := n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.FuncDecl, *syntax.CoprocClause:
			ok = false
		case *syntax.Stmt:
			if n.Background || n.Coprocess || !readOnlyRedirects(n.Redirs) {
				ok = false
			}
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				return true
			}
			words := literalWords(n.Args)
			for _, inv := range peelWrappers(words, d) {
				if !readOnlyInvocation(inv, dynamicArgs(n)) {
					ok = false
				}
			}
		}
		return ok
	})
	return ok
}

// dynamicArgs reports a call with an argument whose value a parameter or substitution
// supplies, so its flags are unknown.
func dynamicArgs(call *syntax.CallExpr) bool {
	return slices.ContainsFunc(call.Args[1:], func(w *syntax.Word) bool { return !staticParts(w.Parts) })
}

// staticParts reports word parts whose text is known without running anything.
func staticParts(parts []syntax.WordPart) bool {
	for _, p := range parts {
		switch p := p.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
		case *syntax.DblQuoted:
			if !staticParts(p.Parts) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// readOnlyRedirects reports redirects that read, or write only to /dev/null or another
// descriptor.
func readOnlyRedirects(rs []*syntax.Redirect) bool {
	for _, r := range rs {
		if writesToFile(r.Op) && !duplicatesDescriptor(r) && redirectWord(r) != "/dev/null" {
			return false
		}
	}
	return true
}

// readOnlyPrograms are the programs that read and cannot be told to write by a flag.
var readOnlyPrograms = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true, "pwd": true, "echo": true,
	"printf": true, "true": true, "false": true, "test": true, "[": true, ":": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "fd": true, "diff": true, "cmp": true,
	"stat": true, "file": true, "which": true, "type": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true, "date": true, "uname": true, "whoami": true, "id": true,
	"hostname": true, "tree": true, "du": true, "df": true, "uniq": true, "cut": true, "tr": true,
	"nl": true, "column": true, "jq": true, "yq": true, "od": true, "xxd": true, "md5sum": true,
	"shasum": true, "sha256sum": true, "cksum": true, "printenv": true, "seq": true, "sleep": true,
	"cd": true, "pushd": true, "popd": true, "read": true,
}

// readOnlyInvocation reports one program run that changes nothing. dynamic is whether an
// argument is computed, which rules out the programs whose flags decide whether they write.
func readOnlyInvocation(c hint.Invocation, dynamic bool) bool {
	switch name := c.Name; {
	case readOnlyPrograms[name]:
		return true
	case dynamic:
		return false
	case name == "sed":
		return !rewritesInPlace(c)
	case name == "sort":
		return !slices.ContainsFunc(c.Args, func(a string) bool { return a == "-o" || strings.HasPrefix(a, "--output") })
	case name == "find":
		return !slices.ContainsFunc(c.Args, func(a string) bool {
			return slices.Contains([]string{"-delete", "-fprint", "-fprintf", "-fls", "-fprint0", "-ok", "-okdir"}, a)
		})
	case name == "git":
		return gitReadOnly(c)
	case name == "hg" || name == "sl" || name == "jj":
		sub, _ := vcsSubcommand(c)
		return slices.Contains([]string{"status", "st", "diff", "log", "show", "summary", "root", "identify", "id", "files"}, sub)
	case name == "gh":
		return ghReadOnly(c.Args)
	case name == "magus":
		return magusReadOnly(c.Args)
	}
	return false
}

// gitReadSubcommands read a repository and take no flag that writes one, bar --output,
// which gitReadOnly checks.
var gitReadSubcommands = []string{
	"status", "diff", "log", "show", "rev-parse", "rev-list", "ls-files", "ls-tree", "ls-remote",
	"blame", "describe", "shortlog", "cat-file", "merge-base", "name-rev", "show-ref", "for-each-ref",
	"diff-tree", "diff-index", "diff-files", "grep", "count-objects", "check-ignore", "whatchanged",
	"version", "help",
}

// gitReadOnly reports a git command that reads. An alias defined on the line can run any
// verb, so it never does.
func gitReadOnly(c hint.Invocation) bool {
	if parseGit(c.Args).alias != "" {
		return false
	}
	sub, rest := vcsSubcommand(c)
	has := func(flags ...string) bool {
		return slices.ContainsFunc(rest, func(a string) bool {
			name, _, _ := strings.Cut(a, "=")
			return slices.Contains(flags, name)
		})
	}
	switch {
	case slices.Contains(gitReadSubcommands, sub):
		return !has("--output")
	case sub == "branch":
		return !has("-d", "-D", "-m", "-M", "-c", "-C", "--delete", "--move", "--copy", "-f", "--force",
			"-u", "--set-upstream-to", "--unset-upstream", "--edit-description") &&
			(len(rest) == 0 || has("-a", "-r", "-l", "--list", "-v", "-vv", "--show-current", "--contains", "--merged", "--no-merged"))
	case sub == "tag":
		return !has("-d", "-a", "-s", "-m", "-f", "--delete", "--force") && (len(rest) == 0 || has("-l", "--list"))
	case sub == "stash":
		return len(rest) > 0 && (rest[0] == "list" || rest[0] == "show")
	case sub == "worktree":
		return len(rest) > 0 && rest[0] == "list"
	case sub == "remote":
		return len(rest) == 0 || rest[0] == "-v" || rest[0] == "show" || rest[0] == "get-url"
	case sub == "config":
		return has("--get", "--get-all", "--get-regexp", "--list", "-l") && !has("--unset", "--unset-all", "--add", "--replace-all")
	case sub == "reflog":
		return len(rest) == 0 || rest[0] == "show"
	}
	return false
}

// ghReadOnly reports a GitHub CLI command that views or lists.
func ghReadOnly(args []string) bool {
	var words []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	if len(words) < 2 {
		return false
	}
	switch words[0] {
	case "pr", "issue", "run", "release", "repo", "workflow", "label":
		return slices.Contains([]string{"view", "list", "diff", "checks", "status"}, words[1])
	case "auth":
		return words[1] == "status"
	}
	return false
}

// magusReadVerbs are the magus verbs that answer a question and write nothing shared.
var magusReadVerbs = []string{"version", "ls", "describe", "query", "status", "explain", "refs", "path", "doctor"}

// magusReadOnly reports a magus invocation that reads. Asking for help always does.
func magusReadOnly(args []string) bool {
	if magusHelpRequest(args) || magusFlag(args, "dry-run") {
		return true
	}
	if renamesSymbol(args) || magusFlag(args, "fix") {
		return false
	}
	words := magusSubcommandWords(args)
	return len(words) > 0 && slices.Contains(magusReadVerbs, words[0])
}

// recoveryLine reports the line that gets a binary able to load this tree: the rebuild, or
// the move-aside and bootstrap it falls back to. Each stands alone on its line. A leased
// worker has none: there is one binary per base, the orchestrator places it, and a worker
// building a second is the thing the deny exists to prevent.
func recoveryLine(command string, d Dialect, lease string) bool {
	if lease != "" {
		return false
	}
	call, ok := soleCall(command, d)
	if !ok {
		return false
	}
	words := literalWords(call.Args)
	switch prog := path.Base(words[0]); {
	case words[0] == "go":
		g, ok := readGoCall(hint.Invocation{Name: "go", Args: words[1:]})
		if !ok {
			return false
		}
		return soleGoCommand(command, d) && (g.recoversMagus() || (g.bootstrapsMagus() && bootstrapLine(command, d)))
	case prog == "magus" && len(call.Assigns) == 0:
		if r, ok := parseTargetRun(hint.Invocation{Name: "magus", Args: words[1:]}); ok {
			return r.verb == "run" && targetName(r.target) == "go-build" && len(r.projects) <= 1 && (len(r.projects) == 0 || r.projects[0] == ".")
		}
	case prog == "mv" && len(call.Assigns) == 0:
		return len(words) == 3 && words[1] == "magus" && words[2] == "magus.old"
	}
	return false
}

// commandCall describes a shell line to denyUnloaded. A line is a change unless it proves
// read-only or is the fix; the verb it names is the gated one, if any.
func commandCall(in commandRuleInput) unloadedCall {
	call := unloadedCall{seam: seamCommand, what: "this command", lease: in.lease}
	if call.verb = gatedVerb(in.command, in.dialect); call.verb != "" {
		call.what, call.changes = call.verb, true
		return call
	}
	if in.mcpTool != "" {
		call.what = "this call to the magus " + in.mcpTool + " tool"
	}
	call.changes = !in.readOnly && !readOnlyLine(in.command, in.dialect) && !recoveryLine(in.command, in.dialect, in.lease)
	return call
}

// staleBinaryReason is the deny for a call that changes state while the binary judging it
// cannot load the tree's guard policy.
func staleBinaryReason(call unloadedCall, cause string, failures []string, own bool, root string) string {
	var b strings.Builder
	b.WriteString("magus workspace: " + call.what + " is denied: the magus judging it cannot load this workspace, so its guard policy is not running.")
	switch cause {
	case causeStale:
		b.WriteString(" That magus is older than the tree.")
	case causeNoBinary:
		b.WriteString(" This checkout has no ./magus, and the magus that answered cannot load its magusfile.")
	}
	for _, f := range failures {
		b.WriteString("\n  " + f)
	}
	b.WriteString("\nEdits, spawns, pushes and commands that change state wait until a binary loads the tree; reads, `git status` and the fix still run.\n")
	b.WriteString(binaryRemedy(own, hasMagusBinary(root), call.lease))
	return b.String()
}

// binaryRemedy is the one fix for a binary that cannot load the tree, by who is asking. A
// leased worker is never told to build: there is one binary per base, and the orchestrator
// builds it in the root and places a copy in each worker checkout.
func binaryRemedy(own, hasBinary bool, lease string) string {
	switch {
	case lease != "" && own:
		return "The binary is the orchestrator's to place, one per base, so a worker never builds one: ask it to run " +
			"`magus buzz hack/dev/bootstrap-worktree.buzz -- --job " + lease + "`, then retry."
	case lease != "":
		return "The magus on this machine is not a worker's to replace: ask the orchestrator, or the person who runs it, to install one that loads this workspace."
	case own && hasBinary:
		return "Rebuild it: `./magus run go-build .`. If that cannot load the tree either, move it aside and bootstrap, " +
			"one command at a time: `mv magus magus.old`, `" + bootstrapCommand + "`. If the error names a magusfile line instead, fix that line."
	case own:
		return "This checkout has no ./magus. Bootstrap one: `" + bootstrapCommand + "`."
	}
	return "Install a magus that loads this workspace (`magus self update`), or fix the line the error names; `magus doctor` names the failure."
}

// soleCall is the line's call when it is one command and nothing else: no pipe, chain,
// redirect or subshell. Its environment prefix is left to the caller.
func soleCall(command string, d Dialect) (*syntax.CallExpr, bool) {
	f, err := parseFile(command, d)
	if err != nil || len(f.Stmts) != 1 {
		return nil, false
	}
	st := f.Stmts[0]
	call, ok := st.Cmd.(*syntax.CallExpr)
	if !ok || st.Negated || st.Background || st.Coprocess || len(st.Redirs) > 0 || len(call.Args) == 0 {
		return nil, false
	}
	return call, true
}
