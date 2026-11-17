// Package serve forks llama-server as a tool and talks to its /infill endpoint. magus
// never serves a model itself: the binary is the one llama.cpp builds, pinned by commit
// the way stage S0 pinned llama-bench, and this package starts it, waits for it to load
// and stops it.
package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PinnedCommit is the llama.cpp commit stage S0's llama-bench numbers came from. A
// llama-server built at another commit is refused: its prompt template, sampler and
// cache reuse change between commits, and a baseline measured on one is not comparable
// to the numbers stage S4 will measure on another.
const PinnedCommit = "8642541b2"

// Build is what `llama-server --version` reports.
type Build struct {
	Version string `json:"version"`
	Number  int    `json:"build"`
	Commit  string `json:"commit"`
}

var versionRE = regexp.MustCompile(`version:\s*(\S+)\s*\(build\s+(\d+),\s*commit\s+([0-9a-f]+)\)`)

// ParseVersion reads the first line llama-server --version prints, "version: 0.8.0
// (build 11960, commit 8642541b2)".
func ParseVersion(out string) (Build, error) {
	m := versionRE.FindStringSubmatch(out)
	if m == nil {
		return Build{}, fmt.Errorf("llama-server --version printed %q, not a version, build and commit", strings.TrimSpace(out))
	}
	n, _ := strconv.Atoi(m[2])
	return Build{Version: m[1], Number: n, Commit: m[3]}, nil
}

// Version runs bin --version and refuses a commit other than PinnedCommit.
func Version(bin string) (Build, error) {
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		return Build{}, fmt.Errorf("%s --version: %w", bin, err)
	}
	b, err := ParseVersion(string(out))
	if err != nil {
		return Build{}, err
	}
	if b.Commit != PinnedCommit {
		return b, fmt.Errorf("%s is built at commit %s, and stage S3 pins %s", bin, b.Commit, PinnedCommit)
	}
	return b, nil
}

// Options start one llama-server.
type Options struct {
	Bin   string
	Model string
	// Port 0 takes a free one.
	Port int
	// Ctx is the total context; it is split across Slots.
	Ctx   int
	Slots int
	// SPM lays a prompt out suffix, prefix, middle, so typing that only extends the
	// prefix keeps the cache (plan 4.2).
	SPM        bool
	CacheReuse int
	// SpecType is --spec-type, "" for none.
	SpecType string
	// SlotSavePath is --slot-save-path; llama-server answers the slot actions, erase
	// among them, only when it has one.
	SlotSavePath string
	Log          io.Writer
}

// Args is the llama-server command line the options make, for a port.
func Args(o Options, port int) []string {
	args := []string{"-m", o.Model, "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"-c", strconv.Itoa(o.Ctx), "-np", strconv.Itoa(o.Slots), "--no-webui", "--no-warmup",
		"--cache-reuse", strconv.Itoa(o.CacheReuse), "--metrics"}
	if o.SPM {
		args = append(args, "--spm-infill")
	}
	if o.SpecType != "" {
		args = append(args, "--spec-type", o.SpecType)
	}
	if o.SlotSavePath != "" {
		args = append(args, "--slot-save-path", o.SlotSavePath)
	}
	return args
}

// Llama is a running llama-server.
type Llama struct {
	URL   string
	Build Build
	Opts  Options

	cmd  *exec.Cmd
	done chan error
	http *http.Client
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Start forks llama-server and waits until /health answers ok, or until ctx or timeout
// ends. A model that fails to load is an error carrying the server's exit.
func Start(ctx context.Context, o Options, timeout time.Duration) (*Llama, error) {
	if o.Slots < 1 || o.Ctx < 1 {
		return nil, fmt.Errorf("serve: %d slots and context %d, both must be positive", o.Slots, o.Ctx)
	}
	build, err := Version(o.Bin)
	if err != nil {
		return nil, err
	}
	port := o.Port
	if port == 0 {
		if port, err = freePort(); err != nil {
			return nil, err
		}
	}
	cmd := exec.Command(o.Bin, Args(o, port)...)
	if o.Log != nil {
		cmd.Stdout, cmd.Stderr = o.Log, o.Log
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", o.Bin, err)
	}
	s := &Llama{URL: "http://127.0.0.1:" + strconv.Itoa(port), Build: build, Opts: o, cmd: cmd,
		done: make(chan error, 1), http: &http.Client{Timeout: 10 * time.Minute}}
	go func() { s.done <- cmd.Wait() }()
	deadline := time.Now().Add(timeout)
	for {
		select {
		case err := <-s.done:
			return nil, fmt.Errorf("llama-server exited while loading %s: %v", o.Model, err)
		case <-ctx.Done():
			_ = s.Close()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		if s.healthy(ctx) {
			return s, nil
		}
		if time.Now().After(deadline) {
			_ = s.Close()
			return nil, fmt.Errorf("llama-server did not report healthy within %s", timeout)
		}
	}
}

func (s *Llama) healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Attach is a Llama over a URL some other process already serves, for tests.
func Attach(url string) *Llama {
	return &Llama{URL: url, http: &http.Client{Timeout: time.Minute}}
}

// Close stops the server and waits for it to exit.
func (s *Llama) Close() error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	_ = s.cmd.Process.Signal(os.Interrupt)
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.done
	}
	return nil
}

