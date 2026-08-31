package hint

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runConf is the golden surface: Run and Confidence, asserted whole-slice so
// an extra or missing suggestion fails, not just a wrong one.
type runConf struct {
	Run        string
	Confidence Confidence
}

func TestSuggestGolden(t *testing.T) {
	scoped := NewGraph(WithProjects([]string{"internal/cache", "docs"}))
	rootScoped := NewGraph(WithProjects([]string{".", "docs"}))
	cases := []struct {
		name  string
		graph *Graph
		cmd   Invocation
		want  []runConf
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
			want: []runConf{{`magus query 'say "hi"'`, Low}}},
		{name: "backtick in pattern cannot execute on paste",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "foo`whoami`", "."}},
			want: []runConf{{"magus query 'foo`whoami`'", Low}}},
		{name: "command substitution in pattern is inert",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "$(cmd)", "."}},
			want: []runConf{{`magus query 'id=~$(cmd)'`, Low}}},
		{name: "backslash regex reaches magus intact",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", `out\d+`, "."}},
			want: []runConf{{`magus query 'id=~out\d+'`, Low}}},

		{name: "bare identifier routes refs then query",
			cmd: Invocation{Name: "grep", Args: []string{"-rn", "funcName", "."}},
			want: []runConf{
				{"magus refs funcName", Medium},
				{"magus query funcName", Low},
			}},
		{name: "word boundary raises refs to High",
			cmd: Invocation{Name: "grep", Args: []string{"-rnw", "Identifier", "."}},
			want: []runConf{
				{"magus refs Identifier", High},
				{"magus query Identifier", Low},
			}},
		{name: "phrase routes to quoted query",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "go test", "docs/"}},
			want: []runConf{{`magus query "go test"`, Low}}},
		{name: "diagnostic code routes to query",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "MGS2011", "docs/"}},
			want: []runConf{{"magus query MGS2011", High}}},
		{name: "buzz op routes to query",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "mgs_listManifests", "spells/"}},
			want: []runConf{{"magus query mgs_listManifests", High}}},
		{name: "regex pattern routes to id matcher",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", `guard.*Re`, "."}},
			want: []runConf{{`magus query "id=~guard.*Re"`, Low}}},
		{name: "fixed-strings never emits id matcher",
			cmd:  Invocation{Name: "grep", Args: []string{"-rnF", "a.b", "."}},
			want: []runConf{{`magus query "a.b"`, Low}}},
		{name: "rg is repo-wide by default",
			cmd: Invocation{Name: "rg", Args: []string{"symbolName"}},
			want: []runConf{
				{"magus refs symbolName", Medium},
				{"magus query symbolName", Low},
			}},
		{name: "rg with file operand keeps suggestion unscoped",
			graph: scoped,
			cmd:   Invocation{Name: "rg", Args: []string{"symbolName", "internal/cache/keys.go"}},
			want: []runConf{
				{"magus refs symbolName", Medium},
				{"magus query symbolName", Low},
			}},
		{name: "multiple -e joins into one alternation query",
			cmd:  Invocation{Name: "rg", Args: []string{"-e", "alpha", "-e", "beta"}},
			want: []runConf{{`magus query "id=~alpha|beta"`, Low}}},
		{name: "grep -G is boolean and does not eat the pattern",
			cmd: Invocation{Name: "grep", Args: []string{"-rG", "MyFunc", "src/"}},
			want: []runConf{
				{"magus refs MyFunc", Medium},
				{"magus query MyFunc", Low},
			}},
		{name: "ag -t is boolean and does not eat the pattern",
			cmd: Invocation{Name: "ag", Args: []string{"-t", "someSymbol"}},
			want: []runConf{
				{"magus refs someSymbol", Medium},
				{"magus query someSymbol", Low},
			}},
		{name: "--color takes a value and is not the pattern",
			cmd: Invocation{Name: "rg", Args: []string{"--color", "never", "someSymbol"}},
			want: []runConf{
				{"magus refs someSymbol", Medium},
				{"magus query someSymbol", Low},
			}},
		{name: "markdown operand routes to docsection",
			cmd:  Invocation{Name: "grep", Args: []string{"-rn", "checkpoint", "docs/guide.md"}},
			want: []runConf{{`magus query kind=docsection "checkpoint"`, Medium}}},

		{name: "find -name glob converts to anchored regex",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "*.go"}},
			want: []runConf{{`magus query kind=file 'id=~\.go$'`, High}}},
		{name: "find with -exec still suggests from -name",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "*.buzz", "-exec", "wc", "-l", "{}", ";"}},
			want: []runConf{{`magus query kind=file 'id=~\.buzz$'`, High}}},
		{name: "find unconvertible glob falls back to bare file query",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "[ab]*.go"}},
			want: []runConf{{"magus query kind=file", Low}}},
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
			want: []runConf{{`magus query kind=file 'id=~(?i)\.md$'`, High}}},
		{name: "? does not cross a separator",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "cmd?.go"}},
			want: []runConf{{`magus query kind=file 'id=~cmd[^/]\.go$'`, High}}},
		{name: "-name ? is too open to translate",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "?"}},
			want: []runConf{{"magus query kind=file", Low}}},
		{name: "fd extension converts like a glob",
			cmd:  Invocation{Name: "fd", Args: []string{"-e", "go"}},
			want: []runConf{{`magus query kind=file 'id=~\.go$'`, High}}},
		{name: "fd -e keeps the pattern operand",
			cmd:  Invocation{Name: "fd", Args: []string{"-e", "go", "parse"}},
			want: []runConf{{`magus query kind=file id=~parse 'id=~\.go$'`, Medium}}},
		{name: "fd pattern passes through as a regex",
			cmd:  Invocation{Name: "fd", Args: []string{"guard_", "cmd/magus"}},
			want: []runConf{{"magus query kind=file id=~guard_", Medium}}},

		{name: "project scoping lands on query suggestions only",
			graph: scoped,
			cmd:   Invocation{Name: "grep", Args: []string{"-rn", "Foo", "internal/cache/"}},
			want: []runConf{
				{"magus refs Foo", Medium},
				{"magus query Foo project=internal/cache", Low},
			}},
		{name: "root project never scopes: project=. says nothing",
			graph: rootScoped,
			cmd:   Invocation{Name: "grep", Args: []string{"-rn", "Foo", "."}},
			want: []runConf{
				{"magus refs Foo", Medium},
				{"magus query Foo", Low},
			}},
		{name: "root project in the list still lets a sibling scope",
			graph: rootScoped,
			cmd:   Invocation{Name: "grep", Args: []string{"-rn", "Foo", "docs/"}},
			want: []runConf{
				{"magus refs Foo", Medium},
				{"magus query Foo project=docs", Low},
			}},
		{name: "no WithProjects means no project= anywhere",
			cmd: Invocation{Name: "grep", Args: []string{"-rn", "Foo", "internal/cache/"}},
			want: []runConf{
				{"magus refs Foo", Medium},
				{"magus query Foo", Low},
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.graph
			if g == nil {
				g = NewGraph()
			}
			var got []runConf
			for _, s := range g.Suggest(tc.cmd) {
				got = append(got, runConf{s.Run, s.Confidence})
			}
			assert.Equal(t, tc.want, got)
			for _, s := range g.Suggest(tc.cmd) {
				assert.NotEmpty(t, s.Why)
				assert.NotEmpty(t, s.Hedge)
			}
		})
	}
}

