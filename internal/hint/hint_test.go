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
)

// identPair is the two-suggestion shape every bare-identifier pattern produces;
// scope is the trailing project filter, empty when nothing scopes.
func identPair(pat, scope string, refsConf Confidence) []Suggestion {
	return []Suggestion{
		{Run: "magus refs " + pat, Why: whyRefs, Confidence: refsConf, Hedge: hedgeRefs},
		{Run: "magus query " + pat + scope, Why: whyEntity, Confidence: ConfidenceLow, Hedge: hedgeQuery},
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
		{name: "single-file grep abstains",
			cmd: Invocation{Name: "grep", Args: []string{"pat", "onefile.txt"}}},
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
		{name: "recursive grep over files only abstains",
			cmd: Invocation{Name: "grep", Args: []string{"-r", "pat", "a.go", "b.go"}}},
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
			want: identPair("symbolName", "", ConfidenceMedium)},
		{name: "multiple -e joins into one alternation query",
			cmd:  Invocation{Name: "rg", Args: []string{"-e", "alpha", "-e", "beta"}},
			want: []Suggestion{{`magus query "id=~alpha|beta"`, whyAlternation, ConfidenceLow, hedgeQuery}}},
		{name: "grep -G is boolean and does not eat the pattern",
			cmd:  Invocation{Name: "grep", Args: []string{"-rG", "MyFunc", "src/"}},
			want: identPair("MyFunc", "", ConfidenceMedium)},
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
			want: identPair("Foo", ` 'project=~^internal/cache(/|$)'`, ConfidenceMedium)},
		// The anchored regex is what keeps the suggestion from being narrower than
		// the grep: project=docs would exclude the nested project's nodes outright.
		{name: "scoping a project with a nested one still covers the nest",
			tr:   nested,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs/"}},
			want: identPair("Foo", ` 'project=~^docs(/|$)'`, ConfidenceMedium)},
		{name: "operands in two projects abstain from scoping",
			tr:   nested,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs", "libs/gopherbuzz"}},
			want: identPair("Foo", "", ConfidenceMedium)},
		{name: "two operands in one project still scope",
			tr:   nested,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs/site", "docs/tour"}},
			want: identPair("Foo", ` 'project=~^docs(/|$)'`, ConfidenceMedium)},
		// A string prefix is not a path prefix, which is why scope compares
		// against proj+"/" rather than the bare name.
		{name: "a sibling sharing a name prefix does not scope",
			tr:   nested,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs-old/"}},
			want: identPair("Foo", "", ConfidenceMedium)},
		{name: "root project never scopes: project=. says nothing",
			tr:   rootScoped,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "."}},
			want: identPair("Foo", "", ConfidenceMedium)},
		{name: "root project in the list still lets a sibling scope",
			tr:   rootScoped,
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs/"}},
			want: identPair("Foo", ` 'project=~^docs(/|$)'`, ConfidenceMedium)},
		{name: "no WithProjects means no project= anywhere",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "Foo", "internal/cache/"}},
			want: identPair("Foo", "", ConfidenceMedium)},
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

// TestCorpusDistribution pins the aggregate behavior over a realistic command
// corpus. The exact numbers are a snapshot: a change that starts
// over-suggesting (or silently stops abstaining) moves them and must be seen
// and re-justified here, not discovered in advisory noise later.
func TestCorpusDistribution(t *testing.T) {
	f, err := os.Open("testdata/corpus.txt")
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
		// The corpus is controlled - no spaces inside an argument - so a
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
	assert.Equal(t, 33, suggesting)
	assert.Equal(t, 23, abstaining)
}
