package guard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/contract"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// unloadKind is why the binary judging a call cannot read the tree's guard policy.
type unloadKind int

const (
	// unloadNone is a tree that loaded, or a failure only its author fixes (a typo).
	unloadNone unloadKind = iota
	// unloadStale is a failed load of a checkout of magus
	// whose contract differs from the judging binary's.
	unloadStale
	// unloadNoBinary is a load failure in a checkout that holds no ./magus, so whatever
	// judges the call is a magus installed somewhere else.
	unloadNoBinary
)

// The rules denyUnloaded refuses under, while no side of the workspace's guard policy loads.
const (
	// denyRuleStaleBinary refuses every call that changes state while the binary judging
	// it cannot load the tree.
	denyRuleStaleBinary denyRuleName = "stale-binary"
	// denyRulePolicyUnloaded refuses a gated verb while the policy that registered its
	// seam's rule does not load.
	denyRulePolicyUnloaded denyRuleName = "policy-unloaded"
)

// unloadCause says why the working tree's policy did not load. The
// contracts say it, never the failure's wording. With no ./magus the
// judge is a PATH binary nobody pinned to this tree.
func unloadCause(loadFailure error, root string) unloadKind {
	switch {
	case loadFailure == nil || root == "":
		return unloadNone
	case errors.Is(loadFailure, errNotCheckoutBinary), !hasMagusBinary(root):
		return unloadNoBinary
	case contractDiffers(root):
		return unloadStale
	}
	return unloadNone
}

// contractDiffers reports a checkout of magus at root whose contract
// differs from the judging binary's. One that cannot be read differs.
func contractDiffers(root string) bool {
	skew, err := contract.Check(root)
	return err != nil || skew.Differs()
}

// errNotCheckoutBinary is the load failure of a judge that is not ./magus.
var errNotCheckoutBinary = errors.New("the judging magus is neither this checkout's ./magus nor built from its contract")

// foreignJudge reports a binary that is not root's ./magus, in a checkout
// of magus, built from another contract than root carries. One whose
// contract matches judges as root's own would.
func foreignJudge(root, binary string) bool {
	if root == "" || binary == "" || ownBootstrapLine(root) == "" {
		return false
	}
	judge, errJudge := os.Stat(binary)
	own, errOwn := os.Stat(filepath.Join(root, "magus"))
	if errJudge == nil && errOwn == nil && os.SameFile(judge, own) {
		return false
	}
	return contractDiffers(root)
}

// ForeignJudgeDeps drops every rule from deps when binary is not root's own
// ./magus. PATH magus then answers only "bootstrap ./magus".
func ForeignJudgeDeps(deps Dependencies, root, binary string) Dependencies {
	if !foreignJudge(root, binary) {
		return deps
	}
	deps.LoadFailure = errNotCheckoutBinary
	deps.ShellRules, deps.Builtins = nil, nil
	deps.SpawnRule, deps.ApprovedSpawnRule = nil, nil
	deps.CommandRule, deps.ApprovedCommandRule = nil, nil
	deps.WriteRule, deps.ApprovedWriteRule = nil, nil
	deps.StopRule, deps.ApprovedStopRule = nil, nil
	return deps
}

// readOnlyLine reports a shell line that changes nothing: every program it runs reads,
// no redirect writes a file, and nothing in it is computed by a substitution the guard
// cannot see into. A line it cannot prove read-only is not one, so a stale binary errs
// toward refusing.
func readOnlyLine(command string, d Dialect) bool {
	return lineWithin(command, d, magusReadOnly)
}

// scoutLine is readOnlyLine for a read-only scout, which may also record its work.
func scoutLine(command string, d Dialect) bool {
	return lineWithin(command, d, func(args []string) bool { return magusReadOnly(args) || scoutRecord(args) })
}

