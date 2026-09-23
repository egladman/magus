package hint

import (
	"bufio"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Why lines the golden pins, named so a row reads as its routing decision
// rather than a paragraph. hedge* constants come from the package itself.
const (
	whyRefs         = "the pattern reads like a code symbol, and refs answers with verified occurrences"
	whyEntity       = "a domain entity (project, target, spell, op, diagnostic, doc) is query's side of the graph"
	whyText         = "query matches node ids, labels, and docs"
	whyRegex        = "the pattern is a regex, and id=~ runs it over node ids"
	whyAlternation  = "one query covers every -e pattern as an id regex alternation"
	whyDiagnostic   = "a diagnostic code has a graph node with its docs"
	whyBuzzOp       = "a Buzz op resolves to the spell functions defining it; refs covers compiled-language symbols only"
	whyProse        = "markdown headings are indexed as doc sections, so the query lands on the passage instead of the whole file"
	whyGlob         = "file nodes are indexed by path, and the glob converts to an id regex"
	whyGlobFallback = "the glob has no clean regex form, so list file nodes and narrow from there"
	whyFdExtension  = "file nodes are indexed by path, and -e is exactly an extension match"
	whyFdBoth       = "matchers AND: fd's pattern and its -e extension each become an id regex"
	whyFdRegex      = "file nodes are indexed by path, and fd's pattern is already a regex over names"
	hedgeDiagnostic = "If it misses, the code is not one this workspace defines."
	// The literal arm: refs --text asks grep's own question, so its Why and Hedge
	// promise a search rather than a semantic answer.
	whyLiteral       = "the same literal search, over the workspace's source files, with grep's exit codes"
	hedgeLiteral     = "Case-sensitive and literal; generated files are searched and counted separately."
	hedgeLiteralCase = "Case-SENSITIVE, unlike the -i you asked for, and literal."
)

// identPair is the routing a bare identifier earns: the two graph verbs that answer a
// better question when the pattern really is a symbol, then the literal search that
// answers the one grep actually asked. The third exists so the hedge on the first two
// ("grep is right") names a magus command instead of sending the reader out of the
// workspace to act on it.
//
// paths are the caller's own path operands, carried onto the text suggestion unchanged.
func identPair(pat, scope string, refsConf Confidence, paths ...string) []Suggestion {
	text := "magus refs --text " + quoted(pat)
	for _, p := range paths {
		text += " " + p
	}
	return []Suggestion{
		{Run: "magus refs " + pat, Why: whyRefs, Confidence: refsConf, Hedge: hedgeRefs},
		{Run: "magus query " + pat + scope, Why: whyEntity, Confidence: ConfidenceLow, Hedge: hedgeQuery},
		{Run: text, Why: whyLiteral, Confidence: ConfidenceHigh, Hedge: hedgeLiteral},
	}
}

func TestSuggestGolden(t *testing.T) {
	scoped := NewTranslator(WithProjects([]string{"internal/cache", "docs"}))
	rootScoped := NewTranslator(WithProjects([]string{".", "docs"}))
	nested := NewTranslator(WithProjects([]string{"docs", "docs/guides/integrations/agents", "libs/gopherbuzz"}))
	cases := []struct {
		name string
		tr   *Translator
		cmd  Invocation
		want []Suggestion
	}{
		// Abstention is the correctness property most likely to regress: a
		// translator that starts guessing shows up here first.
		{name: "awk one-liner abstains",
			cmd: Invocation{Name: "awk", Args: []string{"{print $1}", "file.txt"}}},
		{name: "sed in-place abstains",
			cmd: Invocation{Name: "sed", Args: []string{"-i", "s/a/b/", "file"}}},
		// A grep at named files asks no repo-wide question, so no GRAPH verb answers
		// it. refs --text does, scoped by the caller's own operands, which is why this
		// row stopped abstaining when that landed.
		{name: "single-file grep routes to the literal search",
			cmd: Invocation{Name: "grep", Args: []string{"pat", "onefile.txt"}},
			want: []Suggestion{
				{`magus refs --text "pat" onefile.txt`, whyLiteral, ConfidenceHigh, hedgeLiteral},
			}},
		{name: "cat abstains",
			cmd: Invocation{Name: "cat", Args: []string{"cmd/magus/main.go"}}},
		{name: "grep -f abstains: pattern unknowable",
			cmd: Invocation{Name: "grep", Args: []string{"-f", "patterns.txt", "-r", "."}}},
		{name: "--file= abstains too",
			cmd: Invocation{Name: "grep", Args: []string{"-r", "--file=p.txt", "."}}},
		{name: "unrecognized tool abstains",
			cmd: Invocation{Name: "frobnicate", Args: []string{"--all"}}},
		{name: "empty pattern abstains",
			cmd: Invocation{Name: "grep", Args: []string{"-r"}}},
		{name: "recursive grep over files only routes to the literal search",
			cmd: Invocation{Name: "grep", Args: []string{"-r", "pat", "a.go", "b.go"}},
			want: []Suggestion{
				{`magus refs --text "pat" a.go b.go`, whyLiteral, ConfidenceHigh, hedgeLiteral},
			}},
		{name: "multiple -e with -F abstains: no honest single translation",
			cmd: Invocation{Name: "grep", Args: []string{"-r", "-F", "-e", "alpha", "-e", "beta", "."}}},

		// Suggestions are paste-ready shell: a pattern the shell would
		// interpret inside double quotes must come back single-quoted, never
		// executable and never altered.
		{name: "double quote in pattern emits single-quoted",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", `say "hi"`, "."}},
			want: []Suggestion{{`magus query 'say "hi"'`, whyText, ConfidenceLow, hedgeQuery}}},
		{name: "backtick in pattern cannot execute on paste",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "foo`whoami`", "."}},
			want: []Suggestion{{"magus query 'foo`whoami`'", whyText, ConfidenceLow, hedgeQuery}}},
		{name: "command substitution in pattern is inert",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "$(cmd)", "."}},
			want: []Suggestion{{`magus query 'id=~$(cmd)'`, whyRegex, ConfidenceLow, hedgeQuery}}},
		{name: "backslash regex reaches magus intact",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", `out\d+`, "."}},
			want: []Suggestion{{`magus query 'id=~out\d+'`, whyRegex, ConfidenceLow, hedgeQuery}}},
		// Double quotes do NOT stop history expansion in an interactive shell,
		// which is exactly where a suggestion gets pasted.
		{name: "bang in pattern emits single-quoted",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "foo!bar", "."}},
			want: []Suggestion{{"magus query 'foo!bar'", whyText, ConfidenceLow, hedgeQuery}}},
		{name: "bang in a regex pattern emits a single-quoted matcher",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "foo.*!bar", "."}},
			want: []Suggestion{{"magus query 'id=~foo.*!bar'", whyRegex, ConfidenceLow, hedgeQuery}}},

		{name: "bare identifier routes refs then query",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "funcName", "."}},
			want: identPair("funcName", "", ConfidenceMedium)},
		{name: "word boundary raises refs to High",
			cmd:  Invocation{Name: "grep", Args: []string{"-rnw", "Identifier", "."}},
			want: identPair("Identifier", "", ConfidenceHigh)},
		// -i asked for a fold the graph does not do, so every hedge says so.
		{name: "ignore-case appends the case hedge",
			cmd: Invocation{Name: "grep", Args: []string{"-rni", "todo", "."}},
			want: []Suggestion{
				{"magus refs todo", whyRefs, ConfidenceMedium, hedgeRefs + hedgeCase},
				{"magus query todo", whyEntity, ConfidenceLow, hedgeQuery + hedgeCase},
				// The literal arm states the fold plainly rather than appending the
				// shared clause: refs --text cannot honor -i at all, where the graph
				// verbs merely match case-sensitively.
				{`magus refs --text "todo"`, whyLiteral, ConfidenceHigh, hedgeLiteralCase},
			}},
		{name: "phrase routes to quoted query",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "go test", "docs/"}},
			want: []Suggestion{{`magus query "go test"`, whyText, ConfidenceLow, hedgeQuery}}},
		{name: "diagnostic code routes to query",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "MGS2011", "docs/"}},
			want: []Suggestion{{"magus query MGS2011", whyDiagnostic, ConfidenceHigh, hedgeDiagnostic}}},
		{name: "buzz op routes to query",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "mgs_listManifests", "spells/"}},
			want: []Suggestion{{"magus query mgs_listManifests", whyBuzzOp, ConfidenceHigh, hedgeQuery}}},
		{name: "regex pattern routes to id matcher",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", `guard.*Re`, "."}},
			want: []Suggestion{{`magus query "id=~guard.*Re"`, whyRegex, ConfidenceLow, hedgeQuery}}},
		{name: "fixed-strings never emits id matcher",
			cmd:  Invocation{Name: "grep", Args: []string{"-rnF", "a.b", "."}},
			want: []Suggestion{{`magus query "a.b"`, whyText, ConfidenceLow, hedgeQuery}}},
		// fgrep is grep -F by definition, so its pattern is literal with no -F.
		{name: "fgrep is fixed by default and never emits an id matcher",
			cmd:  Invocation{Name: "fgrep", Args: []string{"-r", "a.b", "."}},
			want: []Suggestion{{`magus query "a.b"`, whyText, ConfidenceLow, hedgeQuery}}},
		{name: "egrep behaves like grep",
			cmd:  Invocation{Name: "egrep", Args: []string{"-r", "Foo", "."}},
			want: identPair("Foo", "", ConfidenceMedium)},
		{name: "egrep regex still routes to an id matcher",
			cmd:  Invocation{Name: "egrep", Args: []string{"-r", `guard.*Re`, "."}},
			want: []Suggestion{{`magus query "id=~guard.*Re"`, whyRegex, ConfidenceLow, hedgeQuery}}},
		{name: "rg is repo-wide by default",
			cmd:  Invocation{Name: "rg", Args: []string{"symbolName"}},
			want: identPair("symbolName", "", ConfidenceMedium)},
		{name: "rg with file operand keeps suggestion unscoped",
			tr:   scoped,
			cmd:  Invocation{Name: "rg", Args: []string{"symbolName", "internal/cache/keys.go"}},
			want: identPair("symbolName", "", ConfidenceMedium, "internal/cache/keys.go")},
		{name: "multiple -e joins into one alternation query",
			cmd:  Invocation{Name: "rg", Args: []string{"-e", "alpha", "-e", "beta"}},
			want: []Suggestion{{`magus query "id=~alpha|beta"`, whyAlternation, ConfidenceLow, hedgeQuery}}},
		{name: "grep -G is boolean and does not eat the pattern",
			cmd:  Invocation{Name: "grep", Args: []string{"-rG", "MyFunc", "src/"}},
			want: identPair("MyFunc", "", ConfidenceMedium, "src/")},
		{name: "ag -t is boolean and does not eat the pattern",
			cmd:  Invocation{Name: "ag", Args: []string{"-t", "someSymbol"}},
			want: identPair("someSymbol", "", ConfidenceMedium)},
		{name: "--color takes a value and is not the pattern",
			cmd:  Invocation{Name: "rg", Args: []string{"--color", "never", "someSymbol"}},
			want: identPair("someSymbol", "", ConfidenceMedium)},
		{name: "markdown operand routes to docsection",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "checkpoint", "docs/guide.md"}},
			want: []Suggestion{{`magus query kind=docsection "checkpoint"`, whyProse, ConfidenceMedium, hedgeProse}}},

		// Short-flag bundles: a value-taking letter must eat its value,
		// attached or separate, and never surrender the pattern position.
		{name: "-e with an attached value carries the pattern",
			cmd:  Invocation{Name: "grep", Args: []string{"-r", "-ealpha", "."}},
			want: identPair("alpha", "", ConfidenceMedium)},
		{name: "-A3 attaches its value and leaves the pattern alone",
			cmd:  Invocation{Name: "grep", Args: []string{"-r", "-A3", "someSymbol", "."}},
			want: identPair("someSymbol", "", ConfidenceMedium)},
		{name: "a value short ending a bundle eats the next word",
			cmd:  Invocation{Name: "grep", Args: []string{"-rnA", "3", "someSymbol", "."}},
			want: identPair("someSymbol", "", ConfidenceMedium)},
		{name: "a value short inside a bundle takes the rest as its value",
			cmd:  Invocation{Name: "grep", Args: []string{"-rnA3", "someSymbol", "."}},
			want: identPair("someSymbol", "", ConfidenceMedium)},
		{name: "--regexp= carries the pattern",
			cmd:  Invocation{Name: "grep", Args: []string{"-r", "--regexp=Foo", "."}},
			want: identPair("Foo", "", ConfidenceMedium)},
		{name: "-- makes the rest operands, flag-looking or not",
			cmd:  Invocation{Name: "grep", Args: []string{"-r", "--", "--exclude", "."}},
			want: []Suggestion{{`magus query "--exclude"`, whyText, ConfidenceLow, hedgeQuery}}},

		{name: "find -name glob converts to anchored regex",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "*.go"}},
			want: []Suggestion{{`magus query kind=file 'id=~\.go$'`, whyGlob, ConfidenceHigh, hedgeFile}}},
		{name: "find with -exec still suggests from -name",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "*.buzz", "-exec", "wc", "-l", "{}", ";"}},
			want: []Suggestion{{`magus query kind=file 'id=~\.buzz$'`, whyGlob, ConfidenceHigh, hedgeFile}}},
		{name: "find unconvertible glob falls back to bare file query",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "[ab]*.go"}},
			want: []Suggestion{{"magus query kind=file", whyGlobFallback, ConfidenceLow, hedgeFile}}},
		// A negated or branched filter has no honest single translation: the
		// -name in it says the opposite of the ask, or only half of it.
		{name: "find ! abstains",
			cmd: Invocation{Name: "find", Args: []string{".", "!", "-name", "*_test.go"}}},
		{name: "find -not abstains",
			cmd: Invocation{Name: "find", Args: []string{".", "-not", "-name", "*.go"}}},
		{name: "find -o abstains",
			cmd: Invocation{Name: "find", Args: []string{".", "-name", "*.go", "-o", "-name", "*.md"}}},
		{name: "find -prune abstains",
			cmd: Invocation{Name: "find", Args: []string{".", "-path", "./vendor", "-prune"}}},
		{name: "-iname folds case",
			cmd:  Invocation{Name: "find", Args: []string{".", "-iname", "*.md"}},
			want: []Suggestion{{`magus query kind=file 'id=~(?i)\.md$'`, whyGlob, ConfidenceHigh, hedgeFile}}},
		{name: "? does not cross a separator",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "cmd?.go"}},
			want: []Suggestion{{`magus query kind=file 'id=~cmd[^/]\.go$'`, whyGlob, ConfidenceHigh, hedgeFile}}},
		{name: "-name ? is too open to translate",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "?"}},
			want: []Suggestion{{"magus query kind=file", whyGlobFallback, ConfidenceLow, hedgeFile}}},
		// -path matches the whole path, so its glob may carry separators.
		{name: "-path glob keeps its separators",
			cmd:  Invocation{Name: "find", Args: []string{".", "-path", "*/internal/*"}},
			want: []Suggestion{{"magus query kind=file id=~/internal/", whyGlob, ConfidenceHigh, hedgeFile}}},
		{name: "-ipath folds case",
			cmd:  Invocation{Name: "find", Args: []string{".", "-ipath", "*/DOCS/*"}},
			want: []Suggestion{{`magus query kind=file "id=~(?i)/DOCS/"`, whyGlob, ConfidenceHigh, hedgeFile}}},
		{name: "-name rejects a glob with a separator",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "*/x.go"}},
			want: []Suggestion{{"magus query kind=file", whyGlobFallback, ConfidenceLow, hedgeFile}}},

		{name: "fd extension converts like a glob",
			cmd:  Invocation{Name: "fd", Args: []string{"-e", "go"}},
			want: []Suggestion{{`magus query kind=file 'id=~\.go$'`, whyFdExtension, ConfidenceHigh, hedgeFile}}},
		{name: "fd --extension= is the same as -e",
			cmd:  Invocation{Name: "fd", Args: []string{"--extension=go"}},
			want: []Suggestion{{`magus query kind=file 'id=~\.go$'`, whyFdExtension, ConfidenceHigh, hedgeFile}}},
		{name: "fd multiple -e becomes one alternation",
			cmd:  Invocation{Name: "fd", Args: []string{"-e", "go", "-e", "md"}},
			want: []Suggestion{{`magus query kind=file 'id=~\.(go|md)$'`, whyFdExtension, ConfidenceHigh, hedgeFile}}},
		{name: "fd -e keeps the pattern operand",
			cmd:  Invocation{Name: "fd", Args: []string{"-e", "go", "parse"}},
			want: []Suggestion{{`magus query kind=file id=~parse 'id=~\.go$'`, whyFdBoth, ConfidenceMedium, hedgeFile}}},
		{name: "fd pattern passes through as a regex",
			cmd:  Invocation{Name: "fd", Args: []string{"guard_", "cmd/magus"}},
			want: []Suggestion{{"magus query kind=file id=~guard_", whyFdRegex, ConfidenceMedium, hedgeFile}}},
		// -x/-X introduce a command fd runs; nothing after it is fd's pattern.
		{name: "fd -x payload never becomes the pattern",
			cmd:  Invocation{Name: "fd", Args: []string{"-e", "go", "-x", "wc", "-l"}},
			want: []Suggestion{{`magus query kind=file 'id=~\.go$'`, whyFdExtension, ConfidenceHigh, hedgeFile}}},
		{name: "fd -X payload never becomes the pattern",
			cmd:  Invocation{Name: "fd", Args: []string{"guard_", "-X", "rm"}},
			want: []Suggestion{{"magus query kind=file id=~guard_", whyFdRegex, ConfidenceMedium, hedgeFile}}},
		{name: "fd -g converts a glob",
			cmd:  Invocation{Name: "fd", Args: []string{"-g", "*.go"}},
			want: []Suggestion{{`magus query kind=file 'id=~\.go$'`, whyGlob, ConfidenceHigh, hedgeFile}}},
		{name: "fd --glob unconvertible falls back to bare file query",
			cmd:  Invocation{Name: "fd", Args: []string{"--glob", "[ab]*.go"}},
			want: []Suggestion{{"magus query kind=file", whyGlobFallback, ConfidenceLow, hedgeFile}}},
		{name: "fd -g with -e converts both",
			cmd:  Invocation{Name: "fd", Args: []string{"-g", "-e", "go", "parse*"}},
			want: []Suggestion{{`magus query kind=file id=~parse 'id=~\.go$'`, whyFdBoth, ConfidenceMedium, hedgeFile}}},
		// With -e in play an unconvertible glob has no honest remainder: a bare
		// extension query would drop the pattern half of an ANDed ask.
		{name: "fd -g unconvertible with -e abstains",
			cmd: Invocation{Name: "fd", Args: []string{"-g", "-e", "go", "[ab]*"}}},

		{name: "project scoping lands on query suggestions only",
			tr:   scoped,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "internal/cache/"}},
			want: identPair("Foo", ` 'project=~^internal/cache(/|$)'`, ConfidenceMedium, "internal/cache/")},
		// The anchored regex is what keeps the suggestion from being narrower than
		// the grep: project=docs would exclude the nested project's nodes outright.
		{name: "scoping a project with a nested one still covers the nest",
			tr:   nested,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs/"}},
			want: identPair("Foo", ` 'project=~^docs(/|$)'`, ConfidenceMedium, "docs/")},
		{name: "operands in two projects abstain from scoping",
			tr:   nested,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs", "libs/gopherbuzz"}},
			want: identPair("Foo", "", ConfidenceMedium, "docs", "libs/gopherbuzz")},
		{name: "two operands in one project still scope",
			tr:   nested,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs/site", "docs/tour"}},
			want: identPair("Foo", ` 'project=~^docs(/|$)'`, ConfidenceMedium, "docs/site", "docs/tour")},
		// A string prefix is not a path prefix, which is why scope compares
		// against proj+"/" rather than the bare name.
		{name: "a sibling sharing a name prefix does not scope",
			tr:   nested,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs-old/"}},
			want: identPair("Foo", "", ConfidenceMedium, "docs-old/")},
		{name: "root project never scopes: project=. says nothing",
			tr:   rootScoped,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "."}},
			want: identPair("Foo", "", ConfidenceMedium)},
		{name: "root project in the list still lets a sibling scope",
			tr:   rootScoped,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs/"}},
			want: identPair("Foo", ` 'project=~^docs(/|$)'`, ConfidenceMedium, "docs/")},
		{name: "no WithProjects means no project= anywhere",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "internal/cache/"}},
			want: identPair("Foo", "", ConfidenceMedium, "internal/cache/")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tc.tr
			if tr == nil {
				tr = NewTranslator()
			}
			require.Equal(t, tc.want, tr.Suggest(tc.cmd))
		})
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		cmd  Invocation
		want Class
	}{
		{"recursive grep", Invocation{Name: "grep", Args: []string{"-rn", "pat", "."}}, ClassSearchSource},
		{"grep -f is still a source search", Invocation{Name: "grep", Args: []string{"-f", "pats.txt", "-r", "."}}, ClassSearchSource},
		{"single-file grep reads", Invocation{Name: "grep", Args: []string{"pat", "onefile.txt"}}, ClassRead},
		{"grep of markdown is prose", Invocation{Name: "grep", Args: []string{"pat", "notes.md"}}, ClassSearchProse},
		{"rg", Invocation{Name: "rg", Args: []string{"pat"}}, ClassSearchSource},
		{"rg of markdown is prose", Invocation{Name: "rg", Args: []string{"pat", "MAGUS.md"}}, ClassSearchProse},
		{"ag", Invocation{Name: "ag", Args: []string{"pat", "src"}}, ClassSearchSource},
		{"egrep", Invocation{Name: "egrep", Args: []string{"-r", "pat", "."}}, ClassSearchSource},
		{"fgrep", Invocation{Name: "fgrep", Args: []string{"-r", "pat", "."}}, ClassSearchSource},
		{"find", Invocation{Name: "find", Args: []string{".", "-name", "*.go"}}, ClassFileFind},
		{"find without -name is still a file find", Invocation{Name: "find", Args: []string{".", "-type", "d"}}, ClassFileFind},
		{"fd", Invocation{Name: "fd", Args: []string{"-e", "go"}}, ClassFileFind},
		{"cat", Invocation{Name: "cat", Args: []string{"go.mod"}}, ClassRead},
		{"bat", Invocation{Name: "bat", Args: []string{"internal/hint/hint.go"}}, ClassRead},
		{"head", Invocation{Name: "head", Args: []string{"-50", "main.go"}}, ClassRead},
		{"tail", Invocation{Name: "tail", Args: []string{"-f", "run.log"}}, ClassRead},
		{"less", Invocation{Name: "less", Args: []string{"MAGUS.md"}}, ClassRead},
		{"more", Invocation{Name: "more", Args: []string{"MAGUS.md"}}, ClassRead},
		{"sed address-print", Invocation{Name: "sed", Args: []string{"-n", "10,20p", "f.buzz"}}, ClassRead},
		{"sed substitution", Invocation{Name: "sed", Args: []string{"s/a/b/", "f"}}, ClassTransform},
		{"sed -n with -i still transforms", Invocation{Name: "sed", Args: []string{"-n", "-i", "s/a/b/p", "f"}}, ClassTransform},
		{"awk", Invocation{Name: "awk", Args: []string{"{print $1}"}}, ClassTransform},
		{"sd", Invocation{Name: "sd", Args: []string{"a", "b", "."}}, ClassTransform},
		{"ls", Invocation{Name: "ls", Args: []string{"-la"}}, ClassNone},
		{"unrecognized", Invocation{Name: "frobnicate"}, ClassNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Classify(tc.cmd))
		})
	}
}

