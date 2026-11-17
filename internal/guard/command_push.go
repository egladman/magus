package guard

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// commandPush reads what the line publishes, for the workspace command rule's req.Push.
// Nil only when no call on the line publishes. Every case magus cannot settle before the
// line runs carries Unknown, so a rule that reads Revision never mistakes it for a line
// that pushes nothing. Several publishing calls that name one commit in one checkout, such
// as a push and then `gh pr create` for the branch it pushed, are one publication.
//
// It copies the location reads of locatePush and pushedRev rather than sharing them: those
// answer for the built-in push rule, which reads one push and treats a refspec it cannot
// read as the current revision.
func commandPush(ctx context.Context, deps Dependencies, in commandRuleInput, at location) *types.CommandPush {
	if inner, ok := substitutedPublish(in); ok {
		return &types.CommandPush{
			Tool: filepath.Base(inner.Name), OpensPullRequest: isPRCreate(inner),
			Unknown: fmt.Sprintf("`%s` runs inside a command substitution, whose words magus does not place before the line runs", publishSpelling(inner)),
		}
	}
	pushes := publishingCalls(in, cmp.Or(at.dir, at.workspace))
	if len(pushes) == 0 {
		return nil
	}
	read := make([]*types.CommandPush, len(pushes))
	for i, p := range pushes {
		read[i] = readPush(ctx, deps, p)
	}
	first := read[0]
	if len(read) == 1 {
		return first
	}
	for _, p := range read[1:] {
		if first.Unknown != "" || p.Unknown != "" || p.Dir != first.Dir || p.Revision != first.Revision {
			return &types.CommandPush{Tool: first.Tool, Dir: first.Dir, OpensPullRequest: first.OpensPullRequest, Unknown: manyPushes(pushes)}
		}
		first.OpensPullRequest = first.OpensPullRequest || p.OpensPullRequest
	}
	return first
}

// readPush is what one publishing call publishes.
func readPush(ctx context.Context, deps Dependencies, found foundPush) *types.CommandPush {
	push := &types.CommandPush{Tool: filepath.Base(found.call.inv.Name), OpensPullRequest: isPRCreate(found.call.inv)}
	site, located := placedPush(found.call)
	switch {
	case !located || site.dir == "":
		push.Unknown = fmt.Sprintf("where `%s` runs is known only by running the line, so the revision it publishes cannot be read first", publishSpelling(found.call.inv))
	case found.moved != "":
		push.Dir = site.dir
		push.Unknown = fmt.Sprintf("this line runs `%s` before it pushes, so the revision it publishes exists only once the line runs", found.moved)
	default:
		push.Dir = site.dir
		resolvePush(ctx, deps, push, found.call)
	}
	return push
}

// substitutedPublish is the first publishing call inside a command or process substitution
// on the line, which the line walk does not reach: `out=$(git push)` runs a push whose
// words are a substitution's.
func substitutedPublish(in commandRuleInput) (hint.Invocation, bool) {
	f, err := parseFile(in.command, in.dialect)
	if err != nil {
		return hint.Invocation{}, false
	}
	var found hint.Invocation
	ok := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if ok {
			return false
		}
		var stmts []*syntax.Stmt
		switch n := n.(type) {
		case *syntax.CmdSubst:
			stmts = n.Stmts
		case *syntax.ProcSubst:
			stmts = n.Stmts
		default:
			return true
		}
		for _, st := range stmts {
			syntax.Walk(st, func(m syntax.Node) bool {
				call, isCall := m.(*syntax.CallExpr)
				if ok || !isCall {
					return !ok
				}
				for _, inv := range peelWrappers(literalWords(call.Args), in.dialect) {
					if publishes(inv) {
						found, ok = inv, true
						return false
					}
				}
				return true
			})
		}
		return false
	})
	return found, ok
}

// foundPush is a publishing call on the line, with the first command before it that
// changes the revision it publishes.
type foundPush struct {
	call  locatedCall
	moved string
}

// publishingCalls are the calls on the line that publish, in the order the shell runs them.
func publishingCalls(in commandRuleInput, start string) []foundPush {
	calls, ok := locateCalls(in.command, in.dialect, start, func(c hint.Invocation) bool {
		return publishes(c) || movesRevision(c)
	}, false)
	if !ok {
		return nil
	}
	var pushes []foundPush
	moved := ""
	for _, c := range calls {
		switch {
		case publishes(c.inv):
			pushes = append(pushes, foundPush{call: c, moved: moved})
		case moved == "":
			moved = moverSpelling(c.inv)
		}
	}
	return pushes
}

