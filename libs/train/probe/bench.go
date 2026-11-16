package probe

import (
	"encoding/json"
	"fmt"
)

// BenchResult is one llama-bench test: pp<N> when Gen is 0, tg<N> when Prompt is 0.
type BenchResult struct {
	Test      string  `json:"test"`
	Prompt    int     `json:"n_prompt"`
	Gen       int     `json:"n_gen"`
	AvgTS     float64 `json:"avg_ts"`
	StddevTS  float64 `json:"stddev_ts"`
	Backends  string  `json:"backends"`
	GPULayers int     `json:"n_gpu_layers"`
	Build     string  `json:"build_commit"`
}

// ParseLlamaBench reads `llama-bench -o json` output and names each test pp<n_prompt> or
// tg<n_gen>. An empty array is an error.
func ParseLlamaBench(out []byte) ([]BenchResult, error) {
	var rs []BenchResult
	if err := json.Unmarshal(out, &rs); err != nil {
		return nil, fmt.Errorf("llama-bench -o json: %w", err)
	}
	if len(rs) == 0 {
		return nil, fmt.Errorf("llama-bench -o json printed no tests")
	}
	for i := range rs {
		switch {
		case rs[i].Gen == 0:
			rs[i].Test = fmt.Sprintf("pp%d", rs[i].Prompt)
		case rs[i].Prompt == 0:
			rs[i].Test = fmt.Sprintf("tg%d", rs[i].Gen)
		default:
			rs[i].Test = fmt.Sprintf("pp%d+tg%d", rs[i].Prompt, rs[i].Gen)
		}
	}
	return rs, nil
}

// BenchRow is the runs.jsonl line of one llama-bench run over one GGUF.
type BenchRow struct {
	Kind    string            `json:"kind"`
	Model   string            `json:"model"`
	Inputs  map[string]string `json:"inputs_sha256"`
	Results []BenchResult     `json:"results"`
	Usage
	WallSeconds float64 `json:"wall_seconds"`
	Load1Start  float64 `json:"load1_start"`
	Load1End    float64 `json:"load1_end"`
}