func TestPatterns(t *testing.T) {
	cases := []struct {
		name string
		cmd  Invocation
		want []string
	}{
		{"every -e value", Invocation{Name: "rg", Args: []string{"-e", "alpha", "-e", "beta", "src"}}, []string{"alpha", "beta"}},
		{"first non-flag operand", Invocation{Name: "grep", Args: []string{"-rn", "pat", "."}}, []string{"pat"}},
		{"flag values are not operands", Invocation{Name: "rg", Args: []string{"-t", "go", "pat"}}, []string{"pat"}},
		{"--color takes a value", Invocation{Name: "rg", Args: []string{"--color", "never", "pat"}}, []string{"pat"}},
		{"-f makes the pattern unknowable", Invocation{Name: "grep", Args: []string{"-f", "pats.txt", "-r", "."}}, nil},
		{"an empty pattern is no pattern", Invocation{Name: "grep", Args: []string{"-rn", "", "."}}, nil},
		{"non-search command", Invocation{Name: "cat", Args: []string{"x.md"}}, nil},
		{"find is not a search command", Invocation{Name: "find", Args: []string{".", "-name", "*.go"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Patterns(tc.cmd))
		})
	}
}

// TestToolSpecTable pins the per-tool flag disagreements the toolSpec comment
// explains in prose: a letter in a tool's value set eats the next word, and the
// same letter elsewhere is boolean and leaves it as the pattern. Driven off the
// table so a new row is covered the moment it is added.
func TestToolSpecTable(t *testing.T) {
	union := commonValueShorts
	for _, spec := range searchTools {
		for _, c := range spec.valueShorts {
			if !strings.ContainsRune(union, c) {
				union += string(c)
			}
		}
	}
	for _, tool := range slices.Sorted(maps.Keys(searchTools)) {
		t.Run(tool, func(t *testing.T) {
			valueShorts := commonValueShorts + searchTools[tool].valueShorts
			for _, c := range valueShorts {
				t.Run("value/-"+string(c), func(t *testing.T) {
					require.Equal(t, []string{"PATTERN"},
						Patterns(Invocation{Name: tool, Args: []string{"-" + string(c), "VALUE", "PATTERN"}}),
						"-%c should consume VALUE", c)
				})
			}
			for _, c := range union {
				if strings.ContainsRune(valueShorts, c) {
					continue
				}
				t.Run("boolean/-"+string(c), func(t *testing.T) {
					require.Equal(t, []string{"VALUE"},
						Patterns(Invocation{Name: tool, Args: []string{"-" + string(c), "VALUE", "PATTERN"}}),
						"-%c should be boolean here", c)
				})
			}
		})
	}
}