// lineWithin reports a line that changes nothing beyond the magus invocations magusAllowed
// passes.
func lineWithin(command string, d Dialect, magusAllowed func(args []string) bool) bool {
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
				if !readOnlyInvocation(inv, dynamicArgs(n), magusAllowed) {
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
// magusAllowed judges a magus invocation.
func readOnlyInvocation(c hint.Invocation, dynamic bool, magusAllowed func(args []string) bool) bool {
	switch name := c.Name; {
	case readOnlyPrograms[name]:
		return true
	case dynamic:
		return false
	case name == "sed":
		return !rewritesInPlace(c)
	case name == "awk" || name == "gawk" || name == "mawk" || name == "nawk":
		return awkReadOnly(c.Args)
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
		return magusAllowed(c.Args)
	}
	return false
}

// awkReadOnly reports an awk run whose program is on the line and cannot write: no program
// file, no flag beyond -F and -v, and no redirect, pipe or system() in the program. A `>`
// comparison reads as a redirect here, which errs toward refusing.
func awkReadOnly(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-F" || a == "-v":
			i++
		case strings.HasPrefix(a, "-F") || strings.HasPrefix(a, "-v"):
		case a == "--":
			return i+1 < len(args) && awkProgramReadOnly(args[i+1])
		case strings.HasPrefix(a, "-"):
			return false
		default:
			return awkProgramReadOnly(a)
		}
	}
	return false
}

