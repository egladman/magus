package benchtasks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/benchmarks/agent/internal/bench"
	"github.com/egladman/magus/internal/json"
)

type BenchtasksSuite struct {
	suite.Suite
}

func TestBenchtasksSuite(t *testing.T) {
	suite.Run(t, new(BenchtasksSuite))
}

// fakeGetter serves bodies by URL and fails on any URL it does not hold.
type fakeGetter map[string]string

func (f fakeGetter) get(_ context.Context, url string) ([]byte, error) {
	body, ok := f[url]
	if !ok {
		return nil, fmt.Errorf("no body for %s", url)
	}
	return []byte(body), nil
}

func row(id, repo string) map[string]any {
	return map[string]any{
		"instance_id": id, "repo": repo, "base_commit": "abc", "created_at": "2024-05-01 10:20:30",
		"problem_statement": "it breaks", "patch": "diff a", "test_patch": "diff b",
		"FAIL_TO_PASS": []string{"TestA"}, "PASS_TO_PASS": []string{}, "image": "swebench/x:latest",
		"eval_script": "#!/bin/bash\ngo test ./...", "log_parser": "parse_log_gotest", "version": "1",
		"hints_text": "",
	}
}

// rowsTable serves one page holding every Go row the card lists plus one row of another
// repository, with extra rows added to repo when it is not empty.
func rowsTable(extraRepo string) fakeGetter {
	var rows []map[string]any
	for _, repo := range slices.Sorted(maps.Keys(goRepos)) {
		n := goRepos[repo]
		if repo == extraRepo {
			n++
		}
		for i := range n {
			rows = append(rows, map[string]any{"row": row(fmt.Sprintf("%s-%02d", strings.ReplaceAll(repo, "/", "__"), i), repo), "truncated_cells": []string{}})
		}
	}
	rows = append(rows, map[string]any{"row": row("apache__druid-1", "apache/druid"), "truncated_cells": []string{}})
	page, err := json.Marshal(map[string]any{"num_rows_total": len(rows), "rows": rows})
	if err != nil {
		panic(err)
	}
	return fakeGetter{
		fmt.Sprintf("%s&offset=0&length=%d", multilingualRows, rowsPerPage): string(page),
		multilingualCard: `{"cardData":{"license":"mit"}}`,
	}
}

func (s *BenchtasksSuite) TestFetchGoRowsKeepsTheGoRowsInInstanceOrder() {
	table := rowsTable("")
	raw, sum, err := fetchGoRows(context.Background(), table)
	s.Require().NoError(err)
	s.Len(raw, 42)
	page := sha256.Sum256([]byte(table[fmt.Sprintf("%s&offset=0&length=%d", multilingualRows, rowsPerPage)]))
	s.Equal("sha256:"+hex.EncodeToString(page[:]), sum)
	var ids []string
	for _, r := range raw {
		var head struct {
			InstanceID string `json:"instance_id"`
		}
		s.Require().NoError(json.Unmarshal(r, &head))
		ids = append(ids, head.InstanceID)
	}
	s.True(slices.IsSorted(ids))
	s.NotContains(ids, "apache__druid-1")
}

func (s *BenchtasksSuite) TestFetchGoRowsRefusesARepositoryCountTheCardDoesNotList() {
	_, _, err := fetchGoRows(context.Background(), rowsTable("gin-gonic/gin"))
	s.ErrorContains(err, "gin-gonic/gin has 9 rows, the dataset card lists 8")
}

func (s *BenchtasksSuite) TestWriteSWEBenchRefusesRowsThatMissThePin() {
	out := filepath.Join(s.T().TempDir(), "swebench-multilingual-go.jsonl")
	err := writeSWEBench(context.Background(), rowsTable(""), out)
	s.ErrorContains(err, "not the pinned")
	s.NoFileExists(out)
}

