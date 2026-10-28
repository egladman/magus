package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/benchmarks/agent/internal/bench"
	"github.com/egladman/magus/internal/json"
)

const (
	multilingualRows = "https://datasets-server.huggingface.co/rows?dataset=SWE-bench%2FSWE-bench_Multilingual&config=default&split=test"
	multilingualCard = "https://huggingface.co/api/datasets/SWE-bench/SWE-bench_Multilingual"
	rowsPerPage      = 100
	// multilingualPin is sha256 over the rows API's page bodies, offset 0, 100 and 200
	// concatenated as served, so `curl ... | shasum -a 256` reproduces it. A fetch that
	// disagrees means upstream changed the table, and is refused until the pin moves.
	multilingualPin = "sha256:ef6c1d6baa6250c063b8778a4e5d27af87f567fc4cb80e29ffcb118b0d8b910b"
)

// goRepos are the Go repositories in SWE-bench Multilingual, with the number of rows each
// contributes, as the dataset card lists them.
var goRepos = map[string]int{
	"caddyserver/caddy":     14,
	"gin-gonic/gin":         8,
	"prometheus/prometheus": 8,
	"gohugoio/hugo":         7,
	"hashicorp/terraform":   5,
}

type getter interface {
	get(ctx context.Context, url string) ([]byte, error)
}

type httpGetter struct{}

// get retries a transport error or a 5xx five times, two seconds apart, as the rows API
// sheds load that way; any other status fails at once.
func (httpGetter) get(ctx context.Context, url string) ([]byte, error) {
	client := &http.Client{Timeout: time.Minute}
	var last error
	for range 5 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			switch {
			case readErr != nil:
				err = readErr
			case resp.StatusCode == http.StatusOK:
				return body, nil
			case resp.StatusCode < 500:
				return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
			default:
				err = fmt.Errorf("GET %s: %s", url, resp.Status)
			}
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, last
}

type rowsPage struct {
	NumRowsTotal int  `json:"num_rows_total"`
	Partial      bool `json:"partial"`
	Rows         []struct {
		Row            json.RawMessage `json:"row"`
		TruncatedCells []string        `json:"truncated_cells"`
	} `json:"rows"`
}

type multilingualRow struct {
	InstanceID       string   `json:"instance_id"`
	Repo             string   `json:"repo"`
	BaseCommit       string   `json:"base_commit"`
	CreatedAt        string   `json:"created_at"`
	ProblemStatement string   `json:"problem_statement"`
	Patch            string   `json:"patch"`
	TestPatch        string   `json:"test_patch"`
	FailToPass       []string `json:"FAIL_TO_PASS"`
	PassToPass       []string `json:"PASS_TO_PASS"`
	Image            string   `json:"image"`
	EvalScript       string   `json:"eval_script"`
	LogParser        string   `json:"log_parser"`
	Version          string   `json:"version"`
}

func writeSWEBench(ctx context.Context, g getter, out string) error {
	if err := checkCardLicense(ctx, g); err != nil {
		return err
	}
	raw, sum, err := fetchGoRows(ctx, g)
	if err != nil {
		return err
	}
	if sum != multilingualPin {
		return fmt.Errorf("the table digests to %s, not the pinned %s: upstream changed it; review the change, then move multilingualPin", sum, multilingualPin)
	}
	tasks := make([]bench.Task, 0, len(raw))
	for _, r := range raw {
		t, err := multilingualTask(r)
		if err != nil {
			return err
		}
		tasks = append(tasks, t)
	}
	return writeAndReload(out, tasks)
}

// writeAndReload writes tasks and reads them back as the runner will, so a file the runner
// would refuse is never left behind as a success.
func writeAndReload(out string, tasks []bench.Task) error {
	if err := bench.WriteTasks(out, tasks); err != nil {
		return err
	}
	_, err := bench.LoadTasks(out)
	return err
}

