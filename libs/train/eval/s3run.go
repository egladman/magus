package eval

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/egladman/magus/libs/train/completion/fim"
	"github.com/egladman/magus/libs/train/completion/serve"
	"github.com/egladman/magus/libs/train/probe"
)

// SetSpec names an evaluation set of stage S1 and how much of it a run reads.
type SetSpec struct {
	// Name is "<language>-<test|dev>", the suffix of fim-<name>.jsonl and the set field of
	// the rows.
	Name     string
	Language string
	// Max keeps this many spans by id order; 0 keeps the whole set.
	Max int
}

// Digest is the pinned name of the set's file.
func (s SetSpec) Digest() string { return "fim-" + s.Name }

// ServerCtx is the context window of an evaluation server: the 8,000 B window is about
// 2,400 to 3,000 tokens, and a completion adds PredictCap.
const ServerCtx = 8192

// CompleteRun is the runs.jsonl line of one system's generation over its sets.
type CompleteRun struct {
	Kind        string            `json:"kind"`
	Run         string            `json:"run"`
	System      string            `json:"system"`
	Order       string            `json:"order"`
	Context     string            `json:"context"`
	Model       string            `json:"model"`
	Inputs      map[string]string `json:"inputs_sha256"`
	Llama       serve.Build       `json:"llama_server"`
	Args        []string          `json:"args"`
	Sets        map[string]Taken  `json:"sets"`
	WallSeconds float64           `json:"wall_seconds"`
	Load1Start  float64           `json:"load1_start"`
	Load1End    float64           `json:"load1_end"`
	Predictions string            `json:"predictions"`
}

// Taken is how much of a set a run read: the sampled spans, the whole set, and the
// digest the set was pinned at.
type Taken struct {
	Spans  int    `json:"spans"`
	Of     int    `json:"of"`
	SHA256 string `json:"sha256"`
}

// LoadSet checks the set against its pin, samples it and makes the tree its files are
// checked in.
func (e S3) LoadSet(spec SetSpec) (Set, string, error) {
	rev, err := e.PinnedSet(spec.Digest())
	if err != nil {
		return Set{}, "", err
	}
	spans, of, err := SampleSpans(e.File(spec.Digest()+".jsonl"), spec.Max)
	if err != nil {
		return Set{}, "", err
	}
	tree, err := MakeTree(e.Root, rev, e.HeadRev, e.Out)
	if err != nil {
		return Set{}, "", err
	}
	sum, err := FileDigest(e.File(spec.Digest() + ".jsonl"))
	sample := "all"
	if len(spans) < of {
		sample = fmt.Sprintf("first %d by span id", len(spans))
	}
	return Set{Name: spec.Name, Language: spec.Language, Spans: spans, Of: of, Tree: tree, Sample: sample}, sum, err
}

func (e S3) predsPath(sys System, set string) string {
	return filepath.Join(e.Out, "preds-"+sys.Key()+"-"+set+".jsonl")
}

// GenerateAll serves one system's model and generates its completions on every set,
// appending a CompleteRun to runs.jsonl. The predictions persist, so a rerun resumes.
func (e S3) GenerateAll(ctx context.Context, sys System, sets []Set, sums map[string]string, log io.Writer) error {
	opts := serve.Options{Bin: e.Llama, Model: sys.Model, Ctx: ServerCtx, Slots: 1, SPM: sys.Order == fim.SPM, CacheReuse: 256}
	row := CompleteRun{Kind: "eval-complete", Run: e.Run, System: sys.Name, Order: string(sys.Order), Context: sys.Context,
		Model: sys.Model, Inputs: map[string]string{sys.Name: sys.SHA256}, Args: serve.Args(opts, 0), Sets: map[string]Taken{},
		Predictions: e.Out}
	var err error
	if row.Load1Start, err = probe.Load1(); err != nil {
		return err
	}
	start := time.Now()
	srv, err := serve.Start(ctx, opts, 15*time.Minute)
	if err != nil {
		return err
	}
	defer srv.Close()
	row.Llama = srv.Build
	for _, set := range sets {
		fmt.Fprintf(log, "%s %s: generating %d of %d spans\n", sys.Key(), set.Name, len(set.Spans), set.Of)
		err := Generate(ctx, srv, sys, set.Spans, e.predsPath(sys, set.Name), func(done, total int) {
			fmt.Fprintf(log, "%s %s: %d/%d\n", sys.Key(), set.Name, done, total)
		})
		if err != nil {
			return err
		}
		row.Sets[set.Name] = Taken{Spans: len(set.Spans), Of: set.Of, SHA256: sums[set.Name]}
	}
	row.WallSeconds = time.Since(start).Seconds()
	if row.Load1End, err = probe.Load1(); err != nil {
		return err
	}
	return probe.Append(e.Runs, row)
}

// ScoreAll scores every system on every set and appends the rows to evals.jsonl. The
// first system is the reference the others' p-values pair against.
func (e S3) ScoreAll(systems []System, sets []Set, log io.Writer) error {
	goc := NewGoChecker()
	for _, set := range sets {
		var ref CompleteMetrics
		for i, sys := range systems {
			preds, err := ReadPreds(e.predsPath(sys, set.Name))
			if err != nil {
				return err
			}
			fmt.Fprintf(log, "%s %s: checking %d predictions\n", sys.Key(), set.Name, len(preds))
			checks, err := e.CheckSpans(set, preds, goc)
			if err != nil {
				return err
			}
			m := ScoreComplete(sys, set, preds, checks, e.Run)
			if i == 0 {
				ref = m
			} else {
				m.Compare(ref, systems[0].Key())
			}
			for _, r := range m.Rows {
				if err := probe.Append(e.Evals, r); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
