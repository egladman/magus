package guard

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// clearedCacheDirRe names a build cache by the directory tools keep it in, as
// a path segment, so `go-build-notes` is not one.
var clearedCacheDirRe = regexp.MustCompile(`(?:^|/)(?:go-build|\.cargo/registry|\.gradle/caches|\.npm/_cacache|\.pnpm-store)(?:/|$)`)

// cacheClearArgs are the tool subcommands that empty a whole cache.
var cacheClearArgs = map[string][]string{
	"go":    {"clean", "-cache"},
	"cargo": {"cache", "--remove-dir"},
	"npm":   {"cache", "clean"},
	"pnpm":  {"store", "prune"},
}

// denyCacheClear is the reason a line clears a build cache by hand, or "". A
// trim to the declared budget is the only sanctioned way: a raw clear throws
// away what a build wants next.
func denyCacheClear(line string, d Dialect) string {
	f, err := parseFile(line, d)
	if err != nil {
		return ""
	}
	reason := ""
	syntax.Walk(f, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok || reason != "" {
			return reason == ""
		}
		for _, c := range peelWrappers(literalWords(call.Args), d) {
			if clearsCache(c) && reason == "" {
				reason = fmt.Sprintf("magus guard denied `%s %s`: a raw clear throws away what a build wants next. Run `%s`, which trims each cache a spell gives a budget and says what it did and what it leaves for you.",
					c.Name, strings.Join(c.Args, " "), types.ReclaimDiskCommand)
			}
		}
		return reason == ""
	})
	return reason
}

func clearsCache(c hint.Invocation) bool {
	program := path.Base(c.Name)
	if shape, ok := cacheClearArgs[program]; ok {
		return !slices.ContainsFunc(shape, func(want string) bool { return !slices.Contains(c.Args, want) })
	}
	if program != "rm" && program != "rmdir" && (program != "find" || !slices.Contains(c.Args, "-delete")) {
		return false
	}
	return slices.ContainsFunc(c.Args, func(w string) bool {
		return !strings.HasPrefix(w, "-") && clearedCacheDirRe.MatchString(w)
	})
}

// rankCacheClear outranks an existing deny: `go clean -cache` also earns the
// raw-tool refusal, whose fix names a target no project declares.
func rankCacheClear(v ShellVerdict, reason string) ShellVerdict {
	if reason == "" {
		return v
	}
	return ShellVerdict{Deny: reason, Rule: denyRule{Name: denyRuleCacheClear}}
}
