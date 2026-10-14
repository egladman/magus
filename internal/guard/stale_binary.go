package guard

import (
	"bytes"
	"context"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// unloadKind is why the binary judging a call cannot read the tree's guard policy.
type unloadKind int

const (
	// unloadNone is a tree that loaded, or a failure only its author fixes (a typo).
	unloadNone unloadKind = iota
	// unloadStale is a load failure shaped like a binary older than the tree: a name the
	// magusfile reaches for that this build has never heard of.
	unloadStale
	// unloadNoBinary is a load failure in a checkout that holds no ./magus, so whatever
	// judges the call is a magus installed somewhere else.
	unloadNoBinary
	// unloadSkewedSpell is a ./magus built on the other side of a spell's addition or
	// removal, which may be newer than the tree as well as older.
	unloadSkewedSpell
)

// unloadReason is what unloadCause found. Spell is the spell the tree carries and the
// binary does not ship, "" for none; a checkout with no ./magus can have one too.
type unloadReason struct {
	Cause unloadKind
	Spell string
}

// The rules denyUnloaded refuses under, while no side of the workspace's guard policy loads.
const (
	// denyRuleStaleBinary refuses every call that changes state while the binary judging
	// it cannot load the tree.
	denyRuleStaleBinary denyRuleName = "stale-binary"
	// denyRulePolicyUnloaded refuses a gated verb while the policy that registered its
	// seam's rule does not load.
	denyRulePolicyUnloaded denyRuleName = "policy-unloaded"
)

// guardRuleCall is what a magusfile writes to register a guard rule. A textual probe for it
// needs no Buzz load, which is the point: the load is what failed.
var guardRuleCall = []byte(`magus\guard.`)

// unloadCause says why the working tree's policy did not load in a way only a different
// binary fixes, unloadNone when the failure is something else (a typo in a magusfile) or
// the tree loaded. A checkout holding no ./magus counts whatever the error says: the judge
// is a PATH binary nobody pinned to this tree.
func unloadCause(loadFailure error, root string) unloadReason {
	if loadFailure == nil || root == "" {
		return unloadReason{}
	}
	if ward.IsStaleBinary(loadFailure) {
		return unloadReason{Cause: unloadStale}
	}
	spell := skewedSpell(loadFailure, root)
	switch {
	case !hasMagusBinary(root):
		return unloadReason{Cause: unloadNoBinary, Spell: spell}
	case spell != "":
		return unloadReason{Cause: unloadSkewedSpell, Spell: spell}
	}
	return unloadReason{}
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
var magusReadVerbs = []string{"version", "ls", "describe", "query", "status", "explain", "refs", "path", "doctor"}

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
	}
	return slices.Contains(magusReadVerbs, words[0])
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
// relink, or the bootstrap it falls back to. Each stands alone on its line. A leased
// worker's only one is the placement of its base's binary: a worker building a second is
// the thing the deny exists to prevent.
func recoveryLine(command string, d Dialect, lease string) bool {
	call, ok := soleCall(command, d)
	if !ok {
		return false
	}
	words := literalWords(call.Args)
	if lease != "" {
		return len(call.Assigns) == 0 && len(words) >= 3 && path.Base(words[0]) == "magus" && words[1] == "buzz" && words[2] == placementScript
	}
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
	}
	return false
}

// commandCall describes a shell line to denyUnloaded. A line is a change unless it proves
// read-only or is the fix; the verb it names is the gated one, if any. A read-only scout
// may also record its work, which reads its row from the job store.
func commandCall(ctx context.Context, in commandRuleInput, at location) judgedCall {
	call := judgedCall{seam: seamCommand, what: "this command", lease: in.lease}
	if call.verb = gatedVerb(in.command, in.dialect); call.verb != "" {
		call.what, call.changes = call.verb, true
		return call
	}
	if in.mcpTool != "" {
		call.what = "this call to the magus " + in.mcpTool + " tool"
	}
	call.changes = !in.readOnly && !readOnlyLine(in.command, in.dialect) && !recoveryLine(in.command, in.dialect, in.lease)
	if call.changes && in.lease != "" && scoutLine(in.command, in.dialect) {
		_, row := actingRole(ctx, at, in.lease)
		call.changes = !row.ReadOnly
	}
	return call
}