func awkProgramReadOnly(program string) bool {
	return !strings.ContainsAny(program, ">|") && !strings.Contains(program, "system")
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
var magusReadVerbs = []string{"version", "ls", "describe", "query", "explain", "refs", "doctor", "where"}

// magusReadOnly reports a magus invocation that reads. Asking for help always does, and so
// does `affected --explain`, which says why each project is affected and runs nothing.
func magusReadOnly(args []string) bool {
	if magusHelpRequest(args) || magusFlag(args, "dry-run") {
		return true
	}
	if renamesSymbol(args) || magusFlag(args, "fix") {
		return false
	}
	words := magusSubcommandWords(args)
	switch {
	case len(words) == 0:
		return false
	case words[0] == "affected":
		return magusFlag(args, "explain")
	case words[0] == "vcs":
		return len(words) > 1 && words[1] == "ls-files"
	}
	return slices.Contains(magusReadVerbs, words[0]) || isServerStatus(words)
}

// scoutRecord reports a magus invocation a read-only scout records its work with: `job
// exec` and `job exit` write only its own row, and `buzz --record` its probe's recording.
// A scout cannot finish without them, and a stale binary must not strand it.
func scoutRecord(args []string) bool {
	words := magusSubcommandWords(args)
	switch {
	case len(words) >= 2 && words[0] == "job":
		return words[1] == "exec" || words[1] == "exit"
	case len(words) >= 1 && words[0] == "buzz":
		return magusFlag(args, "record")
	}
	return false
}

// recoveryLine reports the line that gets a binary able to load this tree: the rebuild, the
// relink, or the bootstrap it falls back to, and for anyone the line that finishes or backs
// out of an interrupted cherry-pick, merge or rebase, or adds a worktree, since a tree left
// mid-pick links no binary at all. Each stands alone on its line. builds is whether the
// caller builds this checkout's binary: the root, or the checkout's integrator.
// Only it runs the rebuild target, a target at declares relinks.
// The relink, regenerate, placement and bootstrap pass for any caller.
func recoveryLine(command string, d Dialect, lease string, builds bool, at location) bool {
	command = withoutLeaseExport(command, d)
	call, ok := soleCall(command, d)
	if !ok {
		return false
	}
	words := literalWords(call.Args)
	if vcsRecoveryLine(words) && onlyEditorAssigns(call.Assigns) {
		return true
	}
	if lease != "" && len(call.Assigns) == 0 && fastForward(hint.Invocation{Name: path.Base(words[0]), Args: words[1:]}) {
		return true
	}
	// denyCheckoutBootstrap still judges who may build which checkout.
	places := onlyBaggageAssigns(call.Assigns) && len(words) >= 3 && path.Base(words[0]) == "magus" && words[1] == "buzz" &&
		(words[2] == placementScript || words[2] == checkoutHook(at.workspace))
	if places {
		return true
	}
	workerOnly := lease != "" && !builds
	switch prog := path.Base(words[0]); {
	case words[0] == "go":
		g, ok := readGoCall(hint.Invocation{Name: "go", Args: words[1:]})
		if !ok || !soleGoCommand(command, d) {
			return false
		}
		own := g.buildRoot(cmp.Or(at.dir, at.workspace)) == filepath.Clean(at.workspace)
		return (g.bootstrapsMagus() && bootstrapLine(command, d)) || (own && g.recoversMagus())
	case workerOnly:
		return false
	case prog == "magus" && len(call.Assigns) == 0:
		if r, ok := parseTargetRun(hint.Invocation{Name: "magus", Args: words[1:]}); ok {
			return runRelinks(at.declared().ix, r)
		}
	}
	return false
}

// relinksCheckout is checkout's ./magus when command is the relink of
// checkout, alone on its line, and "" otherwise. The holder of a checkout
// replaces its binary that way and no other.
func relinksCheckout(command string, d Dialect, cwd, checkout string) string {
	call, ok := soleCall(command, d)
	if checkout == "" || !ok || !soleGoCommand(command, d) {
		return ""
	}
	words := literalWords(call.Args)
	g, ok := readGoCall(hint.Invocation{Name: "go", Args: words[1:]})
	if !ok || g.args[0] != "build" || !g.recoversMagus() || !samePath(g.buildRoot(cmp.Or(cwd, checkout)), checkout) {
		return ""
	}
	return filepath.Join(checkout, "magus")
}

// withoutLeaseExport is command past a leading `export BAGGAGE=...`, the prefix
// edit-ungraded teaches. Any other command comes back unchanged.
func withoutLeaseExport(command string, d Dialect) string {
	f, err := parseFile(command, d)
	if err != nil || len(f.Stmts) == 0 {
		return command
	}
	first, rest := f.Stmts[0], f.Stmts[1:]
	if bin, ok := first.Cmd.(*syntax.BinaryCmd); ok && len(rest) == 0 && bin.Op == syntax.AndStmt {
		first, rest = bin.X, []*syntax.Stmt{bin.Y}
	}
	decl, ok := first.Cmd.(*syntax.DeclClause)
	if !ok || len(rest) != 1 || decl.Variant == nil || decl.Variant.Value != "export" || !onlyBaggageAssigns(decl.Args) {
		return command
	}
	var b strings.Builder
	if syntax.NewPrinter().Print(&b, rest[0]) != nil {
		return command
	}
	return b.String()
}

// onlyBaggageAssigns reports assignments setting nothing but the lease claim.
func onlyBaggageAssigns(assigns []*syntax.Assign) bool {
	return !slices.ContainsFunc(assigns, func(a *syntax.Assign) bool {
		return a.Name == nil || a.Name.Value != trail.EnvBaggage
	})
}

// vcsRecoveryLine reports a git line that continues, skips or aborts an interrupted
// cherry-pick, merge, rebase or revert, picks more commits, or adds a worktree.
func vcsRecoveryLine(words []string) bool {
	if len(words) < 3 || path.Base(words[0]) != "git" {
		return false
	}
	if len(words) == 3 && (words[2] == "--abort" || words[2] == "--continue" || words[2] == "--skip") {
		return slices.Contains([]string{"cherry-pick", "merge", "rebase", "revert"}, words[1])
	}
	switch words[1] {
	case "cherry-pick":
		// Finishing a series: the rest of its commits are what make the tree build again.
		return !slices.ContainsFunc(words[2:], func(w string) bool {
			return strings.HasPrefix(w, "-") && w != "-x" && w != "--no-edit"
		})
	case "worktree":
		return words[2] == "add"
	}
	return false
}

// outsideAnyWorkspace reports a write that lands under no magus workspace: an agent's
// memory, a scratchpad, a temp file. No guard policy judges such a path, so a policy that
// does not load holds nothing back there.
func outsideAnyWorkspace(path string, at location) bool {
	target, placed := placePath(path, cmp.Or(at.dir, at.workspace), userHome())
	if !placed {
		return false
	}
	if rel, err := filepath.Rel(at.workspace, target); at.workspace == "" || err == nil && filepath.IsLocal(rel) {
		return false
	}
	for dir := filepath.Dir(target); ; dir = filepath.Dir(dir) {
		for _, marker := range []string{config.Filename, config.DottedFilename, "magusfile.buzz"} {
			if _, err := os.Lstat(filepath.Join(dir, marker)); err == nil {
				return false
			}
		}
		if dir == filepath.Dir(dir) {
			return true
		}
	}
}

// checkpointReset reports a `git reset --keep|--merge <rev>` alone on
// its line, where rev is a clean checkpoint a job row records.
// A ./magus was placed at that revision, so it loads.
// --hard stays the whole-tree rule's to refuse.
func checkpointReset(ctx context.Context, command string, d Dialect, at location) bool {
	call, ok := soleCall(command, d)
	if !ok || len(call.Assigns) > 0 {
		return false
	}
	words := literalWords(call.Args)
	if len(words) != 4 || path.Base(words[0]) != "git" || words[1] != "reset" ||
		(words[2] != "--keep" && words[2] != "--merge") || strings.HasPrefix(words[3], "-") {
		return false
	}
	root := cmp.Or(at.dir, at.workspace)
	if root == "" {
		return false
	}
	resolved, err := gitNames(ctx, root, "rev-parse", "--verify", "--quiet", words[3]+"^{commit}")
	if err != nil || len(resolved) != 1 {
		return false
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(rows, func(r types.Job) bool {
		rev, digest := types.ParseCheckpointToken(r.Checkpoint)
		return digest == "" && len(rev) >= 7 && strings.HasPrefix(resolved[0], rev)
	})
}

// unpushedUndo reports a lone `git revert [--no-edit] <commit>` or
// `git reset --keep <commit>` that undoes only commits no remote holds.
// Those are the caller's own, so undoing one cannot lose shared work.
func unpushedUndo(ctx context.Context, command string, d Dialect, at location) bool {
	call, ok := soleCall(command, d)
	if !ok || len(call.Assigns) > 0 {
		return false
	}
	words := literalWords(call.Args)
	if len(words) < 3 || path.Base(words[0]) != "git" {
		return false
	}
	var rev string
	reset := false
	switch {
	case words[1] == "revert" && len(words) == 3:
		rev = words[2]
	case words[1] == "revert" && len(words) == 4 && words[2] == "--no-edit":
		rev = words[3]
	case words[1] == "reset" && len(words) == 4 && words[2] == "--keep":
		rev, reset = words[3], true
	default:
		return false
	}
	root := cmp.Or(at.dir, at.workspace)
	if root == "" || strings.HasPrefix(rev, "-") {
		return false
	}
	resolved, err := gitNames(ctx, root, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil || len(resolved) != 1 {
		return false
	}
	commit := resolved[0]
	if _, err := gitNames(ctx, root, "merge-base", "--is-ancestor", commit, "HEAD"); err != nil {
		return false
	}
	undone := commit + "^!"
	if reset {
		undone = commit + "..HEAD"
	}
	all, errAll := gitNames(ctx, root, "rev-list", undone)
	local, errLocal := gitNames(ctx, root, "rev-list", undone, "--not", "--remotes")
	return errAll == nil && errLocal == nil && len(all) > 0 && len(local) == len(all)
}

// onlyEditorAssigns reports an environment prefix that sets nothing but the editor git
// opens for a continued pick's message.
func onlyEditorAssigns(assigns []*syntax.Assign) bool {
	for _, a := range assigns {
		if a.Name == nil || (a.Name.Value != "GIT_EDITOR" && a.Name.Value != "GIT_SEQUENCE_EDITOR") {
			return false
		}
	}
	return true
}

// commandCall describes a shell line to denyUnloaded. A line is a change unless it proves
// read-only or is the fix; the verb it names is the gated one, if any. A read-only scout
// may also record its work, which reads its row from the job store.
func commandCall(ctx context.Context, in commandRuleInput, at location) judgedCall {
	call := judgedCall{
		seam: seamCommand, what: "this command", lease: in.lease, builds: in.lease == "",
		command: in.command, dialect: in.dialect, policyNote: runPolicyNote(in.command, at),
	}
	if !call.builds {
		role, _ := actingRole(ctx, at, in.lease, "")
		call.builds = role == types.AgentRoleIntegrator
	}
	if call.verb = gatedVerb(in.command, in.dialect); call.verb != "" {
		call.what, call.changes = call.verb, true
		return call
	}
	if in.mcpTool != "" {
		call.what = fmt.Sprintf("this call to the magus %s tool", in.mcpTool)
	}
	call.changes = !in.readOnly && !readOnlyLine(in.command, in.dialect) && !recoveryLine(in.command, in.dialect, in.lease, call.builds, at) &&
		!writesOnlyOutside(in.command, in.dialect, cmp.Or(at.dir, at.workspace), at.workspace, userHome()) &&
		!checkpointReset(ctx, in.command, in.dialect, at) && !unpushedUndo(ctx, in.command, in.dialect, at)
	if call.changes && in.lease != "" && scoutLine(in.command, in.dialect) {
		_, row := actingRole(ctx, at, in.lease, "")
		call.changes = !row.ReadOnly
	}
	return call
}

// staleBinaryDenial is the deny for a call that changes state while the binary judging it
// cannot load the tree's guard policy for reason: the cause and the likeliest fix inline,
// the call, the failures and the fallbacks behind the ref.
func staleBinaryDenial(call judgedCall, cause unloadKind, failures []string, own bool, root string) denial {
	hasBinary := hasMagusBinary(root)
	fix := binaryRemedy(own, root, call.lease, call.builds)
	if cause == unloadNoBinary && own && !hasBinary && (call.lease == "" || call.builds) {
		fix = binaryFix{say: bootstrapSay(ownBootstrapLine(root), readyCheckoutLine(root))}
	}
	problem := "the magus judging this cannot load this workspace's guard policy"
	switch {
	case own && !hasBinary:
		problem = "this checkout has no ./magus to load its guard policy"
	case own && cause == unloadNoBinary:
		problem = "the magus judging this is not this checkout's ./magus and was built from another contract, so it cannot load its guard policy"
	case own:
		problem = "./magus was built from another contract than this tree carries and cannot load its guard policy"
	}
	var why strings.Builder
	why.WriteString(call.what + " changes state, and the guard policy that would judge it is not running:")
	for _, f := range failures {
		why.WriteString("\n  " + f)
	}
	why.WriteString("\nEdits, spawns, pushes and commands that change state wait until a magus loads the tree. " +
		"Reads, `git status` and the fix still run, and so do a read-only scout's `magus job exec`, `magus job exit` and `magus buzz --record`. " +
		"So does getting the tree back to one a binary can be built from, each alone on its line: " +
		"`git cherry-pick`, `git merge`, `git rebase` or `git revert` with `--abort` or `--skip`, " +
		"`git cherry-pick <commit>...` to finish a series, `git worktree add`, and `git reset --keep <checkpoint>` to a revision a job row records as its checkpoint. " +
		"So do `git revert <commit>` and `git reset --keep <commit>` when every commit they undo is one no remote holds. " +
		"So do writes outside any workspace, such as memory or a scratchpad, by a file write or a shell redirect. " +
		"So does putting back a file the load error names as an import it cannot find: `git mv` onto it, `git restore` or `git checkout --` of it, or a write to it. " +
		"`git restore` and `git checkout --` also put back any other file the load error names.")
	if fix.why != "" {
		why.WriteString("\n" + fix.why)
	}
	if call.policyNote != "" {
		why.WriteString("\n" + call.policyNote)
	}
	return denial{Say: fmt.Sprintf("%s; %s.", problem, fix.say), Why: why.String()}
}

// binaryFix is the fix for a binary that cannot load the tree: say is the likeliest one,
// carrying at most one command, and why the rationale and the fallbacks.
// A bootstrap also names the checkout hook it needs.
// placeFor is the lease whose checkout the main session places ./magus in,
// "" when the fix is not that.
type binaryFix struct{ say, why, placeFor string }

// next is the fix's command, served as the deny's next under rule.
func (f binaryFix) next(rule denyRuleName) []types.NextStep {
	if f.placeFor == "" {
		return nil
	}
	return []types.NextStep{placementNext(rule, f.placeFor)}
}

// binaryRemedy is the fix for a binary that cannot load the tree at root, by who is asking.
// In a checkout of magus holding ./magus it is the relink, for every caller.
func binaryRemedy(own bool, root, lease string, builds bool) binaryFix {
	if builds {
		lease = ""
	}
	hasBinary := hasMagusBinary(root)
	bootstrap := config.SourceCommand(root, "", "", true)
	switch {
	case own && hasBinary:
		return binaryFix{
			say: fmt.Sprintf("relink ./magus, alone on its line: `%s`", ward.RelinkAt(root)),
			why: relinkWhy,
		}
	case lease != "" && own:
		return binaryFix{
			say:      "ask the main session to place a ./magus that loads this workspace",
			why:      onePerBase,
			placeFor: lease,
		}
	case lease != "":
		return binaryFix{
			say: "ask the main session to install a magus that loads this workspace",
			why: "The magus on this machine is not a worker's to replace.",
		}
	case own:
		return binaryFix{
			say: bootstrapSay(bootstrap, readyCheckoutLine(root)),
			why: "The bootstrap builds ./magus and nothing else. The checkout hook then installs every project's dependencies and every declared harness's tool rows, " +
				"and builds the graph, the symbol digest and the proofread binary the commit guard runs.",
		}
	}
	return binaryFix{
		say: fmt.Sprintf("install a magus that loads this workspace with `%s`", config.ReleaseCommand(config.RequiredVersionAt(root))),
		why: "If the error names a magusfile line instead, fix that line; `magus doctor` names the failure.",
	}
}

// bootstrapSay is the fix for a checkout with no ./magus. The build alone
// leaves the harness tool rows uninstalled, so the checkout hook follows it.
func bootstrapSay(bootstrap, ready string) string {
	return fmt.Sprintf("bootstrap ./magus with `%s`, then ready the checkout with `%s`, each alone on its line", bootstrap, ready)
}

// ownBootstrapLine is the line that builds a ./magus at the HEAD of root, a
// checkout of magus itself, "" for any other workspace.
func ownBootstrapLine(root string) string {
	if !ownSourceRoot(root) {
		return ""
	}
	return config.SourceCommand(root, "", "", true)
}

// readyCheckoutLine is the line that runs root's checkout hook after
// [ownBootstrapLine], "" when root declares none.
func readyCheckoutLine(root string) string {
	return config.ReadyCheckoutCommand(config.CheckoutHookAt(root))
}

// placementScript copies the main checkout's ./magus into a worker's checkout.
const placementScript = "hack/dev/bootstrap-worktree.buzz"

// onePerBase is why a worker never builds magus, worded as hack/policy/builds.buzz words it.
const onePerBase = "There is one binary per base commit: the checkout's integrator builds ./magus once, in the landing checkout, " +
	"and " + placementScript + " copies it into a worker checkout whenever no Go build input differs. " +
	"A build per worker is two Go compiles each, and a fan-out runs a dozen at once."

// relinkWhy is why the relink is the one remedy, for every caller.
const relinkWhy = "The relink loads no magusfile, so it runs while nothing loads the tree, and the binary it links embeds the tree's contract. " +
	"If the load error names a magusfile line instead, fix that line. " +
	"If it names generated code the tree's sources moved past, regenerate it with `go generate ./<package>`, alone on its line, then relink."

// workerBuildVerdict is the verdict for a worker building magus. Its command is
// placementNext, served apart from it.
const workerBuildVerdict = "a worker does not build magus; ask the main session to place ./magus"

// placementNext is the command that places the main checkout's ./magus in lease's checkout.
// The main checkout stays a placeholder: a worker's checkout does not say which one it is.
func placementNext(rule denyRuleName, lease string) types.NextStep {
	return hint.NextForDenyRemedy(string(rule), placementArgv(lease), "copies the main checkout's ./magus into this checkout.")
}

// placementArgv is placementNext's command as argv, for matching a served next against it.
func placementArgv(lease string) []string {
	return []string{"<main checkout>/magus", "buzz", placementScript, "--", "--job", lease, "--from", "<main checkout>"}
}

// placesBinary reports whether n is lease's placement of ./magus.
func placesBinary(n types.NextStep, lease string) bool {
	return lease != "" && slices.Equal(n.Argv, placementArgv(lease))
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

// replacesStaleMagus reports the line that gets at's workspace a current
// ./magus, alone on its line: a relinking target run, or the bootstrap.
// It holds while ./magus is absent or built from another contract.
func replacesStaleMagus(command string, at location) bool {
	root := at.workspace
	if root == "" || (hasMagusBinary(root) && !contractDiffers(root)) {
		return false
	}
	call, ok := soleCall(command, DialectBash)
	if !ok {
		return false
	}
	words := literalWords(call.Args)
	if len(words) == 0 {
		return false
	}
	if words[0] == "go" {
		g, ok := readGoCall(hint.Invocation{Name: "go", Args: words[1:]})
		return ok && g.bootstrapsMagus() && bootstrapLine(command, DialectBash)
	}
	if path.Base(words[0]) != "magus" || len(call.Assigns) > 0 {
		return false
	}
	r, ok := parseTargetRun(hint.Invocation{Name: "magus", Args: words[1:]})
	return ok && runRelinks(at.declared().ix, r)
}