func TestIsIdentifier(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"a", false},
		{"ab", false}, // the shape needs at least two characters AFTER the first
		{"abc", true},
		{"_x1", true},
		{"1abc", false},
		{"Foo_Bar9", true},
		{"has space", false},
		{"has-dash", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, IsIdentifier(tc.in))
		})
	}
}

func TestIsSearchTool(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"grep", true},
		{"egrep", true},
		{"fgrep", true},
		{"rg", true},
		{"ag", true},
		{"find", false},
		{"fd", false},
		{"cat", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, IsSearchTool(tc.in))
		})
	}
}

// TestQuoting covers the paste-safety policy character by character: every
// member of each set, and the bare branch that keeps a plain matcher readable.
func TestQuoting(t *testing.T) {
	t.Run("quoted", func(t *testing.T) {
		cases := []struct{ name, in, want string }{
			{"plain stays in double quotes", "plain", `"plain"`},
			{"empty", "", `""`},
			{"space", "go test", `"go test"`},
			{"apostrophe is literal in double quotes", "it's", `"it's"`},
			{"dollar", "a$b", `'a$b'`},
			{"backslash", `a\b`, `'a\b'`},
			{"double quote", `a"b`, `'a"b'`},
			{"backtick", "a`b", "'a`b'"},
			{"bang", "a!b", `'a!b'`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				assert.Equal(t, tc.want, quoted(tc.in))
			})
		}
	})

	t.Run("matcherArg", func(t *testing.T) {
		cases := []struct{ name, in, want string }{
			{"bare matcher reads as the docs write it", "id=~foo", "id=~foo"},
			{"space", "id=~a b", `"id=~a b"`},
			{"pipe", "id=~a|b", `"id=~a|b"`},
			{"open paren", "id=~(a", `"id=~(a"`},
			{"close paren", "id=~a)", `"id=~a)"`},
			{"open brace", "id=~{2", `"id=~{2"`},
			{"close brace", "id=~2}", `"id=~2}"`},
			{"open bracket", "id=~[a", `"id=~[a"`},
			{"close bracket", "id=~a]", `"id=~a]"`},
			{"less than", "id=~a<b", `"id=~a<b"`},
			{"greater than", "id=~a>b", `"id=~a>b"`},
			{"ampersand", "id=~a&b", `"id=~a&b"`},
			{"semicolon", "id=~a;b", `"id=~a;b"`},
			{"star", "id=~a*", `"id=~a*"`},
			{"question", "id=~a?", `"id=~a?"`},
			{"apostrophe", "id=~a'b", `"id=~a'b"`},
			{"dollar", "id=~a$", `'id=~a$'`},
			{"backslash", `id=~a\.b`, `'id=~a\.b'`},
			{"double quote", `id=~a"b`, `'id=~a"b'`},
			{"backtick", "id=~a`b", "'id=~a`b'"},
			{"bang outranks the double-quote branch", "id=~a!b", `'id=~a!b'`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				assert.Equal(t, tc.want, matcherArg(tc.in))
			})
		}
	})

	t.Run("singleQuoted", func(t *testing.T) {
		cases := []struct{ name, in, want string }{
			{"plain", "plain", `'plain'`},
			{"empty", "", `''`},
			{"one apostrophe splices close-escape-reopen", "it's", `'it'\''s'`},
			{"only an apostrophe", "'", `''\'''`},
			{"two apostrophes", "a'b'c", `'a'\''b'\''c'`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				assert.Equal(t, tc.want, singleQuoted(tc.in))
			})
		}
	})
}

