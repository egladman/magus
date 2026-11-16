package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/train/completion/fim"
	"github.com/egladman/magus/libs/train/dedup"
)

type EvalSuite struct{ suite.Suite }

func TestEvalSuite(t *testing.T) { suite.Run(t, new(EvalSuite)) }

func (s *EvalSuite) TestDecontaminatorDropsRepeatedWindows() {
	evalText := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron"
	d := NewDecontaminator([]string{evalText})
	s.Equal("exact", d.Match(evalText))
	s.Equal("ngram", d.Match("prefix words here "+evalText+" and a tail"))
	s.Equal("", d.Match("one two three four five six seven eight nine ten eleven twelve thirteen fourteen"))
	s.Equal("", d.Match(""))
}

func (s *EvalSuite) TestDecontaminatorIgnoresBoilerplateGrams() {
	var texts []string
	for i := range Common + 2 {
		texts = append(texts, "if err != nil { return err } // shared boilerplate line number "+strings.Repeat("x", i))
	}
	d := NewDecontaminator(texts)
	s.Equal("", d.Match("a b c if err != nil { return err } // shared boilerplate line number zz more"))
}

func (s *EvalSuite) TestDevTailStartReservesTheLastFifthOfTheLines() {
	text := strings.Repeat("line\n", 10)
	s.Equal(len("line\n")*8, DevTailStart(text))
	s.Equal(0, DevTailStart(""))
}

func (s *EvalSuite) TestTextIsTheMiddle() {
	s.Equal("MID", Text(fim.Sample{Prefix: "a\nb", Middle: "MID", Suffix: "f\ng"}))
}

func (s *EvalSuite) TestFileSpansAreDeterministicFimOnlyAndDistinct() {
	var b strings.Builder
	b.WriteString("package p\n\n")
	for i := range 40 {
		b.WriteString("func F" + strings.Repeat("a", i) + "(x int) int {\n\ty := x * 2\n\treturn y + 1\n}\n\n")
	}
	f := fim.File{Path: "p.go", Blob: "b", Language: dedup.Go, Text: b.String()}
	one := FileSpans(f, fim.Limit{})
	s.Equal(one, FileSpans(f, fim.Limit{}))
	s.GreaterOrEqual(len(one), 10)
	seen := map[[2]int]bool{}
	for _, sm := range one {
		s.NotEqual(fim.KindLM, sm.Kind)
		s.False(seen[[2]int{sm.Start, sm.End}])
		seen[[2]int{sm.Start, sm.End}] = true
	}
	tail := FileSpans(f, fim.Limit{From: len(f.Text) * 8 / 10})
	for _, sm := range tail {
		s.GreaterOrEqual(sm.Start, len(f.Text)*8/10)
	}
}

const taskSrc = `import "std";

fun scale(n: int) > int {
    final twice = n * 2;
    return twice;
}

fun unused(n: int) > int {
    final nothing = n + 100;
    return nothing;
}

test "scale scales" {
    std\assert(scale(2) == 4, message: "scale");
}
`

func (s *EvalSuite) TestFileTasksNeedATestThatCallsTheFunction() {
	tasks := FileTasks(fim.File{Path: "a.buzz", Blob: "b", Language: dedup.Buzz, Text: taskSrc}, "dir:.")
	s.Require().Len(tasks, 1)
	t := tasks[0]
	s.Equal("scale", t.Name)
	s.Equal([]string{"scale scales"}, t.Tests)
	s.Contains(t.Middle, "final twice = n * 2;")
	s.True(strings.HasSuffix(t.Prefix, "{"))
	s.True(strings.HasPrefix(t.Suffix, "}"))
	s.Equal(taskSrc, t.Prefix+t.Middle+t.Suffix)
	s.Empty(FileTasks(fim.File{Path: "b.buzz", Language: dedup.Buzz, Text: "fun ("}, "g"))
}

func (s *EvalSuite) TestPinReplacesByNameAndSorts() {
	path := filepath.Join(s.T().TempDir(), "gen", "split.digest")
	s.Require().NoError(Pin(path, Digest{Name: "b", SHA256: "1", Rows: 1, Rev: "r"}))
	s.Require().NoError(Pin(path, Digest{Name: "a", SHA256: "2", Rows: 2, Rev: "r"}))
	s.Require().NoError(Pin(path, Digest{Name: "b", SHA256: "3", Rows: 3, Rev: "r"}))
	got, err := ReadDigests(path)
	s.Require().NoError(err)
	s.Equal([]Digest{{"a", "2", 2, "r"}, {"b", "3", 3, "r"}}, got)
}

func (s *EvalSuite) TestReadSplitRefusesADevTestFile() {
	path := filepath.Join(s.T().TempDir(), "split.jsonl")
	s.Require().NoError(os.WriteFile(path, []byte(`{"path":"a.go","language":"go","bytes":1,"group":"g","side":"test","dev":true}`+"\n"), 0o644))
	_, err := ReadSplit(path)
	s.Error(err)
}