func (s *BenchtasksSuite) TestCheckCardLicenseRefusesAnythingButMIT() {
	s.NoError(checkCardLicense(context.Background(), fakeGetter{multilingualCard: `{"cardData":{"license":["mit"]}}`}))
	s.ErrorContains(checkCardLicense(context.Background(), fakeGetter{multilingualCard: `{"cardData":{"license":"apache-2.0"}}`}), "not mit")
}

func (s *BenchtasksSuite) TestMultilingualTaskCarriesTheDatasetsGrading() {
	raw, err := json.Marshal(row("gin-gonic__gin-1", "gin-gonic/gin"))
	s.Require().NoError(err)
	t, err := multilingualTask(raw)
	s.Require().NoError(err)
	s.Equal("2024-05-01T10:20:30Z", t.Created)
	s.Equal(bench.TaskBugfix, t.Kind)
	s.Equal("parse_log_gotest", t.SWEBench.LogParser)
	s.Equal([]string{"TestA"}, t.FailToPass)
	s.NoError(t.Seal())
}

func (s *BenchtasksSuite) TestClassifyFindsABugfix() {
	c := classify([]string{"internal/cache/key.go", "internal/cache/key_test.go", "docs/x.md"}, nil)
	s.Equal(bench.TaskBugfix, c.kind)
	s.Equal([]string{"internal/cache/key.go"}, c.sources)
	s.Equal([]string{"internal/cache/key_test.go"}, c.tests)
}

func (s *BenchtasksSuite) TestClassifyFindsARegeneration() {
	c := classify([]string{"internal/config/config.go", "internal/config/gen/flags.go"}, nil)
	s.Equal(bench.TaskRegeneration, c.kind)
	s.Equal([]string{"internal/config/gen/flags.go"}, c.generated)
}

func (s *BenchtasksSuite) TestClassifyLeavesNestedModulesOutOfTheRootPackages() {
	c := classify([]string{"libs/xdg/xdg.go", "libs/xdg/xdg_test.go", "run.go"}, []string{"libs/xdg"})
	s.Equal(bench.TaskInterrogation, c.kind)
	s.Equal([]string{"run.go"}, c.sources)
	s.Empty(c.tests)
}

func (s *BenchtasksSuite) TestClassifyTreatsTestdataAsTests() {
	c := classify([]string{"a/b.go", "a/testdata/in.txt"}, nil)
	s.Equal(bench.TaskBugfix, c.kind)
	s.Equal([]string{"a/testdata/in.txt"}, c.tests)
}

func (s *BenchtasksSuite) TestClassifyRefusesAWideCommitAsABugfix() {
	var files []string
	for i := range maxBugfixFiles {
		files = append(files, fmt.Sprintf("p/f%d.go", i))
	}
	c := classify(append(files, "p/f_test.go"), nil)
	s.Equal(bench.TaskInterrogation, c.kind)
}

func (s *BenchtasksSuite) TestClassifyIgnoresADocsOnlyCommit() {
	s.Empty(classify([]string{"docs/a.md"}, nil).kind)
}

func (s *BenchtasksSuite) TestSlugCutsAtADash() {
	s.Equal("fix-the-cache-key-race-v2", slug("Fix the cache key race (v2)!"))
	long := slug("select the in-process Go indexer by the go-packages symbol format")
	s.LessOrEqual(len(long), 48)
	s.False(strings.HasSuffix(long, "-"))
}

func (s *BenchtasksSuite) TestPromptDropsTrailers() {
	s.Equal("fix it\n\nbody", prompt("fix it\n\nbody\nCo-Authored-By: x <y>\nSigned-off-by: z\n"))
}

func (s *BenchtasksSuite) TestTestEnvDropsSessionVariables() {
	s.Equal([]string{"PATH=/bin"}, testEnv([]string{"PATH=/bin", "MAGUS_JOB=x", "CLAUDECODE=1"}))
}

const suiteFile = `package p

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type KeySuite struct{ suite.Suite }

func TestKeySuite(t *testing.T) { suite.Run(t, new(KeySuite)) }

func (s *KeySuite) TestRace() { s.True(true) }

func (s *KeySuite) helper() {}

func TestPlain(t *testing.T) {}
`