func TestGlobToRe(t *testing.T) {
	cases := []struct {
		name         string
		glob         string
		basenameOnly bool
		foldCase     bool
		want         string
		ok           bool
	}{
		{name: "leading star drops the anchor and keeps the tail", glob: "*.go", basenameOnly: true, want: `\.go$`, ok: true},
		// A trailing star means the name continues, so anchoring it would be a
		// stricter question than the glob asked.
		{name: "trailing star does not anchor", glob: "main*", basenameOnly: true, want: "main", ok: true},
		{name: "stars at both ends leave a bare substring", glob: "*util*", basenameOnly: true, want: "util", ok: true},
		{name: "literal name anchors", glob: "main.go", basenameOnly: true, want: `main\.go$`, ok: true},
		{name: "? does not cross a separator", glob: "cmd?.go", basenameOnly: true, want: `cmd[^/]\.go$`, ok: true},
		{name: "regex metacharacters are quoted", glob: "v1.2*", basenameOnly: true, want: `v1\.2`, ok: true},
		{name: "fold case prefixes the flag", glob: "*.md", basenameOnly: true, foldCase: true, want: `(?i)\.md$`, ok: true},
		{name: "fold case on an unanchored glob", glob: "*README*", basenameOnly: true, foldCase: true, want: "(?i)README", ok: true},
		{name: "a path glob may carry separators", glob: "src/*.go", want: `src/[^/]*\.go$`, ok: true},
		{name: "a path glob keeps interior separators", glob: "*/internal/*", want: "/internal/", ok: true},
		{name: "basenameOnly rejects a separator", glob: "src/*.go", basenameOnly: true},
		{name: "brackets do not convert", glob: "[ab]*.go", basenameOnly: true},
		{name: "braces do not convert", glob: "{a,b}.go", basenameOnly: true},
		{name: "empty glob", glob: ""},
		{name: "bare star matches everything", glob: "*", basenameOnly: true},
		{name: "bare ? matches everything", glob: "?", basenameOnly: true},
		{name: "?* is still all wildcards", glob: "?*", basenameOnly: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			re, ok := globToRe(tc.glob, tc.basenameOnly, tc.foldCase)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, re)
		})
	}
}