func manyPushes(pushes []foundPush) string {
	names := make([]string, len(pushes))
	for i, p := range pushes {
		names[i] = fmt.Sprintf("`%s`", publishSpelling(p.call.inv))
	}
	return fmt.Sprintf("this line publishes %d times (%s) and they do not name one commit in one checkout, so no one revision is what it publishes", len(pushes), strings.Join(names, ", "))
}

// resolvePush fills push's Source and Revision from the call, or Unknown with the reason
// neither can be read.
func resolvePush(ctx context.Context, deps Dependencies, push *types.CommandPush, call locatedCall) {
	if hasUnreadWord(call) {
		push.Unknown = fmt.Sprintf("`%s` has a word that is not literal, so what it publishes is known only when the line runs", publishSpelling(call.inv))
		return
	}
	source, unknown := pushSource(call.inv)
	push.Source = source
	if unknown != "" {
		push.Unknown = unknown
		return
	}
	res, err := vcs.Resolve(ctx, push.Dir, "", deps.VCS)
	if err != nil || res.VCS == nil {
		push.Unknown = fmt.Sprintf("no version control answers in `%s`: %s", push.Dir, cmp.Or(errorMessage(err), "version control is disabled"))
		return
	}
	commit, err := res.VCS.FindCommit(ctx, push.Dir, source)
	switch {
	case err != nil && source == "":
		push.Unknown = fmt.Sprintf("the current revision of `%s` is unread: %v", push.Dir, err)
	case err != nil:
		push.Unknown = fmt.Sprintf("`%s` names no commit in `%s`: %v", source, push.Dir, err)
	default:
		push.Revision = commit.ID
	}
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// publishes reports a call that sends a commit out: a push that deletes nothing but also
// `gh pr create`, which opens review on the pushed head. A dry run or a request for help
// sends nothing.
func publishes(c hint.Invocation) bool {
	return isPush(c) && !deletesOnly(c) && !sendsNothing(c)
}

// sendsNothing reports a push or `gh pr create` that only prints: --dry-run (git's -n too)
// or --help, given as an option and not as an option's value: `git push -o --dry-run` and
// `gh pr create --body -h` publish.
func sendsNothing(c hint.Invocation) bool {
	return slices.ContainsFunc(optionWords(c), func(a string) bool {
		return a == "--dry-run" || a == "--help" || a == "-h" || c.Name == "git" && a == "-n"
	})
}

// valuedOptions are, per tool, the push options whose value may be the next word.
var valuedOptions = map[string][]string{
	"git": {"-o", "--push-option", "--repo", "--receive-pack", "--exec"},
	"gh":  ghValued,
	"jj":  {"-r", "--revisions", "-c", "--change", "-b", "--bookmark", "--remote", "-R", "--repository", "--config", "--at-op"},
	"hg":  {"-r", "--rev", "-b", "--branch", "-B", "--bookmark", "-e", "--ssh", "--remotecmd"},
	"sl":  {"-r", "--rev", "-b", "--branch", "-B", "--bookmark", "-e", "--ssh", "--remotecmd", "--to"},
}

// optionWords are the words of a push call that are options, with every option's value
// left out.
func optionWords(c hint.Invocation) []string {
	valued := valuedOptions[filepath.Base(c.Name)]
	words := pushWords(c)
	var out []string
	for i := 0; i < len(words); i++ {
		a := words[i]
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
		if _, _, joined := strings.Cut(a, "="); !joined && slices.Contains(valued, a) {
			i++
		}
	}
	return out
}

// deletesOnly reports a push that removes remote names and publishes no commit.
func deletesOnly(c hint.Invocation) bool {
	if isPRCreate(c) {
		return false
	}
	args := pushWords(c)
	switch c.Name {
	case "git":
		if slices.Contains(args, "-d") || slices.Contains(args, "--delete") {
			return true
		}
		ops := operands(args, "o")
		if len(ops) < 2 {
			return false
		}
		// A bare `:` is not a deletion: it pushes every branch that matches one on the remote.
		return !slices.ContainsFunc(ops[1:], func(spec string) bool { return !strings.HasPrefix(spec, ":") || spec == ":" })
	case "sl":
		return slices.Contains(args, "--delete")
	case "jj":
		return slices.Contains(args, "--deleted") && len(jjSelectors(args)) == 0 &&
			!slices.ContainsFunc(args, func(a string) bool { return a == "--all" || a == "--tracked" })
	}
	return false
}

// pushWords are the words a push call carries after its push subcommand, with the tool's
// global options taken out: for jj, past `git push`.
func pushWords(c hint.Invocation) []string {
	if isPRCreate(c) {
		return c.Args
	}
	sub, rest := vcsSubcommand(c)
	if c.Name == "jj" && sub == "git" {
		_, rest = vcsSubcommand(hint.Invocation{Name: c.Name, Args: rest})
	}
	return rest
}

// publishSpelling is how a line spells the call: the push `vcsMutation` names, or gh's own.
func publishSpelling(c hint.Invocation) string {
	if isPRCreate(c) {
		return "gh pr create"
	}
	return vcsMutation(c)
}

// moverSpelling is the command that changes the revision a later push publishes.
func moverSpelling(c hint.Invocation) string {
	sub, _ := vcsSubcommand(c)
	return strings.TrimSpace(fmt.Sprintf("%s %s", c.Name, sub))
}

// placedPush is where the call runs, read off the line without running it. False when only
// running the line reveals it: a directory held in a variable, a call inside a conditional
// or a function, or a relocation a wrapper's script hides.
func placedPush(c locatedCall) (pushSite, bool) {
	args := c.pushArgs()
	var site pushSite
	switch {
	case c.unfollowed:
		return pushSite{}, false
	case args == nil:
		if !c.at.known || vcsRelocates(c.inv.Name, c.inv.Args) {
			return pushSite{}, false
		}
		site = pushSite{dir: c.at.dir, relocated: c.at.moved}
	default:
		var ok bool
		if site, ok = parsePush(c.inv.Name, args, c.at); !ok {
			return pushSite{}, false
		}
	}
	if site.relocated && !filepath.IsAbs(site.dir) {
		abs, err := filepath.Abs(site.dir)
		if err != nil {
			return pushSite{}, false
		}
		site.dir = abs
	}
	return site, true
}

// hasUnreadWord reports a word of the call the shell expands at run time. A call reached
// through a script (`sh -c`, eval) has only its rendered words, where such a word renders
// empty. The text gh pr create sends as a title or body never decides what it publishes,
// so a substitution there, such as a heredoc body, is not one.
func hasUnreadWord(c locatedCall) bool {
	words := c.pushArgs()
	read := func(i int) bool {
		if words == nil {
			return c.inv.Args[i] != ""
		}
		_, ok := literalArg(words[i].Parts)
		return ok
	}
	var text map[int]bool
	if isPRCreate(c.inv) {
		_, text = ghOptions(c.inv.Args)
	}
	for i := range c.inv.Args {
		if words != nil && i >= len(words) {
			break
		}
		if !text[i] && !read(i) {
			return true
		}
	}
	return false
}

// ghValued are the options of gh pr create that take a value, as the short and long forms
// gh accepts. Every other option of it is a switch.
var ghValued = []string{
	"-t", "--title", "-b", "--body", "-F", "--body-file", "-B", "--base", "-H", "--head", "-R", "--repo",
	"-a", "--assignee", "-l", "--label", "-m", "--milestone", "-p", "--project", "-r", "--reviewer",
	"-T", "--template", "--recover",
}

// ghOptions reads gh pr create's args: the values of --head, and the index of every word
// that is an option's value or carries one, other than --head and --repo, whose values
// decide what is published. A value never reads as an option: a body of `- Handles the
// push` is the body.
func ghOptions(args []string) (heads []string, text map[int]bool) {
	text = map[int]bool{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return heads, text
		case strings.HasPrefix(a, "--"):
			name, value, joined := strings.Cut(a, "=")
			if !slices.Contains(ghValued, name) {
				continue
			}
			at := i
			if !joined {
				i++
				if i >= len(args) {
					return heads, text
				}
				at, value = i, args[i]
			}
			switch name {
			case "--head":
				heads = append(heads, value)
			case "--repo":
			default:
				text[at] = true
			}
		case len(a) > 1 && a[0] == '-':
			i = ghShort(args, i, &heads, text)
		}
	}
	return heads, text
}

