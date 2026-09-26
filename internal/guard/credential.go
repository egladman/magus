package guard

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/hint"
)

// The credential rules. Every command reaching the guard came from an agent, and an agent
// holds its own token, so a session that mints, prints or rotates one, or reads the files the
// secrets live in, is taking a grant nobody handed it. Both rules are a seatbelt against a
// harness that opted in, not a boundary: a process running as the user can read those files
// whatever the guard says, which docs/concepts/tokens.md states plainly.

// credentialInvocation is one magus command that mints, prints, rotates or revokes a
// credential: its words, and the flag that makes it one when the command alone does not.
type credentialInvocation struct {
	words []string
	flag  string
}

// credentialInvocations is every such command. TestCredentialVerbsExistInTheRegistry holds
// each to a command internal/cli declares, so a rename fails there rather than dropping out.
var credentialInvocations = []credentialInvocation{
	{words: []string{"config", "token", "print"}},
	{words: []string{"config", "token", "generate"}},
	{words: []string{"config", "token", "revoke"}},
	{words: []string{"config", "mcp", "connector", "create"}},
	{words: []string{"config", "mcp", "connector", "revoke"}},
	{words: []string{"config", "console", "token", "create"}},
	{words: []string{"config", "console", "token", "revoke"}},
	// Its link carries a console sign-in code.
	{words: []string{"graph", "export"}, flag: "follow"},
}

// credentialVerbRe is the unparsable-line fallback: any listed spelling after a word ending in
// magus, with its flag when it needs one.
var credentialVerbRe = sync.OnceValue(func() *regexp.Regexp {
	alts := make([]string, 0, len(credentialInvocations))
	for _, inv := range credentialInvocations {
		alt := strings.Join(inv.words, `\s+`) + `\b`
		if inv.flag != "" {
			alt += `[^|;&]*\s--?` + regexp.QuoteMeta(inv.flag) + `\b`
		}
		alts = append(alts, alt)
	}
	return regexp.MustCompile(`\bmagus\b[^|;&]*?\b(?:` + strings.Join(alts, "|") + `)`)
})

// credentialVerbFires reports a line that runs a credential verb through magus, however the
// binary is spelled: `magus`, `./magus`, a path, or `go run ./cmd/magus`.
func credentialVerbFires(cmds []hint.Invocation, parsed bool, command string) bool {
	if !parsed {
		return credentialVerbRe().MatchString(command)
	}
	for _, c := range cmds {
		args, ok := magusArgv(c)
		if !ok {
			continue
		}
		words := magusSubcommandWords(args)
		for _, inv := range credentialInvocations {
			if len(words) >= len(inv.words) && slices.Equal(words[:len(inv.words)], inv.words) &&
				(inv.flag == "" || magusFlag(args, inv.flag)) {
				return true
			}
		}
	}
	return false
}

// magusArgv is the argv magus itself would see from c: c's own arguments when c is magus,
// and the words after the package when c is `go run` of magus's command package.
func magusArgv(c hint.Invocation) ([]string, bool) {
	if c.Name == "magus" {
		return c.Args, true
	}
	if c.Name != "go" || len(c.Args) == 0 || c.Args[0] != "run" {
		return nil, false
	}
	// A go flag's value is not told apart from the package here (`-tags dev`), so every word
	// is tried as the package; a later word matching is the safe direction to be wrong in.
	for i, a := range c.Args[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		pkg, _, _ := strings.Cut(a, "@")
		if path.Base(pkg) == "magus" && path.Base(path.Dir(pkg)) == "cmd" {
			return c.Args[i+2:], true
		}
	}
	return nil, false
}

// tokenStateSegmentRe matches the token files by name anywhere in a word: the operator file
// and the store directory under a magus state dir. Anywhere, as cacheDirSegmentRe does, since
// an interpreter's script carries the path inside a program and a word whose value came from
// a parameter renders with the expansion dropped.
var tokenStateSegmentRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.-])magus/(?:mcp_token|tokens\.d)(?:$|[^A-Za-z0-9_.-])`)

// namesTokenState reports whether a word points at the operator token file, the token store,
// or the state dir holding both, by name or by resolving it against where the call runs.
func namesTokenState(location location, candidate string) bool {
	p := strings.TrimSpace(candidate)
	if p == "" {
		return false
	}
	if tokenStateSegmentRe.MatchString(filepath.ToSlash(p)) {
		return true
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		p = filepath.Join(home, rest)
	}
	abs := filepath.Clean(filepath.FromSlash(p))
	if !filepath.IsAbs(abs) {
		dir := location.dir
		if dir == "" {
			dir = location.workspace
		}
		if dir == "" {
			return false
		}
		abs = filepath.Join(dir, abs)
	}
	abs = resolveSymlinks(abs)
	state, err := auth.StateDir()
	if err != nil {
		return false
	}
	operator, _ := auth.OperatorPath()
	store, _ := auth.StoreDir()
	if abs == resolveSymlinks(state) || abs == resolveSymlinks(operator) {
		return true
	}
	rel, err := filepath.Rel(resolveSymlinks(store), abs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// tokenStateDenial is the one text both surfaces refuse with.
func tokenStateDenial(what string) string {
	return "magus guard denied access to " + what + ", which holds magus's token secrets: the operator token or the token store.\n\n" +
		"Those files are the credentials the server checks, so a session that reads one holds a grant nobody handed it, and one that writes one mints itself a token. " +
		"Use the token you were given. A person mints another with `" + hint.ConfigMCPConnectorCreate.String() + "` or `" + hint.ConfigConsoleTokenCreate.String() + "`, and `" +
		hint.ConfigMCPConnectorLs.String() + "` lists what exists without a secret."
}