// TestCommandDistribution pins the aggregate behavior over a realistic set of
// recorded commands. The exact numbers are a snapshot: a change that starts
// over-suggesting (or silently stops abstaining) moves them and must be seen
// and re-justified here, not discovered in advisory noise later.
func TestCommandDistribution(t *testing.T) {
	f, err := os.Open("testdata/commands.txt")
	require.NoError(t, err)
	defer f.Close()

	tr := NewTranslator()
	counts := map[Class]int{}
	suggesting, abstaining := 0, 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// The command list is controlled (no spaces inside an argument), so a
		// whitespace split plus quote-stripping stands in for a shell parser
		// without depending on one.
		fields := strings.Fields(line)
		for i, w := range fields {
			fields[i] = strings.Trim(w, `"'`)
		}
		cmd := Invocation{Name: fields[0], Args: fields[1:]}
		counts[Classify(cmd)]++
		if len(tr.Suggest(cmd)) > 0 {
			suggesting++
		} else {
			abstaining++
		}
	}
	require.NoError(t, sc.Err())

	assert.Equal(t, map[Class]int{
		ClassSearchSource: 22,
		ClassSearchProse:  4,
		ClassRead:         11,
		ClassFileFind:     10,
		ClassTransform:    5,
		ClassNone:         4,
	}, counts)
	// 33 -> 36 when refs --text landed. The three that moved are the shapes with no
	// GRAPH answer: a non-recursive grep, and one pointed only at named files. They
	// abstained for as long as magus had no raw-text search, and silence there is
	// what sent a reader back to grep with nothing to try. This is the one direction
	// an increase is allowed to move: a literal-for-literal translation, not a guess.
	assert.Equal(t, 36, suggesting)
	assert.Equal(t, 20, abstaining)
}

