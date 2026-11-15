package probe

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Chunks cuts body into pieces of at most maxBytes, each ending at a line end. A single
// line longer than maxBytes is its own piece; mlx-lm truncates it to max_seq_length.
func Chunks(body []byte, maxBytes int) [][]byte {
	var out [][]byte
	start, last := 0, 0
	for i, c := range body {
		if c != '\n' {
			continue
		}
		if i+1-start > maxBytes && last > start {
			out = append(out, body[start:last])
			start = last
		}
		last = i + 1
	}
	if len(body)-start > maxBytes && last > start {
		out = append(out, body[start:last])
		start = last
	}
	if start < len(body) {
		out = append(out, body[start:])
	}
	return out
}

// PilotData is what WriteData wrote: mlx-lm's train.jsonl and valid.jsonl in Dir.
type PilotData struct {
	Dir         string `json:"dir"`
	TrainSHA256 string `json:"train_sha256"`
	ValidSHA256 string `json:"valid_sha256"`
	Files       int    `json:"files"`
	Train       int    `json:"train_pieces"`
	Valid       int    `json:"valid_pieces"`
	Bytes       int64  `json:"bytes"`
}

// Pack cuts each text into Chunks and joins consecutive chunks, a blank line between
// files, into pieces of at most maxBytes, so a training sequence is near full length
// rather than one short file.
func Pack(texts []Text, maxBytes int) [][]byte {
	var out [][]byte
	var cur []byte
	for _, t := range texts {
		for _, c := range Chunks(t.Body, maxBytes) {
			if len(cur) > 0 && len(cur)+2+len(c) > maxBytes {
				out = append(out, cur)
				cur = nil
			}
			if len(cur) > 0 {
				cur = append(cur, "\n\n"...)
			}
			cur = append(cur, c...)
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// WriteData writes texts as mlx-lm {"text": ...} rows, packed by Pack into pieces of at
// most maxBytes, into dir/train.jsonl and dir/valid.jsonl. Every validEvery-th piece
// goes to valid.jsonl. Both files come from the texts given, so a caller that passes
// only train-split text keeps eval and holdout text out of the pilot's validation too.
func WriteData(dir string, texts []Text, maxBytes, validEvery int) (PilotData, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return PilotData{}, err
	}
	d := PilotData{Dir: dir, Files: len(texts)}
	var train, valid strings.Builder
	for i, c := range Pack(texts, maxBytes) {
		b, err := json.Marshal(map[string]string{"text": string(c)})
		if err != nil {
			return PilotData{}, err
		}
		d.Bytes += int64(len(c))
		if (i+1)%validEvery == 0 {
			valid.Write(append(b, '\n'))
			d.Valid++
		} else {
			train.Write(append(b, '\n'))
			d.Train++
		}
	}
	if d.Train == 0 || d.Valid == 0 {
		return PilotData{}, fmt.Errorf("%d texts make %d train and %d valid pieces, and mlx-lm needs both", len(texts), d.Train, d.Valid)
	}
	for name, body := range map[string]string{"train.jsonl": train.String(), "valid.jsonl": valid.String()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return PilotData{}, err
		}
	}
	var err error
	if d.TrainSHA256, err = FileSHA256(filepath.Join(dir, "train.jsonl")); err != nil {
		return PilotData{}, err
	}
	d.ValidSHA256, err = FileSHA256(filepath.Join(dir, "valid.jsonl"))
	return d, err
}

// Report is one mlx-lm training report line: the step, the mean loss and tokens/s over
// the window since the previous report, and the tokens trained so far.
type Report struct {
	Step          int
	Loss          float64
	TokensPerSec  float64
	TrainedTokens float64
}

// mlx-lm 0.32's cli_ui prints "  <step>    <loss> ▼    <tok/s>    <trained/1000>k", the
// arrow ▲ when the loss rose.
// ansiRE matches the SGR colour codes rich writes even into a pipe.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

var reportRE = regexp.MustCompile(`^\s*(\d+)\s+(\d+\.\d+)\s+[▼▲]\s+([\d,]+)\s+([\d.]+)k\s*$`)

// ParseReports reads the training report lines out of mlx_lm.lora output.
func ParseReports(out string) []Report {
	var rs []Report
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		m := reportRE.FindStringSubmatch(ansiRE.ReplaceAllString(sc.Text(), ""))
		if m == nil {
			continue
		}
		step, _ := strconv.Atoi(m[1])
		loss, _ := strconv.ParseFloat(m[2], 64)
		tps, _ := strconv.ParseFloat(strings.ReplaceAll(m[3], ",", ""), 64)
		k, _ := strconv.ParseFloat(m[4], 64)
		rs = append(rs, Report{Step: step, Loss: loss, TokensPerSec: tps, TrainedTokens: k * 1000})
	}
	return rs
}