// denyTokenStatePath is the path surface: the reason a file write aimed at the token state is
// refused, or "".
func denyTokenStatePath(location location, filePath string) string {
	if !namesTokenState(location, filePath) {
		return ""
	}
	return tokenStateDenial(strings.TrimSpace(filePath))
}

// denyTokenStateCommand is the command surface: the reason a shell line names the token
// state, or "". An unparsable line is matched as text.
//
// Every word counts: each command's arguments, each redirect's target, heredoc bodies, and
// the words inside command substitutions. A token file is refused whether it is read,
// written, copied or merely named, so the question is not which command takes it but
// whether any word does. A name passed to a metadata lister (tokenStateListers) is not a read: the listing shows
// the state dir's file names, `mcp_token` and `tokens.d/<name>.json`, and a token's name
// is what `magus config mcp connector ls` already prints. The secret is inside the file.
// The exemption holds only while the names stay names: a listing inside a substitution,
// or piped into anything but a text filter, can hand a file to a reader (`ls ... | xargs
// cat`), so it is refused like any other mention.
func denyTokenStateCommand(location location, command string, d Dialect) string {
	f, err := parseFile(command, d)
	if err != nil {
		if namesTokenState(location, command) {
			return tokenStateDenial(command)
		}
		return ""
	}
	listed := map[*syntax.Word]bool{}
	reason := ""
	var stack []syntax.Node
	syntax.Walk(f, func(n syntax.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		if reason != "" {
			return true
		}
		if call, ok := n.(*syntax.CallExpr); ok && len(call.Args) > 1 &&
			tokenStateListers[filepath.Base(literalWord(call.Args[0].Parts))] && namesStayNames(stack, d) {
			for _, arg := range call.Args[1:] {
				listed[arg] = true
			}
		}
		if w, ok := n.(*syntax.Word); ok && !listed[w] {
			if text := literalWord(w.Parts); namesTokenState(location, text) {
				reason = tokenStateDenial(text)
			}
		}
		return true
	})
	return reason
}

// tokenStateListers print a file's name and metadata and never its content.
var tokenStateListers = map[string]bool{"ls": true, "du": true, "stat": true, "test": true, "[": true}

// namesStayNames reports that the output of the innermost node of stack reaches only the
// console or text filters: no enclosing substitution, and every pipe it feeds is filters.
func namesStayNames(stack []syntax.Node, d Dialect) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			return false
		case *syntax.BinaryCmd:
			if (n.Op == syntax.Pipe || n.Op == syntax.PipeAll) && stack[i+1] == syntax.Node(n.X) &&
				!onlyTextFilters(n.Y, d) {
				return false
			}
		}
	}
	return true
}

// onlyTextFilters reports a statement every command of which is a name filter. awk and sed
// are left out: each can run a command or read a file its input names.
func onlyTextFilters(s *syntax.Stmt, d Dialect) bool {
	only := true
	syntax.Walk(s, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok && only {
			for _, c := range peelWrappers(literalWords(call.Args), d) {
				only = only && nameFilters[filepath.Base(c.Name)]
			}
		}
		return only
	})
	return only
}

// nameFilters trim or count lines of text and never open a file their input names.
var nameFilters = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "head": true, "tail": true,
	"wc": true, "sort": true, "uniq": true, "cut": true, "column": true,
}

// rankTokenState outranks every other verdict on the line, as the cache-dir rule does: no
// advice about a line's shape matters when the line reaches a secret.
func rankTokenState(v ShellVerdict, reason string) ShellVerdict {
	if reason == "" {
		return v
	}
	return ShellVerdict{Deny: reason, Rule: denyRule{Name: denyRuleTokenState}}
}