// The tool parsers are graded on REAL invocations from both coreutils families, because
// the two disagree in exactly the places a guard reads. Every case below names which
// family spells it that way, so a reader can tell a deliberate BSD-ism from a typo.
//
// The rule these serve is a safety one, so the bias is stated once here rather than per
// case: where a spelling is ambiguous ACROSS families, the answer is the unsafe reading.
// A parser that guesses right on GNU and wrong on BSD is worse than one that declines,
// because the wrong guess is silent on half the machines that run it.

// TestVariantInferenceReadsOnlyFamilySpecificSpellings pins what the flag evidence can and
// cannot decide. The pair with LocalVariant is the design: one says what the author
// assumed, the other what will run it, and only their DISAGREEMENT is interesting.
func TestVariantInferenceReadsOnlyFamilySpecificSpellings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cmd  Invocation
		want Variant
	}{
		// Long options are the strongest signal: BSD sed and BSD xargs have none.
		{name: "sed long in-place is GNU", cmd: Invocation{Name: "sed", Args: []string{"--in-place", "s/a/b/"}}, want: VariantGNU},
		{name: "sed long in-place with value is GNU", cmd: Invocation{Name: "sed", Args: []string{"--in-place=.bak"}}, want: VariantGNU},
		{name: "xargs -r is GNU", cmd: Invocation{Name: "xargs", Args: []string{"-r", "rm"}}, want: VariantGNU},
		{name: "xargs --null is GNU", cmd: Invocation{Name: "xargs", Args: []string{"--null", "rm"}}, want: VariantGNU},
		{name: "grep -P is GNU", cmd: Invocation{Name: "grep", Args: []string{"-P", `\d+`}}, want: VariantGNU},
		{name: "find -printf is GNU", cmd: Invocation{Name: "find", Args: []string{".", "-printf", "%p"}}, want: VariantGNU},

		{name: "xargs -J is BSD", cmd: Invocation{Name: "xargs", Args: []string{"-J", "%", "cp"}}, want: VariantBSD},
		{name: "xargs -L is BSD", cmd: Invocation{Name: "xargs", Args: []string{"-L", "1", "rm"}}, want: VariantBSD},

		// Nothing family-specific, so no claim. Saying "GNU" here because it is the common
		// case would be inventing evidence.
		{name: "portable sed decides nothing", cmd: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "f"}}, want: VariantUnknown},
		{name: "portable grep decides nothing", cmd: Invocation{Name: "grep", Args: []string{"-rn", "foo", "."}}, want: VariantUnknown},
		{name: "a tool with no table entry", cmd: Invocation{Name: "awk", Args: []string{"{print}"}}, want: VariantUnknown},

		// Evidence both ways is a line no single tool would accept; unknown is truer than
		// picking a winner.
		{name: "both families named at once", cmd: Invocation{Name: "xargs", Args: []string{"-r", "-J", "%", "cp"}}, want: VariantUnknown},

		{name: "an absolute path resolves by base name",
			cmd: Invocation{Name: "/usr/bin/xargs", Args: []string{"-r", "rm"}}, want: VariantGNU},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, InferVariant(tc.cmd))
		})
	}
}

