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
// that pushes nothing.
//
// It copies the location reads of locatePush and pushedRev rather than sharing them: those
// answer for the built-in push rule, which reads one push and treats a refspec it cannot
// read as the current revision.
func commandPush(ctx context.Context, deps Dependencies, in commandRuleInput, at location) *types.CommandPush {
	pushes := publishingCalls(in, cmp.Or(at.dir, at.workspace))
	if len(pushes) == 0 {
		return nil
	}
	first := pushes[0]
	push := &types.CommandPush{Tool: filepath.Base(first.call.inv.Name), OpensPullRequest: isPRCreate(first.call.inv)}
	site, located := placedPush(first.call)
	switch {
	case len(pushes) > 1:
		push.Unknown = manyPushes(pushes)
	case !located || site.dir == "":
		push.Unknown = fmt.Sprintf("where `%s` runs is known only by running the line, so the revision it publishes cannot be read first", publishSpelling(first.call.inv))
	case first.moved != "":
		push.Dir = site.dir
		push.Unknown = fmt.Sprintf("this line runs `%s` before it pushes, so the revision it publishes exists only once the line runs", first.moved)
	default:
		push.Dir = site.dir
		resolvePush(ctx, deps, push, first.call)
	}
	return push
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
	return fmt.Sprintf("this line publishes %d times (%s), so no one revision is what it publishes", len(pushes), strings.Join(names, ", "))
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
// `gh pr create`, which opens review on the pushed head.
func publishes(c hint.Invocation) bool {
	return isPush(c) && !deletesOnly(c)
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
		return !slices.ContainsFunc(ops[1:], func(spec string) bool { return !strings.HasPrefix(spec, ":") })
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

// hasUnreadWord reports a word of the call the shell expands at run time, which the
// rendered arguments show as empty. A call reached through a script has only rendered
// words, and pushSource reads those.
func hasUnreadWord(c locatedCall) bool {
	return slices.ContainsFunc(c.pushArgs(), func(w *syntax.Word) bool {
		_, ok := literalArg(w.Parts)
		return !ok
	})
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
	src, _, _ := strings.Cut(strings.TrimPrefix(ops[1], "+"), ":")
	switch {
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
	heads := flagValues(args, "H", "head")
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
