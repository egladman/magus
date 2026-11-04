package guard

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// topicAmbiguousCIWord marks a session whose person named the bare word "ci"
// without saying which meaning: the magus `ci` target (often via `affected ci`),
// or the host's CI/CD (GitHub Actions and the rest). Recording it says nothing
// to the model: ambiguous-ci-word reads it on the next graded call.
const topicAmbiguousCIWord hint.MarkerKind = "topic-ambiguous-ci-word"

// topicCIWordResolved marks that the person clarified which meaning of "ci"
// they meant, so later calls that pick one no longer assume.
const topicCIWordResolved hint.MarkerKind = "topic-ci-word-resolved"

// ciWord is the bare token that collides: magus's reserved target name, and the
// everyday shorthand for a host's continuous-integration run.
var ciWord = regexp.MustCompile(`(?i)\bci\b`)

// ciMeansMagus is phrasing that picks the magus target. Any match clears the
// ambiguity for the session.
var ciMeansMagus = regexp.MustCompile(`(?i)\b(?:` +
	`affected\s+ci` +
	`|(?:magus\s+)?(?:run|affected)\s+ci` +
	`|ci\s+target` +
	`|target\s+(?:named\s+)?ci` +
	`|magus(?:'s|\s+)?\s*ci` +
	`|the\s+gate` +
	`)\b`)

// ciMeansHost is phrasing that picks the host's CI/CD. Any match clears the
// ambiguity for the session.
var ciMeansHost = regexp.MustCompile(`(?i)\b(?:` +
	`github\s+actions?` +
	`|gha` +
	`|gitlab\s+ci` +
	`|circle\s*ci` +
	`|buildkite` +
	`|jenkins` +
	`|travis` +
	`|azure\s+pipelines?` +
	`|ci/?cd` +
	`|continuous\s+integration` +
	`|workflow\s+run` +
	`|actions?\s+run` +
	`|pr\s+checks?` +
	`|pull[- ]request\s+checks?` +
	`|host(?:ed)?\s+ci` +
	`|platform\s+ci` +
	`)\b`)

// asksAmbiguousCIWord reports whether a message the person typed names "ci"
// without picking a meaning. A clarifying phrase in the same message resolves it.
func asksAmbiguousCIWord(message string) bool {
	if !ciWord.MatchString(message) {
		return false
	}
	return !clarifiesCIWord(message)
}

// clarifiesCIWord reports whether a message picks the magus target or the host's CI/CD.
func clarifiesCIWord(message string) bool {
	return ciMeansMagus.MatchString(message) || ciMeansHost.MatchString(message)
}

// recordAmbiguousCIWord marks the session from a message the person typed.
// A clarifying message resolves an earlier ambiguity; an ambiguous one arms the
// rule. It judges nothing and returns whether the ambiguous mark is live.
func recordAmbiguousCIWord(facts hint.Gate, message string) bool {
	if facts.Session() == "" {
		return false
	}
	if clarifiesCIWord(message) {
		facts.MarkFired(topicCIWordResolved)
		return false
	}
	if !asksAmbiguousCIWord(message) {
		return facts.AlreadyFired(topicAmbiguousCIWord) && !facts.AlreadyFired(topicCIWordResolved)
	}
	facts.MarkFired(topicAmbiguousCIWord)
	return facts.AlreadyFired(topicAmbiguousCIWord) && !facts.AlreadyFired(topicCIWordResolved)
}

// ambiguousCIWordLive reports whether the session still owes a clarification.
func ambiguousCIWordLive(facts hint.Gate) bool {
	return facts.AlreadyFired(topicAmbiguousCIWord) && !facts.AlreadyFired(topicCIWordResolved)
}

// denyAmbiguousCIWord is ambiguous-ci-word: the verdict for a graded call that
// picks magus `ci` or the host's CI/CD while the person's bare "ci" is still unresolved.
func denyAmbiguousCIWord(facts hint.Gate, assumes bool) string {
	if !assumes || !ambiguousCIWordLive(facts) {
		return ""
	}
	return "\"ci\" is ambiguous here: the magus `ci` target (often `magus affected ci`), or the host's CI/CD (GitHub Actions and the rest). Ask which one, then retry."
}

// assumesCIWord reports whether a shell line picks magus `ci` or the host's CI/CD:
// a magus run or affected of the `ci` target, a host CI CLI, or a path under a
// workflow tree.
func assumesCIWord(line string) bool {
	f, err := parseFile(line, DialectBash)
	if err != nil {
		return false
	}
	assumes := false
	syntax.Walk(f, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok || assumes {
			return !assumes
		}
		for _, c := range peelWrappers(literalWords(call.Args), DialectBash) {
			if invocationAssumesCIWord(c) {
				assumes = true
				break
			}
		}
		return !assumes
	})
	return assumes
}

// invocationAssumesCIWord reports one command that picks magus `ci` or host CI/CD.
func invocationAssumesCIWord(c hint.Invocation) bool {
	name := path.Base(c.Name)
	if isMagusInvocation(c) {
		if _, target, _, ok := splitRunInvocation(magusSubcommandWords(c.Args)); ok && target == "ci" {
			return true
		}
		return false
	}
	switch name {
	case "gh":
		return ghAssumesCIWord(c.Args)
	case "glab", "act", "circleci", "buildkite-agent", "travis":
		return true
	}
	for _, p := range invocationPaths(c) {
		if hostCIPath(p) {
			return true
		}
	}
	return false
}

// ghAssumesCIWord reports a gh invocation that reaches Actions or check runs.
func ghAssumesCIWord(args []string) bool {
	for i, a := range args {
		switch a {
		case "run", "workflow":
			return true
		case "checks", "check":
			return true
		case "pr":
			if i+1 < len(args) && (args[i+1] == "checks" || args[i+1] == "check") {
				return true
			}
		}
	}
	return false
}

// hostCIPath reports a path under a host CI config tree.
func hostCIPath(p string) bool {
	slash := filepath.ToSlash(p)
	return strings.Contains(slash, ".github/workflows") ||
		strings.Contains(slash, ".github/actions") ||
		strings.HasSuffix(slash, ".gitlab-ci.yml") ||
		strings.Contains(slash, ".circleci/") ||
		strings.Contains(slash, "azure-pipelines")
}

// writeAssumesCIWord reports a write path that picks the host's CI/CD.
func writeAssumesCIWord(path string) bool {
	return hostCIPath(path)
}
