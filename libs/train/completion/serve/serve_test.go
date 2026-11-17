package serve

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/suite"
)

type serveSuite struct{ suite.Suite }

func TestServe(t *testing.T) { suite.Run(t, new(serveSuite)) }

func (s *serveSuite) TestParseVersionReadsTheBuildLine() {
	b, err := ParseVersion("version: 0.8.0 (build 11960, commit 8642541b2)\nbuilt with AppleClang\n")
	s.Require().NoError(err)
	s.Equal(Build{Version: "0.8.0", Number: 11960, Commit: "8642541b2"}, b)
	_, err = ParseVersion("llama-server unknown")
	s.ErrorContains(err, "not a version")
}

func (s *serveSuite) TestArgsOrderTheInfillAndPinTheCache() {
	args := Args(Options{Model: "m.gguf", Ctx: 8192, Slots: 1, SPM: true, CacheReuse: 256, SpecType: "ngram-simple"}, 9000)
	s.Equal([]string{"-m", "m.gguf", "--host", "127.0.0.1", "--port", "9000", "-c", "8192", "-np", "1", "--no-webui", "--no-warmup",
		"--cache-reuse", "256", "--metrics", "--spm-infill", "--spec-type", "ngram-simple"}, args)
	saved := Args(Options{Model: "m", Ctx: 1, Slots: 1, SlotSavePath: "/tmp/slots"}, 1)
	s.Equal([]string{"--slot-save-path", "/tmp/slots"}, saved[len(saved)-2:])
	plain := Args(Options{Model: "m.gguf", Ctx: 1, Slots: 1}, 1)
	s.NotContains(plain, "--spm-infill")
	s.NotContains(plain, "--spec-type")
}

func (s *serveSuite) TestStartRefusesNoSlots() {
	_, err := Start(context.Background(), Options{Bin: "llama-server", Model: "m"}, 0)
	s.ErrorContains(err, "must be positive")
}

func (s *serveSuite) TestInfillSendsTheRequestAndReadsTheReply() {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Equal("/infill", r.URL.Path)
		b, _ := io.ReadAll(r.Body)
		s.Require().NoError(json.Unmarshal(b, &got))
		_, _ = w.Write([]byte(`{"content":"a + b","stop_type":"eos","timings":{"cache_n":5,"prompt_n":3,"prompt_ms":2.5,"predicted_n":4,"predicted_ms":9},
			"completion_probabilities":[{"token":"a","logprob":-0.1},{"token":" + b","logprob":-0.3}]}`))
	}))
	defer ts.Close()
	c, err := Attach(ts.URL).Infill(context.Background(), Request{Prefix: "p", Suffix: "q", NPredict: 8, Temperature: 0.8, TopP: 0.95, Seed: 7,
		Stop: []string{"\n"}, CachePrompt: true, Extra: []Chunk{{Path: "x.go", Text: "t"}}})
	s.Require().NoError(err)
	s.Equal("a + b", c.Text)
	s.Equal(5, c.Timings.CacheN)
	s.Equal(2.5, c.Timings.PromptMS)
	s.Len(c.Tokens, 2)
	s.Equal("p", got["input_prefix"])
	s.Equal(float64(0), got["top_k"])
	s.Equal(float64(0), got["min_p"])
	s.Equal(true, got["cache_prompt"])
	s.Equal([]any{map[string]any{"filename": "x.go", "text": "t"}}, got["input_extra"])
}

func (s *serveSuite) TestInfillReportsAServerError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"Loading model","code":503}}`))
	}))
	defer ts.Close()
	_, err := Attach(ts.URL).Infill(context.Background(), Request{NPredict: 1})
	s.ErrorContains(err, "Loading model")
}

func (s *serveSuite) TestEraseSlotPostsTheAction() {
	var path string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.RequestURI()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	s.Require().NoError(Attach(ts.URL).EraseSlot(context.Background(), 2))
	s.Equal("POST /slots/2?action=erase", path)
}

func (s *serveSuite) TestFirstLineConfidenceStopsAtTheFirstNewline() {
	toks := []Token{{Text: "a", Logprob: math.Log(0.5)}, {Text: "b\n", Logprob: math.Log(0.5)}, {Text: "c", Logprob: math.Log(0.001)}}
	s.InDelta(0.5, FirstLineConfidence(toks), 1e-9)
	s.Equal(0.0, FirstLineConfidence(nil))
}
