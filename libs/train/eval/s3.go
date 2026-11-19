package eval

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/egladman/magus/libs/train/completion/fim"
	"github.com/egladman/magus/libs/train/completion/serve"
	"github.com/egladman/magus/libs/train/probe"
)

// S3 is where stage S3 reads and writes: stage S1's datasets through Env, the pinned
// models through Cache and Inputs, one run directory under the cache root for
// predictions and scratch trees, and the two committed logs.
type S3 struct {
	Env
	Cache  string
	Inputs string
	Out    string
	Llama  string
	Runs   string
	Evals  string
	Run    string
	// HeadRev is the commit whose magus.yaml the checking binary accepts.
	HeadRev string
}

// System is one base model as served: its GGUF, the order its prompts take and the
// context provider in front of it. Context is "none" until stage S2 builds a provider;
// any other value sends each span's Context chunks as /infill's input_extra and fails
// when a span carries none.
type System struct {
	Name    string
	Model   string
	SHA256  string
	Order   fim.Order
	Context string
}

// Key names a system's rows and files: model, prompt order and context.
func (s System) Key() string { return s.Name + "." + string(s.Order) + "." + s.Context }

// Row is the system name evals rows carry; the order and the context are fields beside it.
func (s System) Row(set, metric, run string) Row {
	return Row{System: s.Name, Context: s.Context, Order: string(s.Order), Set: set, Metric: metric, Run: run}
}

// ResolveSystem finds the pinned GGUF of a base by name (the directory under gguf/ in
// inputs.yaml) and checks it is in the cache root at its pinned size.
func ResolveSystem(inputs []probe.Input, cache, name string, order fim.Order, context string) (System, error) {
	var found []probe.Input
	for _, in := range inputs {
		if strings.HasPrefix(in.Name, "gguf/"+name+"/") && strings.HasSuffix(in.Name, ".gguf") {
			found = append(found, in)
		}
	}
	if len(found) != 1 {
		return System{}, fmt.Errorf("inputs.yaml pins %d GGUFs under gguf/%s/, want one", len(found), name)
	}
	if order != fim.PSM && order != fim.SPM {
		return System{}, fmt.Errorf("prompt order %q is neither psm nor spm", order)
	}
	if _, err := probe.Present(cache, found); err != nil {
		return System{}, err
	}
	return System{Name: name, Model: filepath.Join(cache, found[0].Path), SHA256: found[0].SHA256, Order: order, Context: context}, nil
}

// Heavy is the lock of the one heavy run at a time: other sessions share the machine,
// and two runs of ours would measure each other. It is an advisory flock on a file in
// the run directory's parent, released by Close or when the process exits.
type Heavy struct{ f *os.File }

// LockHeavy takes the lock or fails naming the file, without waiting.
func LockHeavy(dir string) (*Heavy, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "heavy.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another heavy stage S3 run holds %s: %w", path, err)
	}
	return &Heavy{f: f}, nil
}

// Close releases the lock.
func (h *Heavy) Close() error { return h.f.Close() }

// PinnedSet checks a dataset file in the S1 directory against the digest gen/split.digest
// pins, and returns the commit the set was cut from. A set that was rewritten after it
// was pinned is an error: every number would be against a different test set.
func (e S3) PinnedSet(name string) (rev string, err error) {
	digests, err := ReadDigests(e.Digests)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(digests, func(d Digest) bool { return d.Name == name })
	if i < 0 {
		return "", fmt.Errorf("%s pins no %s (run the eval-sets target)", e.Digests, name)
	}
	got, err := FileDigest(e.File(name + ".jsonl"))
	if err != nil {
		return "", err
	}
	if got != digests[i].SHA256 {
		return "", fmt.Errorf("%s.jsonl is %s, %s pins %s: the set changed after it was pinned", name, got[:12], e.Digests, digests[i].SHA256[:12])
	}
	return digests[i].Rev, nil
}

// SampleSpans reads a span set and keeps the first max by span id, which is a hash of
// path, blob and range: a fixed sample that does not favour a file or a group. max 0 keeps
// every span. It returns the sampled spans and the size of the whole set.
func SampleSpans(path string, max int) ([]Span, int, error) {
	spans, err := ReadRows[Span](path)
	if err != nil {
		return nil, 0, err
	}
	total := len(spans)
	slices.SortFunc(spans, func(a, b Span) int { return cmp.Compare(a.ID, b.ID) })
	if max > 0 && max < len(spans) {
		spans = spans[:max]
	}
	return spans, total, nil
}

// PredictCap is the most tokens a completion is allowed in an evaluation, the cap of
// an explicit request (plan 4.2). A middle longer than that is cut where the editor
// would cut it, so its check-pass is judged on the cut text.
const PredictCap = 64

// ReadPreds reads the predictions already stored for a run; a missing file has none.
func ReadPreds(path string) (map[string]Pred, error) {
	out := map[string]Pred{}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var p Pred
		if err := json.Unmarshal(sc.Bytes(), &p); err != nil {
			// A torn last line from a killed run: drop it and ask for the span again.
			break
		}
		out[p.ID] = p
	}
	return out, nil
}

func appendPred(f *os.File, p Pred) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func extra(sys System, s Span) ([]serve.Chunk, error) {
	if sys.Context == "none" {
		return nil, nil
	}
	if len(s.Context) == 0 {
		return nil, fmt.Errorf("span %s carries no context chunks, and the system runs with context %q", s.ID, sys.Context)
	}
	out := make([]serve.Chunk, len(s.Context))
	for i, c := range s.Context {
		out[i] = serve.Chunk{Path: c.Path, Text: c.Text}
	}
	return out, nil
}

// Generate asks the server for each span's completion at temperature 0 and appends the
// answers to path, one line each. Spans already in the file are skipped, so a killed run
// resumes. progress gets the count done and the count wanted.
func Generate(ctx context.Context, srv *serve.Llama, sys System, spans []Span, path string, progress func(done, total int)) error {
	have, err := ReadPreds(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// A killed run can leave a torn last line; rewrite the file from what parsed.
	if err := rewritePreds(path, have); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for i, s := range spans {
		if _, ok := have[s.ID]; ok {
			continue
		}
		ex, err := extra(sys, s)
		if err != nil {
			return err
		}
		c, err := srv.Infill(ctx, serve.Request{Prefix: s.Prefix, Suffix: s.Suffix, Extra: ex, NPredict: PredictCap,
			Seed: 1, CachePrompt: true, Slot: -1})
		if err != nil {
			return fmt.Errorf("span %s: %w", s.ID, err)
		}
		p := Pred{ID: s.ID, Text: c.Text, Confidence: serve.FirstLineConfidence(c.Tokens), Tokens: c.Timings.PredictedN, Stop: c.StopType}
		if err := appendPred(f, p); err != nil {
			return err
		}
		if progress != nil && (i+1)%50 == 0 {
			progress(i+1, len(spans))
		}
	}
	return nil
}

func rewritePreds(path string, have map[string]Pred) error {
	if len(have) == 0 {
		return os.WriteFile(path, nil, 0o644)
	}
	ids := make([]string, 0, len(have))
	for id := range have {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var b strings.Builder
	for _, id := range ids {
		line, err := json.Marshal(have[id])
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