// Chunk is a piece of retrieved context, sent as /infill's input_extra. Stage S2's
// provider fills it; the baselines without context send none.
type Chunk struct {
	Path string `json:"filename"`
	Text string `json:"text"`
}

// Request is one /infill call.
type Request struct {
	Prefix, Suffix string
	Extra          []Chunk
	NPredict       int
	Temperature    float64
	TopP           float64
	Seed           int
	Stop           []string
	// CachePrompt keeps the slot's cache between calls; false forces a cold prefill.
	CachePrompt bool
	// Slot pins the call to one slot, -1 for any.
	Slot int
}

// Token is one generated token and its log probability.
type Token struct {
	Text    string  `json:"token"`
	Logprob float64 `json:"logprob"`
}

// Timings are llama-server's own measurements of a call.
type Timings struct {
	CacheN        int     `json:"cache_n"`
	PromptN       int     `json:"prompt_n"`
	PromptMS      float64 `json:"prompt_ms"`
	PredictedN    int     `json:"predicted_n"`
	PredictedMS   float64 `json:"predicted_ms"`
	DraftN        int     `json:"draft_n"`
	DraftAccepted int     `json:"draft_n_accepted"`
}

// Completion is what /infill answered. Wall is the round trip as the caller saw it, the
// number plan 4.2 budgets.
type Completion struct {
	Text     string
	Tokens   []Token
	StopType string
	Timings  Timings
	Wall     time.Duration
}

type infillBody struct {
	Prefix      string   `json:"input_prefix"`
	Suffix      string   `json:"input_suffix"`
	Extra       []Chunk  `json:"input_extra,omitempty"`
	NPredict    int      `json:"n_predict"`
	Temperature float64  `json:"temperature"`
	TopP        float64  `json:"top_p"`
	TopK        int      `json:"top_k"`
	MinP        float64  `json:"min_p"`
	Seed        int      `json:"seed"`
	Stop        []string `json:"stop,omitempty"`
	CachePrompt bool     `json:"cache_prompt"`
	NProbs      int      `json:"n_probs"`
	IDSlot      int      `json:"id_slot"`
}

type infillReply struct {
	Content       string  `json:"content"`
	StopType      string  `json:"stop_type"`
	Timings       Timings `json:"timings"`
	Probabilities []Token `json:"completion_probabilities"`
	Error         *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Infill sends one request. top_k and min_p are fixed at 0 so only temperature and
// top_p shape sampling: llama-server's defaults of 40 and 0.05 would otherwise change
// pass@k's distribution under the plan's T=0.8, top-p 0.95.
func (s *Llama) Infill(ctx context.Context, r Request) (Completion, error) {
	body, err := json.Marshal(infillBody{Prefix: r.Prefix, Suffix: r.Suffix, Extra: r.Extra, NPredict: r.NPredict,
		Temperature: r.Temperature, TopP: r.TopP, TopK: 0, MinP: 0, Seed: r.Seed, Stop: r.Stop,
		CachePrompt: r.CachePrompt, NProbs: 1, IDSlot: r.Slot})
	if err != nil {
		return Completion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL+"/infill", bytes.NewReader(body))
	if err != nil {
		return Completion{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := s.http.Do(req)
	if err != nil {
		return Completion{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	wall := time.Since(start)
	if err != nil {
		return Completion{}, err
	}
	var rep infillReply
	if err := json.Unmarshal(raw, &rep); err != nil {
		return Completion{}, fmt.Errorf("/infill answered %d with %q: %w", resp.StatusCode, truncate(raw), err)
	}
	if rep.Error != nil || resp.StatusCode != http.StatusOK {
		msg := truncate(raw)
		if rep.Error != nil {
			msg = rep.Error.Message
		}
		return Completion{}, fmt.Errorf("/infill answered %d: %s", resp.StatusCode, msg)
	}
	return Completion{Text: rep.Content, Tokens: rep.Probabilities, StopType: rep.StopType, Timings: rep.Timings, Wall: wall}, nil
}

func truncate(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return strings.TrimSpace(string(b))
}

// EraseSlot drops a slot's cache, so the next call prefills from nothing.
func (s *Llama) EraseSlot(ctx context.Context, slot int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL+"/slots/"+strconv.Itoa(slot)+"?action=erase", nil)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("erase slot %d answered %d: %s", slot, resp.StatusCode, truncate(b))
	}
	return nil
}

// FirstLineConfidence is the geometric mean probability of the tokens that make the
// completion's first line, the first token holding a newline included, since a model
// sure of the line is also sure where it ends. It is 0 for a completion with no token
// probabilities.
func FirstLineConfidence(tokens []Token) float64 {
	if len(tokens) == 0 {
		return 0
	}
	var sum float64
	n := 0
	for _, t := range tokens {
		sum += t.Logprob
		n++
		if strings.Contains(t.Text, "\n") {
			break
		}
	}
	return expNeg(-sum / float64(n))
}

func expNeg(x float64) float64 { return math.Exp(-x) }