// ghShort reads the cluster of short options at args[i] (`-dH topic`), and is the index of
// the last word it took.
func ghShort(args []string, i int, heads *[]string, text map[int]bool) int {
	a := args[i]
	for j := 1; j < len(a); j++ {
		flag := "-" + a[j:j+1]
		if !slices.Contains(ghValued, flag) {
			continue
		}
		at, value := i, a[j+1:]
		if value == "" {
			if i+1 >= len(args) {
				return i
			}
			i++
			at, value = i, args[i]
		}
		switch flag {
		case "-H":
			*heads = append(*heads, value)
		case "-R":
		default:
			text[at] = true
		}
		return i
	}
	return i
}

// pushOptionsWithValues are the git push options whose value is the next word, which
// operands would read as a remote or a refspec.
var pushOptionsWithValues = []string{"--repo", "--receive-pack", "--exec", "--push-option"}

// pushSource is the one revision the call names as what it publishes, as typed, "" for the
// checkout's current revision. unknown says why no one revision can be named: the call
// publishes several, or names a selector magus cannot resolve to one commit. It reads the
// rendered arguments, which every call has, wrapped in a script or not.
func pushSource(c hint.Invocation) (source, unknown string) {
	if isPRCreate(c) {
		return ghSource(c.Args)
	}
	args := pushWords(c)
	switch c.Name {
	case "git":
		return gitSource(args)
	case "hg":
		return hgSource("hg", args, false)
	case "sl":
		return hgSource("sl", args, true)
	case "jj":
		return jjSource(args)
	}
	return "", fmt.Sprintf("`%s` is not a push whose revision magus reads", c.Name)
}