func (s *BenchtasksSuite) TestTestFuncsNamesSuiteMethodsUnderTheirRunner() {
	funcs, err := testFuncs(map[string][]byte{"p/key_test.go": []byte(suiteFile)})
	s.Require().NoError(err)
	s.Equal([]string{"TestKeySuite", "TestKeySuite/TestRace", "TestPlain"}, slices.Sorted(maps.Keys(funcs)))
}

func (s *BenchtasksSuite) TestTestFuncsFollowsARunnerInAnotherFile() {
	funcs, err := testFuncs(map[string][]byte{
		"p/a_test.go": []byte("package p\nimport \"testing\"\nfunc TestRunner(t *testing.T) { suite.Run(t, &Other{}) }\n"),
		"p/b_test.go": []byte("package p\nfunc (o *Other) TestThing() {}\n"),
	})
	s.Require().NoError(err)
	s.Contains(funcs, "TestRunner/TestThing")
}

func (s *BenchtasksSuite) TestChangedTestsFindsNewAndEditedTests() {
	parent := map[string]string{"TestA": "a", "TestB": "b"}
	fix := map[string]string{"TestA": "a", "TestB": "b2", "TestS/TestC": "c"}
	s.Equal([]string{"TestB", "TestS/TestC"}, changedTests(parent, fix))
}

func (s *BenchtasksSuite) TestRunPatternSelectsTopLevelTestsOnce() {
	s.Equal("^(TestB|TestS)$", runPattern([]string{"TestS/TestC", "TestB", "TestS/TestD"}))
}

const testJSON = `# p [p.test]
p/x_test.go:3:2: undefined: Thing
{"Action":"run","Package":"p","Test":"TestS"}
{"Action":"run","Package":"p","Test":"TestS/TestC"}
{"Action":"fail","Package":"p","Test":"TestS/TestC"}
{"Action":"pass","Package":"p","Test":"TestB"}
{"Action":"fail","Package":"p","Test":"TestS"}
{"Action":"fail","Package":"p"}
{"Action":"pass","Package":"q"}
`

func (s *BenchtasksSuite) TestParseTestJSONKeepsOutcomesAndFailedPackages() {
	run, err := parseTestJSON(strings.NewReader(testJSON))
	s.Require().NoError(err)
	s.Equal(map[string]string{"TestS": "fail", "TestS/TestC": "fail", "TestB": "pass"}, run.outcomes)
	s.Equal([]string{"p"}, run.failedPackages)
}

func (s *BenchtasksSuite) TestFailToPassWantsAFailOrAbsenceAtTheParent() {
	parent := testRun{outcomes: map[string]string{"TestA": "fail", "TestB": "pass"}}
	fix := testRun{outcomes: map[string]string{"TestA": "pass", "TestB": "pass", "TestC": "pass", "TestD": "fail"}}
	s.Equal([]string{"TestA", "TestC"}, failToPass([]string{"TestA", "TestB", "TestC", "TestD"}, parent, fix))
}

func (s *BenchtasksSuite) TestDependentsDirectAndTransitive() {
	listed := []listedPackage{
		{ImportPath: "m/a", Imports: []string{"m/t"}, Deps: []string{"fmt", "m/t"}},
		{ImportPath: "m/b", Imports: []string{"m/a"}, Deps: []string{"m/a", "m/t"}},
		{ImportPath: "m/t", Deps: []string{"fmt"}},
	}
	s.Equal([]string{"m/a"}, dependents(listed, "m/t", false))
	s.Equal([]string{"m/a", "m/b"}, dependents(listed, "m/t", true))
}

func (s *BenchtasksSuite) TestHistoryNamesEveryMissingFlag() {
	s.ErrorContains(writeHistory(context.Background(), historyOptions{repo: "."}), "--after, --ref, -o required")
}