func TestSuggestCaseInsensitiveHedge(t *testing.T) {
	g := NewGraph()
	got := g.Suggest(Invocation{Name: "grep", Args: []string{"-rni", "todo", "."}})
	require.NotEmpty(t, got)
	for _, s := range got {
		assert.Contains(t, s.Hedge, "case")
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
		{"find", Invocation{Name: "find", Args: []string{".", "-name", "*.go"}}, ClassFileFind},
		{"find without -name is still a file find", Invocation{Name: "find", Args: []string{".", "-type", "d"}}, ClassFileFind},
		{"fd", Invocation{Name: "fd", Args: []string{"-e", "go"}}, ClassFileFind},
		{"cat", Invocation{Name: "cat", Args: []string{"go.mod"}}, ClassRead},
		{"head", Invocation{Name: "head", Args: []string{"-50", "main.go"}}, ClassRead},
		{"sed address-print", Invocation{Name: "sed", Args: []string{"-n", "10,20p", "f.buzz"}}, ClassRead},
		{"sed substitution", Invocation{Name: "sed", Args: []string{"s/a/b/", "f"}}, ClassTransform},
		{"sed -n with -i still transforms", Invocation{Name: "sed", Args: []string{"-n", "-i", "s/a/b/p", "f"}}, ClassTransform},
		{"awk", Invocation{Name: "awk", Args: []string{"{print $1}"}}, ClassTransform},
		{"sd", Invocation{Name: "sd", Args: []string{"a", "b", "."}}, ClassTransform},
		{"ls", Invocation{Name: "ls", Args: []string{"-la"}}, ClassNone},
		{"unrecognized", Invocation{Name: "frobnicate"}, ClassNone},
	}
	g := NewGraph()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, g.Classify(tc.cmd))
		})
	}
}

func TestPatterns(t *testing.T) {
	g := NewGraph()
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
		{"non-search command", Invocation{Name: "cat", Args: []string{"x.md"}}, nil},
		{"find is not a search command", Invocation{Name: "find", Args: []string{".", "-name", "*.go"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, g.Patterns(tc.cmd))
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

	g := NewGraph()
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
		counts[g.Classify(cmd)]++
		if len(g.Suggest(cmd)) > 0 {
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