// SteadyTokensPerSec is the tokens/s over every report window but the first, which
// carries graph compilation and warmup. It weights each window by its tokens, so it is
// total tokens over total time. It is 0 with fewer than two reports.
func SteadyTokensPerSec(rs []Report) float64 {
	var tokens, seconds float64
	for i := 1; i < len(rs); i++ {
		window := rs[i].TrainedTokens - rs[i-1].TrainedTokens
		if rs[i].TokensPerSec <= 0 || window <= 0 {
			continue
		}
		tokens += window
		seconds += window / rs[i].TokensPerSec
	}
	if seconds == 0 {
		return 0
	}
	return tokens / seconds
}

// Usage is what macOS /usr/bin/time -l reports about the process it ran.
type Usage struct {
	MaxRSSBytes        int64 `json:"max_rss_bytes"`
	PeakFootprintBytes int64 `json:"peak_footprint_bytes"`
}

var (
	maxRSSRE    = regexp.MustCompile(`(?m)^\s*(\d+)\s+maximum resident set size\s*$`)
	footprintRE = regexp.MustCompile(`(?m)^\s*(\d+)\s+peak memory footprint\s*$`)
)

// ParseUsage reads /usr/bin/time -l's maximum resident set size and peak memory
// footprint, both in bytes on macOS. The footprint counts the Metal buffers MLX and
// llama.cpp allocate in unified memory, which the resident set size can miss. A missing
// line is an error.
func ParseUsage(out string) (Usage, error) {
	var u Usage
	m := maxRSSRE.FindStringSubmatch(out)
	f := footprintRE.FindStringSubmatch(out)
	if m == nil || f == nil {
		return u, errors.New("output has no /usr/bin/time -l maximum resident set size and peak memory footprint lines")
	}
	u.MaxRSSBytes, _ = strconv.ParseInt(m[1], 10, 64)
	u.PeakFootprintBytes, _ = strconv.ParseInt(f[1], 10, 64)
	return u, nil
}

var oomRE = regexp.MustCompile(`(?i)insufficient memory|out of memory|OutOfMemory`)

// OutOfMemory reports whether a failed run's output names a memory failure: Metal's
// "Insufficient Memory" command buffer error, or an allocator's out-of-memory error.
func OutOfMemory(out string) bool { return oomRE.MatchString(out) }

// CompletionTokens is the token count of the completion training run in plan 3.5, the
// one gate G0 projects.
const CompletionTokens = 25_000_000

// PilotRow is the runs.jsonl line of one mlx-lm pilot.
type PilotRow struct {
	Kind         string            `json:"kind"`
	Pilot        string            `json:"pilot"`
	Model        string            `json:"model"`
	FineTune     string            `json:"fine_tune"`
	Inputs       map[string]string `json:"inputs_sha256"`
	Config       string            `json:"config"`
	ConfigSHA256 string            `json:"config_sha256"`
	MLXLM        string            `json:"mlx_lm"`
	Data         PilotData         `json:"data"`
	Exit         int               `json:"exit"`
	OOM          bool              `json:"oom"`
	Steps        int               `json:"steps"`
	FirstLoss    float64           `json:"first_loss"`
	LastLoss     float64           `json:"last_loss"`
	Tokens       int64             `json:"tokens"`
	// TokensPerSec leaves out the first report window; see SteadyTokensPerSec.
	TokensPerSec float64 `json:"tok_s"`
	// ProjectedHours is CompletionTokens at TokensPerSec, on this machine as loaded.
	ProjectedHours float64 `json:"projected_hours_25m_tokens"`
	Usage
	WallSeconds float64 `json:"wall_seconds"`
	Load1Start  float64 `json:"load1_start"`
	Load1End    float64 `json:"load1_end"`
	Log         string  `json:"log"`
}

// Summarize fills the row's step, loss, token and throughput fields from reports.
func (r *PilotRow) Summarize(rs []Report) {
	if len(rs) == 0 {
		return
	}
	r.Steps = rs[len(rs)-1].Step
	r.FirstLoss = rs[0].Loss
	r.LastLoss = rs[len(rs)-1].Loss
	r.Tokens = int64(rs[len(rs)-1].TrainedTokens)
	r.TokensPerSec = SteadyTokensPerSec(rs)
	if r.TokensPerSec > 0 {
		r.ProjectedHours = CompletionTokens / r.TokensPerSec / 3600
	}
}