func (s *EvalSuite) TestReadCutoffTakesTheCutoffRow() {
	path := filepath.Join(s.T().TempDir(), "inputs.jsonl")
	sha := strings.Repeat("a", 40)
	s.Require().NoError(os.WriteFile(path, []byte(`{"name":"x/y","revision":"no"}`+"\n"+`{"name":"cutoff/first","revision":"`+sha+`","date":"d","status":"proposed"}`+"\n"), 0o644))
	c, err := ReadCutoff(path)
	s.Require().NoError(err)
	s.Equal(Cutoff{Commit: sha, Date: "d", Status: "proposed"}, c)
}

func (s *EvalSuite) git(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, string(out))
	return strings.TrimSpace(string(out))
}

func (s *EvalSuite) TestBlobsAndTemporalReadAGitHistory() {
	dir := s.T().TempDir()
	s.git(dir, "init", "-q")
	write := func(name, body string) {
		s.Require().NoError(os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	write("a.go", "package a\n")
	s.git(dir, "add", ".")
	s.git(dir, "commit", "-qm", "one")
	cutoff := s.git(dir, "rev-parse", "HEAD")
	write("a.go", "package a\n\nvar X = 1\n")
	write("notes.md", "x\n")
	s.git(dir, "add", ".")
	s.git(dir, "commit", "-qm", "two")

	blobs, err := OpenBlobs(dir, "HEAD")
	s.Require().NoError(err)
	sha, data, err := blobs.Read("a.go")
	s.Require().NoError(err)
	s.Equal("package a\n\nvar X = 1\n", string(data))
	s.Equal(gitBlobSHA(data), sha)
	_, _, err = blobs.Read("missing.go")
	s.Error(err)
	s.NoError(blobs.Close())

	changes, commits, err := Temporal(dir, cutoff, "HEAD", hasLanguage)
	s.Require().NoError(err)
	s.Equal(1, commits)
	s.Require().Len(changes, 1)
	s.Equal("a.go", changes[0].Path)
	s.Equal(2, changes[0].Added)
}

func (s *EvalSuite) TestFailureReasonBucketsByCode() {
	s.Equal("BZZ1005", failureReason("[BZZ1005] buzz: x.buzz:1:1: cannot assign"))
	s.Equal("other", failureReason("buzz: x.buzz:3:1: if statement is not allowed"))
}

const scrubSrc = `package p

func Keep(a int) int {
	return a + 1
}

func Leak(a, b int) int {
	first := a * b * 1001 + 7 * a + b * 3 - 12
	second := first * first * 2002 + 9 * first
	return second
}
`

func (s *EvalSuite) TestScrubRemovesOnlyTheConstructOverlappingAnEvalText() {
	d := NewDecontaminator([]string{"first := a * b * 1001 + 7 * a + b * 3 - 12 second := first * first * 2002 + 9 * first return second"})
	hits := d.Hits(scrubSrc)
	s.NotEmpty(hits)
	got := Scrub(d, dedup.Go, scrubSrc)
	s.Contains(got.Text, "func Keep")
	s.NotContains(got.Text, "1001")
	s.Positive(got.Removed)
	s.Equal(len(scrubSrc)-len(got.Text), got.Removed)
	s.Empty(d.Hits(got.Text))
	s.True(got.Reparsed)
}

func (s *EvalSuite) TestScrubLeavesACleanFileAlone() {
	d := NewDecontaminator([]string{"nothing here matches the source at all not a single window of it"})
	got := Scrub(d, dedup.Go, scrubSrc)
	s.Equal(scrubSrc, got.Text)
	s.Zero(got.Removed)
}

func (s *EvalSuite) TestScrubRemovesAFileEqualToAnEvalText() {
	d := NewDecontaminator([]string{scrubSrc})
	s.Empty(Scrub(d, dedup.Go, scrubSrc).Text)
}

func (s *EvalSuite) TestCountChecksSetsProjectImportsAside() {
	rows := []CheckRow{
		{Path: "a", OKAtRev: true},
		{Path: "b", OKAtRev: true},
		{Path: "c", ReasonAtRev: `x.buzz:0:0: [BZZ2001] buzz: import "magus/spell/go": module not found`},
		{Path: "d", ReasonAtRev: `x.buzz:0:0: [BZZ2001] buzz: import "project/../..": module not found`},
		{Path: "e", ReasonAtRev: "[BZZ1005] x.buzz:1:1: cannot assign"},
	}
	got := CountChecks(rows)
	s.Equal(2, got.Pass)
	s.Equal(4, got.Counted)
	s.Equal(1, got.ProjectImport)
}

func (s *EvalSuite) TestG1ThresholdsAreElisRetry3Bars() {
	s.Equal(2000, MinFIMBuzzSpans)
	s.Equal(30, MinFIMBuzzGroups)
	s.Equal(120, MinPassKTasks)
	s.InDelta(0.95, MinCheckPass, 1e-9)
}
