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

type swebenchSuite struct {
	suite.Suite
}

func TestSWEBenchSuite(t *testing.T) {
	suite.Run(t, new(swebenchSuite))
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

func (s *swebenchSuite) TestFetchGoRowsKeepsTheGoRowsInInstanceOrder() {
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

func (s *swebenchSuite) TestFetchGoRowsRefusesARepositoryCountTheCardDoesNotList() {
	_, _, err := fetchGoRows(context.Background(), rowsTable("gin-gonic/gin"))
	s.ErrorContains(err, "gin-gonic/gin has 9 rows, the dataset card lists 8")
}

func (s *swebenchSuite) TestWriteSWEBenchRefusesRowsThatMissThePin() {
	out := filepath.Join(s.T().TempDir(), "swebench-multilingual-go.jsonl")
	err := writeSWEBench(context.Background(), rowsTable(""), out)
	s.ErrorContains(err, "not the pinned")
	s.NoFileExists(out)
}

func (s *swebenchSuite) TestCheckCardLicenseRefusesAnythingButMIT() {
	s.Require().NoError(checkCardLicense(context.Background(), fakeGetter{multilingualCard: `{"cardData":{"license":["mit"]}}`}))
	s.ErrorContains(checkCardLicense(context.Background(), fakeGetter{multilingualCard: `{"cardData":{"license":"apache-2.0"}}`}), "not mit")
}

func (s *swebenchSuite) TestMultilingualTaskCarriesTheDatasetsGrading() {
	raw, err := json.Marshal(row("gin-gonic__gin-1", "gin-gonic/gin"))
	s.Require().NoError(err)
	t, err := multilingualTask(raw)
	s.Require().NoError(err)
	s.Equal(bench.Task{
		Suite:      bench.SuiteSWEBenchMultilingualGo,
		ID:         "gin-gonic__gin-1",
		Kind:       bench.TaskBugfix,
		Tag:        bench.TagCapability,
		Created:    "2024-05-01T10:20:30Z",
		Repo:       "gin-gonic/gin",
		License:    "MIT",
		BaseCommit: "abc",
		Prompt:     "it breaks",
		Patch:      "diff a",
		TestPatch:  "diff b",
		FailToPass: []string{"TestA"},
		PassToPass: []string{},
		SWEBench: &bench.SWEBenchGrading{
			Image:      "swebench/x:latest",
			EvalScript: "#!/bin/bash\ngo test ./...",
			LogParser:  "parse_log_gotest",
			Version:    "1",
		},
	}, t)
	s.NoError(t.Seal())
}