// checkCardLicense refuses the dataset unless its card still declares MIT, the license the
// task records carry.
func checkCardLicense(ctx context.Context, g getter) error {
	body, err := g.get(ctx, multilingualCard)
	if err != nil {
		return err
	}
	var card struct {
		CardData struct {
			License json.RawMessage `json:"license"`
		} `json:"cardData"`
	}
	if err := json.Unmarshal(body, &card); err != nil {
		return fmt.Errorf("dataset card: %w", err)
	}
	var one string
	var many []string
	if json.Unmarshal(card.CardData.License, &one) == nil {
		many = []string{one}
	} else if err := json.Unmarshal(card.CardData.License, &many); err != nil {
		return fmt.Errorf("dataset card license %s: %w", card.CardData.License, err)
	}
	if !slices.Equal(many, []string{"mit"}) {
		return fmt.Errorf("dataset card declares license %v, not mit", many)
	}
	return nil
}

// fetchGoRows pages through the whole table and returns the Go rows' raw JSON in
// instance_id order and the digest of the pages, checking each repository contributes the
// count the card lists.
func fetchGoRows(ctx context.Context, g getter) ([]json.RawMessage, string, error) {
	type keyed struct {
		id  string
		raw json.RawMessage
	}
	var kept []keyed
	perRepo := map[string]int{}
	pages := sha256.New()
	total, seen := -1, 0
	for offset := 0; total < 0 || offset < total; offset += rowsPerPage {
		body, err := g.get(ctx, fmt.Sprintf("%s&offset=%d&length=%d", multilingualRows, offset, rowsPerPage))
		if err != nil {
			return nil, "", err
		}
		pages.Write(body)
		var page rowsPage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, "", fmt.Errorf("rows at offset %d: %w", offset, err)
		}
		if page.Partial || len(page.Rows) == 0 {
			return nil, "", fmt.Errorf("rows at offset %d: a partial or empty page before row %d of %d", offset, seen, page.NumRowsTotal)
		}
		total = page.NumRowsTotal
		for _, r := range page.Rows {
			seen++
			var head struct {
				InstanceID string `json:"instance_id"`
				Repo       string `json:"repo"`
			}
			if err := json.Unmarshal(r.Row, &head); err != nil {
				return nil, "", fmt.Errorf("row %d: %w", seen, err)
			}
			if _, ok := goRepos[head.Repo]; !ok {
				continue
			}
			if len(r.TruncatedCells) > 0 {
				return nil, "", fmt.Errorf("%s: the rows API truncated %v", head.InstanceID, r.TruncatedCells)
			}
			perRepo[head.Repo]++
			kept = append(kept, keyed{head.InstanceID, bytes.Clone(r.Row)})
		}
	}
	if seen != total {
		return nil, "", fmt.Errorf("read %d rows, the table says %d", seen, total)
	}
	for repo, want := range goRepos {
		if perRepo[repo] != want {
			return nil, "", fmt.Errorf("%s has %d rows, the dataset card lists %d", repo, perRepo[repo], want)
		}
	}
	slices.SortFunc(kept, func(a, b keyed) int { return strings.Compare(a.id, b.id) })
	raw := make([]json.RawMessage, len(kept))
	for i, k := range kept {
		raw[i] = k.raw
	}
	return raw, "sha256:" + hex.EncodeToString(pages.Sum(nil)), nil
}

func multilingualTask(raw json.RawMessage) (bench.Task, error) {
	var r multilingualRow
	if err := json.Unmarshal(raw, &r); err != nil {
		return bench.Task{}, err
	}
	created, err := time.Parse(time.DateTime, r.CreatedAt)
	if err != nil {
		return bench.Task{}, fmt.Errorf("%s: created_at: %w", r.InstanceID, err)
	}
	return bench.Task{
		Suite:      bench.SuiteSWEBenchMultilingualGo,
		ID:         r.InstanceID,
		Kind:       bench.TaskBugfix,
		Tag:        bench.TagCapability,
		Created:    created.UTC().Format(time.RFC3339),
		Repo:       r.Repo,
		License:    "MIT",
		BaseCommit: r.BaseCommit,
		Prompt:     r.ProblemStatement,
		Patch:      r.Patch,
		TestPatch:  r.TestPatch,
		FailToPass: r.FailToPass,
		PassToPass: r.PassToPass,
		SWEBench: &bench.SWEBenchGrading{
			Image:      r.Image,
			EvalScript: r.EvalScript,
			LogParser:  r.LogParser,
			Version:    r.Version,
		},
	}, nil
}