// TestPortabilityGapNamesBothSides pins that the gap is reported only when both sides are
// known AND disagree. It is context, never a refusal: the command may be headed for a
// container, which is a machine this cannot see.
func TestPortabilityGapNamesBothSides(t *testing.T) {
	t.Parallel()

	gnuSed := Invocation{Name: "sed", Args: []string{"--in-place", "s/a/b/", "f"}}
	assert.Contains(t, PortabilityGap(gnuSed, VariantBSD), "gnu")
	assert.Contains(t, PortabilityGap(gnuSed, VariantBSD), "bsd")
	assert.Contains(t, PortabilityGap(gnuSed, VariantBSD), "sed")

	assert.Empty(t, PortabilityGap(gnuSed, VariantGNU), "agreement is not a gap")
	assert.Empty(t, PortabilityGap(gnuSed, VariantUnknown), "an unknown host cannot disagree")
	assert.Empty(t, PortabilityGap(Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "f"}}, VariantBSD),
		"a portable spelling names no family, so there is nothing to compare")
}

// TestLocalVariantIsStableAndDerivedOnce pins the session-scoped contract: one answer for
// the process, so nothing recomputes it per line.
func TestLocalVariantIsStableAndDerivedOnce(t *testing.T) {
	t.Parallel()
	assert.Equal(t, LocalVariant(), LocalVariant())
	assert.Equal(t, LocalVariant(), NewTranslator().Variant(),
		"a translator starts from the host answer, so the common case needs no option")
	assert.Equal(t, VariantBSD, NewTranslator(WithVariant(VariantBSD)).Variant(),
		"an explicit override wins, for a caller reasoning about another machine")
}

func TestSedFilesAcrossCoreutilsFamilies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    []string
		files   []string
		bounded bool
	}{
		// The ambiguity that motivates the whole function. BSD sed reads the word after a
		// bare -i as the backup SUFFIX; GNU sed reads it as the first FILE. No parser can
		// tell them apart, so the bare form is never bounded.
		{name: "bare -i is unsplittable (BSD suffix vs GNU file)", args: []string{"-i", "", "s/a/b/", "f.go"}},
		{name: "bare -i even with obvious files", args: []string{"-i", "s/a/b/", "one.go", "two.go"}},
		{name: "bare -i last", args: []string{"-e", "s/a/b/", "-i", "one.go"}},

		// Packed suffix is unambiguous in both families.
		{name: "GNU packed suffix", args: []string{"-i.bak", "s/a/b/", "one.go"}, files: []string{"one.go"}, bounded: true},
		{name: "BSD packed suffix", args: []string{"-i''", "s/a/b/", "one.go"}, files: []string{"one.go"}, bounded: true},
		{name: "packed suffix, several files", args: []string{"-i.bak", "s/a/b/", "a.go", "b.go", "c.go"},
			files: []string{"a.go", "b.go", "c.go"}, bounded: true},

		// --in-place is GNU only; BSD sed has no long options at all.
		{name: "GNU long form takes no suffix", args: []string{"--in-place", "s/a/b/", "one.go"},
			files: []string{"one.go"}, bounded: true},
		{name: "GNU long form with inline suffix", args: []string{"--in-place=.bak", "s/a/b/", "one.go"},
			files: []string{"one.go"}, bounded: true},

		// -e and -f supply the script, so the first bare word is then a FILE, not a script.
		// Reading it as a script would drop a real file from the list and under-report.
		{name: "-e supplies the script so no operand is consumed", args: []string{"-i.bak", "-e", "s/a/b/", "one.go", "two.go"},
			files: []string{"one.go", "two.go"}, bounded: true},
		{name: "several -e scripts", args: []string{"-i.bak", "-e", "s/a/b/", "-e", "s/c/d/", "one.go"},
			files: []string{"one.go"}, bounded: true},
		{name: "-f names a script FILE, which is not an edited file", args: []string{"-i.bak", "-f", "prog.sed", "one.go"},
			files: []string{"one.go"}, bounded: true},

		// A script arriving as the first bare word is consumed, leaving the rest as files.
		{name: "script then file", args: []string{"-i.bak", "s/a/b/", "one.go"}, files: []string{"one.go"}, bounded: true},

		// Unbounded operand sets: the edited files are not visible on the line.
		{name: "recursive glob", args: []string{"-i.bak", "s/a/b/", "**/*.go"}},
		{name: "simple glob", args: []string{"-i.bak", "s/a/b/", "*.go"}},
		{name: "character class", args: []string{"-i.bak", "s/a/b/", "file[0-9].go"}},
		{name: "brace expansion", args: []string{"-i.bak", "s/a/b/", "{a,b}.go"}},
		{name: "command substitution", args: []string{"-i.bak", "s/a/b/", "$(git ls-files)"}},
		{name: "backtick substitution", args: []string{"-i.bak", "s/a/b/", "`git ls-files`"}},
		{name: "one bad operand poisons the set", args: []string{"-i.bak", "s/a/b/", "ok.go", "*.go"}},

		// No file at all: sed reads a stream, and what it edits is upstream's business.
		{name: "script only, files come from a pipe", args: []string{"-i.bak", "s/a/b/"}},
		{name: "no arguments", args: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, bounded := SedFiles(tc.args)
			assert.Equal(t, tc.bounded, bounded, "bounded")
			if tc.bounded {
				assert.Equal(t, tc.files, files, "files")
			}
		})
	}
}