// staleBinaryDenial is the deny for a call that changes state while the binary judging it
// cannot load the tree's guard policy for reason: the cause and the likeliest fix inline,
// the call, the failures and the fallbacks behind the ref.
func staleBinaryDenial(call judgedCall, reason unloadReason, failures []string, own bool, root string) denial {
	hasBinary := hasMagusBinary(root)
	fix := binaryRemedy(own, hasBinary, call.lease)
	problem := "the magus judging this is older than this workspace and cannot load its guard policy"
	switch {
	case reason.Cause == unloadSkewedSpell:
		problem = "./magus was built from another commit than this tree: it ships no spell " + strconv.Quote(reason.Spell) +
			", which the tree carries, so it cannot load its guard policy"
	case reason.Cause == unloadNoBinary, own && !hasBinary:
		problem = "this checkout has no ./magus to load its guard policy"
	case own && hasBinary:
		problem = "./magus is older than this workspace and cannot load its guard policy"
	}
	var why strings.Builder
	why.WriteString(call.what + " changes state, and the guard policy that would judge it is not running:")
	for _, f := range failures {
		why.WriteString("\n  " + f)
	}
	why.WriteString("\nEdits, spawns, pushes and commands that change state wait until a magus loads the tree. " +
		"Reads, `git status` and the fix still run, and so do a read-only scout's `magus job exec`, `magus job exit` and `magus buzz --record`.")
	if fix.why != "" {
		why.WriteString("\n" + fix.why)
	}
	return denial{Say: problem + "; " + fix.say + ".", Why: why.String()}
}

// binaryFix is the fix for a binary that cannot load the tree: say is the likeliest one,
// carrying at most one command, and why the rationale and the fallbacks. placeFor is the
// lease whose checkout the main session places ./magus in, "" when the fix is not that.
type binaryFix struct{ say, why, placeFor string }

// next is the fix's command, served as the deny's next under rule.
func (f binaryFix) next(rule denyRuleName) []types.NextStep {
	if f.placeFor == "" {
		return nil
	}
	return []types.NextStep{placementNext(rule, f.placeFor)}
}

// binaryRemedy is the fix for a binary that cannot load the tree, by who is asking. A
// leased worker is never told to build: there is one binary per base, and the main
// session builds it and places a copy in each worker checkout.
func binaryRemedy(own, hasBinary bool, lease string) binaryFix {
	switch {
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
	case own && hasBinary:
		return binaryFix{
			say: "rebuild ./magus with " + ownRebuild,
			why: "If the rebuild cannot load the tree either, bootstrap over it, alone on its line: " +
				"`" + ward.BootstrapCommand + "`. If the error names a magusfile line instead, fix that line.",
		}
	case own:
		return binaryFix{say: "bootstrap ./magus with `" + ward.BootstrapCommand + "`"}
	}
	return binaryFix{
		say: "install a magus that loads this workspace with `magus self update`",
		why: "If the error names a magusfile line instead, fix that line; `magus doctor` names the failure.",
	}
}

// placementScript copies the main checkout's ./magus into a worker's checkout.
const placementScript = "hack/dev/bootstrap-worktree.buzz"

// onePerBase is why a worker never builds magus, worded as hack/policy/builds.buzz words it.
const onePerBase = "The main session places one binary per base commit: it builds ./magus once, in the root checkout, " +
	"and " + placementScript + " copies it into a worker checkout whenever no Go build input differs. " +
	"A build per worker is two Go compiles each, and a fan-out runs a dozen at once."

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