func gitSource(args []string) (source, unknown string) {
	for _, flag := range []string{"--all", "--branches", "--mirror", "--tags"} {
		if slices.Contains(args, flag) {
			return "", fmt.Sprintf("`git push %s` publishes more than one revision", flag)
		}
	}
	for _, opt := range pushOptionsWithValues {
		if slices.Contains(args, opt) {
			return "", fmt.Sprintf("`git push %s` takes its value as a separate word, which magus does not read as a refspec", opt)
		}
	}
	ops := operands(args, "o")
	switch {
	case len(ops) < 2:
		return "", ""
	case len(ops) > 2:
		return "", "`git push` names more than one refspec, so it publishes more than one revision"
	case ops[1] == "":
		return "", "a refspec of this push is not literal, so what it publishes is known only when the line runs"
	}
	spec := strings.TrimPrefix(ops[1], "+")
	src, _, _ := strings.Cut(spec, ":")
	switch {
	case spec == ":":
		return "", fmt.Sprintf("the refspec `%s` pushes every branch that matches one on the remote, so it publishes more than one revision", ops[1])
	case src == "HEAD" || src == "@":
		return "", ""
	case strings.ContainsAny(src, "*?["):
		return "", fmt.Sprintf("the refspec `%s` is a pattern that publishes more than one revision", ops[1])
	}
	return src, ""
}

// hgSource is hg's or sl's push: -r names the commit. Without it sl publishes the current
// commit and hg every outgoing head.
func hgSource(tool string, args []string, current bool) (source, unknown string) {
	revs := flagValues(args, "r", "rev")
	switch {
	case len(revs) > 1:
		return "", fmt.Sprintf("`%s push` names more than one -r, so it publishes more than one revision", tool)
	case len(revs) == 0 && current:
		return "", ""
	case len(revs) == 0:
		return "", fmt.Sprintf("`%s push` with no -r publishes every outgoing head", tool)
	}
	return oneCommit(revs[0], "-r")
}

// jjSource is jj git push: one -r, -c or -b names what it publishes.
func jjSource(args []string) (source, unknown string) {
	for _, flag := range []string{"--all", "--tracked", "--deleted"} {
		if slices.Contains(args, flag) {
			return "", fmt.Sprintf("`jj git push %s` publishes bookmarks magus does not read before the line runs", flag)
		}
	}
	selectors := jjSelectors(args)
	switch len(selectors) {
	case 0:
		return "", "`jj git push` with no -r, -c or -b publishes bookmarks magus does not read before the line runs"
	case 1:
		return oneCommit(selectors[0], "the selector")
	}
	return "", "`jj git push` names more than one of -r, -c and -b, so it publishes more than one revision"
}

func jjSelectors(args []string) []string {
	return slices.Concat(flagValues(args, "r", "revisions"), flagValues(args, "c", "change"), flagValues(args, "b", "bookmark"))
}

// ghSource is gh pr create's --head, a branch of this repository.
func ghSource(args []string) (source, unknown string) {
	heads, _ := ghOptions(args)
	switch {
	case len(heads) == 0:
		return "", ""
	case len(heads) > 1:
		return "", "`gh pr create` names more than one --head"
	case strings.Contains(heads[0], ":"):
		return "", "`--head` names a branch of another repository"
	}
	return oneCommit(heads[0], "--head")
}

// oneCommit is value as the one commit it names, or why it may name several or none.
func oneCommit(value, what string) (source, unknown string) {
	if value == "" || strings.ContainsAny(value, "|&~,()+ ") || strings.Contains(value, "..") || strings.Contains(value, "::") {
		return "", fmt.Sprintf("%s `%s` is empty or a range, not one commit", what, value)
	}
	return value, ""
}