// TestDrivenCommandAcrossDriverFamilies grades the parser that finds the command a driver
// runs. It is the one that was missing, and its absence is why a driven rewrite read as
// harmless: the invocation magus parsed was the DRIVER, whose own operands say nothing
// about what the child will do.
func TestDrivenCommandAcrossDriverFamilies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cmd  Invocation
		want Invocation
		ok   bool
	}{
		// xargs: everything after its own flags is the child's argv, and the child's flags
		// must survive. Stripping them is the bug this test exists for: `xargs sed -i`
		// came back as sed with no -i, so an in-place rewrite graded as a read.
		{name: "plain xargs keeps the child's flags",
			cmd:  Invocation{Name: "xargs", Args: []string{"sed", "-i", "", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i", "", "s/a/b/"}}, ok: true},
		{name: "GNU -r before the command",
			cmd:  Invocation{Name: "xargs", Args: []string{"-r", "sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
		{name: "GNU --null long flag",
			cmd:  Invocation{Name: "xargs", Args: []string{"--null", "sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
		{name: "-n consumes its count",
			cmd:  Invocation{Name: "xargs", Args: []string{"-n", "1", "sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
		{name: "-P consumes its parallelism",
			cmd:  Invocation{Name: "xargs", Args: []string{"-P", "4", "rm", "-rf"}},
			want: Invocation{Name: "rm", Args: []string{"-rf"}}, ok: true},
		{name: "-I consumes its replace string",
			cmd:  Invocation{Name: "xargs", Args: []string{"-I", "{}", "sed", "-i.bak", "s/a/b/", "{}"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "{}"}}, ok: true},
		{name: "-0 is boolean and consumes nothing",
			cmd:  Invocation{Name: "xargs", Args: []string{"-0", "sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
		{name: "several flags before the command",
			cmd:  Invocation{Name: "xargs", Args: []string{"-0", "-r", "-n", "1", "-P", "8", "sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
		{name: "bare xargs runs echo and writes nothing",
			cmd: Invocation{Name: "xargs", Args: nil}},
		{name: "flags but no command",
			cmd: Invocation{Name: "xargs", Args: []string{"-r", "-0"}}},

		// find: the command runs between -exec and its terminator. Anything past the
		// terminator is another predicate, and swallowing it would attribute flags to the
		// child that find never passes it.
		{name: "-exec with semicolon terminator",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "*.go", "-exec", "sed", "-i.bak", "s/a/b/", "{}", ";"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "{}"}}, ok: true},
		{name: "-exec with plus terminator",
			cmd:  Invocation{Name: "find", Args: []string{".", "-exec", "sed", "-i.bak", "s/a/b/", "{}", "+"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "{}"}}, ok: true},
		{name: "escaped semicolon, as a shell line carries it",
			cmd:  Invocation{Name: "find", Args: []string{".", "-exec", "sed", "-i.bak", "s/a/b/", "{}", `\;`}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "{}"}}, ok: true},
		{name: "-execdir is the same shape",
			cmd:  Invocation{Name: "find", Args: []string{".", "-execdir", "rm", "{}", ";"}},
			want: Invocation{Name: "rm", Args: []string{"{}"}}, ok: true},
		{name: "-ok prompts but still runs the command",
			cmd:  Invocation{Name: "find", Args: []string{".", "-ok", "rm", "{}", ";"}},
			want: Invocation{Name: "rm", Args: []string{"{}"}}, ok: true},
		{name: "predicates AFTER the terminator are not the child's argv",
			cmd:  Invocation{Name: "find", Args: []string{".", "-exec", "sed", "-i.bak", "s/a/b/", "{}", ";", "-print"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "{}"}}, ok: true},
		{name: "find with no -exec drives nothing",
			cmd: Invocation{Name: "find", Args: []string{".", "-name", "*.go", "-print"}}},
		{name: "-exec with nothing after it",
			cmd: Invocation{Name: "find", Args: []string{".", "-exec"}}},

		// Everything else drives nothing, including tools that merely read a file list.
		{name: "grep is not a driver", cmd: Invocation{Name: "grep", Args: []string{"-r", "foo", "."}}},
		{name: "an absolute path still resolves by base name",
			cmd:  Invocation{Name: "/usr/bin/xargs", Args: []string{"sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := DrivenCommand(tc.cmd)
			assert.Equal(t, tc.ok, ok, "found a driven command")
			if tc.ok {
				assert.Equal(t, tc.want.Name, got.Name, "driven command name")
				assert.Equal(t, tc.want.Args, got.Args, "driven command args")
			}
		})
	}
}
