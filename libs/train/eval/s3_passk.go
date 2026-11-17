package eval

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/egladman/magus/libs/train/completion/fim"
	"github.com/egladman/magus/libs/train/completion/serve"
	"github.com/egladman/magus/libs/train/probe"
)

// Plan 6.2's sampling: n samples per task at temperature 0.8 and top-p 0.95, and pass@k
// for the k below.
const (
	PassN           = 20
	PassTemperature = 0.8
	PassTopP        = 0.95
	// PassPredict bounds a sampled body: MaxTaskBody is 1,500 B, about 450 tokens, and a
	// longer one is not the function the file holds.
	PassPredict = 400
	// PassSlots is how many samples are drawn at once.
	PassSlots = 4
	// TestTimeout is how long `magus buzz -t` runs on one sample before it is killed.
	TestTimeout = 60 * time.Second
)

// PassKs are the k pass@k reports.
var PassKs = []int{1, 5, 10}

// Sample is one drawn body for a task.
type Sample struct {
	Task string `json:"task"`
	I    int    `json:"i"`
	Text string `json:"text"`
	Stop string `json:"stop"`
}

// Grade is the verdict on a sample: whether its file passes `magus buzz --check`, and
// whether every test block that calls the function passes under `magus buzz -t`. Timeout
// marks a run that was killed.
type Grade struct {
	Task    string `json:"task"`
	I       int    `json:"i"`
	Checked bool   `json:"check"`
	Passed  bool   `json:"pass"`
	Timeout bool   `json:"timeout,omitempty"`
}

// readJSONL reads rows of T, dropping a torn last line.
func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var r T
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			break
		}
		out = append(out, r)
	}
	return out, nil
}

type jsonlAppender struct {
	mu sync.Mutex
	f  *os.File
}

func (a *jsonlAppender) add(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err = a.f.Write(append(b, '\n'))
	return err
}

func (e S3) samplesPath(sys System) string {
	return filepath.Join(e.Out, "passk-samples-"+sys.Key()+".jsonl")
}

func (e S3) gradesPath(sys System) string {
	return filepath.Join(e.Out, "passk-grades-"+sys.Key()+".jsonl")
}

// TaskPrompt is a task's FIM request: the file cut at the body's braces, clipped to the
// editor's windows.
func TaskPrompt(t Task) (prefix, suffix string) {
	text := t.Prefix + t.Middle + t.Suffix
	p, _, s := fim.Windows(text, fim.Range{Start: len(t.Prefix), End: len(t.Prefix) + len(t.Middle)})
	return p, s
}

// SampleTasks draws PassN bodies for each task, PassSlots at a time, appending each to
// the system's samples file. A sample already there is not drawn again.
func (e S3) SampleTasks(ctx context.Context, srv *serve.Llama, sys System, tasks []Task, log io.Writer) error {
	have, err := readJSONL[Sample](e.samplesPath(sys))
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for _, s := range have {
		done[s.Task+"/"+strconv.Itoa(s.I)] = true
	}
	if err := os.MkdirAll(e.Out, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(e.samplesPath(sys), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	out := &jsonlAppender{f: f}
	type job struct {
		t Task
		i int
	}
	jobs := make(chan job)
	errs := make(chan error, PassSlots)
	var wg sync.WaitGroup
	var count int
	var cmu sync.Mutex
	for range PassSlots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				prefix, suffix := TaskPrompt(j.t)
				c, err := srv.Infill(ctx, serve.Request{Prefix: prefix, Suffix: suffix, NPredict: PassPredict, Temperature: PassTemperature,
					TopP: PassTopP, Seed: 1000 + j.i, CachePrompt: true, Slot: -1})
				if err == nil {
					err = out.add(Sample{Task: j.t.ID, I: j.i, Text: c.Text, Stop: c.StopType})
				}
				if err != nil {
					errs <- fmt.Errorf("task %s sample %d: %w", j.t.ID, j.i, err)
					return
				}
				cmu.Lock()
				count++
				if count%100 == 0 {
					fmt.Fprintf(log, "%s pass@k: %d samples drawn this run\n", sys.Key(), count)
				}
				cmu.Unlock()
			}
		}()
	}
feed:
	for _, t := range tasks {
		for i := range PassN {
			if done[t.ID+"/"+strconv.Itoa(i)] {
				continue
			}
			select {
			case jobs <- job{t, i}:
			case <-errs:
				break feed
			case <-ctx.Done():
				break feed
			}
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errs:
		return err
	default:
	}
	return ctx.Err()
}

// gradeName is the sibling file a graded sample is written as.
func gradeName(t Task, i int) string { return SpliceName(t.Path, t.ID+"k"+strconv.Itoa(i)) }

// GradeTasks writes each sample into the set's tree, checks all of them with `magus buzz
// --check`, and runs `magus buzz -t` on the ones that pass under the seatbelt profile,
// PassSlots at a time. Grades already stored are kept. Each task's reference file is
// graded too, under the id "ref": a task whose own file fails its tests is not gradable.
func (e S3) GradeTasks(ctx context.Context, sys System, tree Tree, tasks []Task, log io.Writer) error {
	samples, err := readJSONL[Sample](e.samplesPath(sys))
	if err != nil {
		return err
	}
	have, err := readJSONL[Grade](e.gradesPath(sys))
	if err != nil {
		return err
	}
	graded := map[string]bool{}
	for _, g := range have {
		graded[g.Task+"/"+strconv.Itoa(g.I)] = true
	}
	byID := map[string]Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	var todo []Sample
	for _, t := range tasks {
		if !graded[t.ID+"/-1"] {
			todo = append(todo, Sample{Task: t.ID, I: -1, Text: t.Middle})
		}
	}
	for _, s := range samples {
		if _, ok := byID[s.Task]; ok && !graded[s.Task+"/"+strconv.Itoa(s.I)] {
			todo = append(todo, s)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	files, err := e.writeVariants(tree, byID, todo)
	if err != nil {
		return err
	}
	defer func() {
		for _, f := range files {
			_ = os.Remove(filepath.Join(tree.Dir, f))
		}
	}()
	names := make([]string, 0, len(todo))
	for _, s := range todo {
		names = append(names, files[s.Task+"/"+strconv.Itoa(s.I)])
	}
	fmt.Fprintf(log, "%s pass@k: checking %d files\n", sys.Key(), len(names))
	rows, err := CheckBuzz(e.Magus, tree.Dir, names, CheckBatch)
	if err != nil {
		return err
	}
	checked := map[string]bool{}
	for _, r := range rows {
		checked[r.Path] = r.OK
	}
	profile, err := WriteSeatbelt(e.Out, tree.Dir)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(e.gradesPath(sys), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return e.runGrades(ctx, f, profile, tree, byID, todo, files, checked, log)
}

func (e S3) writeVariants(tree Tree, byID map[string]Task, todo []Sample) (map[string]string, error) {
	files := map[string]string{}
	for _, s := range todo {
		t := byID[s.Task]
		name := gradeName(t, s.I)
		if s.I < 0 {
			name = gradeName(t, -1)
		}
		text := t.Prefix + s.Text + t.Suffix
		if err := os.WriteFile(filepath.Join(tree.Dir, name), []byte(text), 0o644); err != nil {
			return nil, err
		}
		files[s.Task+"/"+strconv.Itoa(s.I)] = name
	}
	return files, nil
}

func (e S3) runGrades(ctx context.Context, f *os.File, profile string, tree Tree, byID map[string]Task, todo []Sample,
	files map[string]string, checked map[string]bool, log io.Writer) error {
	out := &jsonlAppender{f: f}
	jobs := make(chan Sample)
	errs := make(chan error, PassSlots)
	var wg sync.WaitGroup
	for range PassSlots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				name := files[s.Task+"/"+strconv.Itoa(s.I)]
				g := Grade{Task: s.Task, I: s.I, Checked: checked[name]}
				if g.Checked {
					text, timeout, err := RunTests(ctx, e.Magus, profile, tree.Dir, name, TestTimeout)
					if err != nil {
						errs <- err
						return
					}
					g.Passed, g.Timeout = AllPass(text, byID[s.Task].Tests), timeout
				}
				if err := out.add(g); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	n := 0
feed:
	for _, s := range todo {
		select {
		case jobs <- s:
			n++
			if n%200 == 0 {
				fmt.Fprintf(log, "pass@k: graded %d of %d\n", n, len(todo))
			}
		case <-errs:
			break feed
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errs:
		return err
	default:
	}
	return ctx.Err()
}

// PassMetrics is one system's pass@k rows and the per-task values behind them.
type PassMetrics struct {
	Rows  []Row
	Units map[string]Units
	// Gradable and Of count the tasks whose reference file passes its own tests and the
	// whole set.
	Gradable, Of int
}

// ScorePassK computes pass@1, pass@5 and pass@10 per task with the unbiased estimator,
// and the share of each task's samples whose file passes the check, over the tasks whose
// reference file passes its named tests. A task with fewer than PassN samples graded is
// an error: a short count would bias the estimator.
func (e S3) ScorePassK(sys System, tasks []Task, run string) (PassMetrics, error) {
	grades, err := readJSONL[Grade](e.gradesPath(sys))
	if err != nil {
		return PassMetrics{}, err
	}
	type tally struct {
		n, pass, check int
		ref, hasRef    bool
	}
	by := map[string]*tally{}
	for _, g := range grades {
		t := by[g.Task]
		if t == nil {
			t = &tally{}
			by[g.Task] = t
		}
		if g.I < 0 {
			t.ref, t.hasRef = g.Passed, true
			continue
		}
		t.n++
		if g.Passed {
			t.pass++
		}
		if g.Checked {
			t.check++
		}
	}
	m := PassMetrics{Units: map[string]Units{}, Of: len(tasks)}
	units := map[string]Units{}
	for _, k := range PassKs {
		units["pass@"+strconv.Itoa(k)] = NewUnits()
	}
	units["sample_check_pass"] = NewUnits()
	for _, t := range tasks {
		tl := by[t.ID]
		if tl == nil || !tl.hasRef {
			return m, fmt.Errorf("task %s has no graded reference file: grade %s first", t.ID, sys.Key())
		}
		if !tl.ref {
			continue
		}
		if tl.n != PassN {
			return m, fmt.Errorf("task %s has %d graded samples, pass@k needs %d", t.ID, tl.n, PassN)
		}
		m.Gradable++
		for _, k := range PassKs {
			units["pass@"+strconv.Itoa(k)].Add(t.ID, t.Group, PassAtK(tl.n, tl.pass, k))
		}
		units["sample_check_pass"].Add(t.ID, t.Group, float64(tl.check)/float64(tl.n))
	}
	names := make([]string, 0, len(units))
	for name := range units {
		names = append(names, name)
	}
	slices.SortFunc(names, cmp.Compare[string])
	for _, name := range names {
		r := sys.Row("buzz-passk", name, run)
		r.Of = m.Of
		units[name].Fill(&r)
		m.Rows = append(m.Rows, r)
		m.Units[name] = units[name]
	}
	return m, nil
}

// Compare fills the p-values against a reference system's tasks.
func (m *PassMetrics) Compare(ref PassMetrics, refKey string) {
	for i := range m.Rows {
		r := &m.Rows[i]
		if r.NotScored != "" {
			continue
		}
		r.P, r.Ref = m.Units[r.Metric].PValue(ref.Units[r.Metric]), refKey
	}
}

// PassRun is the runs.jsonl line of one system's pass@k run.
type PassRun struct {
	Kind        string            `json:"kind"`
	Run         string            `json:"run"`
	System      string            `json:"system"`
	Order       string            `json:"order"`
	Context     string            `json:"context"`
	Inputs      map[string]string `json:"inputs_sha256"`
	Llama       serve.Build       `json:"llama_server"`
	Args        []string          `json:"args"`
	Tasks       int               `json:"tasks"`
	Gradable    int               `json:"gradable_tasks"`
	N           int               `json:"n"`
	Temperature float64           `json:"temperature"`
	TopP        float64           `json:"top_p"`
	Sandbox     string            `json:"sandbox"`
	WallSeconds float64           `json:"wall_seconds"`
	Load1Start  float64           `json:"load1_start"`
	Load1End    float64           `json:"load1_end"`
}

// PassKAll draws and grades the samples of each system on the pass@k tasks, then scores
// them and appends the rows. The first system is the reference.
func (e S3) PassKAll(ctx context.Context, systems []System, maxTasks int, log io.Writer) error {
	rev, err := e.PinnedSet("passk-buzz")
	if err != nil {
		return err
	}
	tasks, err := ReadRows[Task](e.File("passk-buzz.jsonl"))
	if err != nil {
		return err
	}
	slices.SortFunc(tasks, func(a, b Task) int { return cmp.Compare(a.ID, b.ID) })
	if maxTasks > 0 && maxTasks < len(tasks) {
		tasks = tasks[:maxTasks]
	}
	tree, err := MakeTree(e.Root, rev, e.HeadRev, e.Out)
	if err != nil {
		return err
	}
	var ref PassMetrics
	for i, sys := range systems {
		row := PassRun{Kind: "pass-at-k", Run: e.Run, System: sys.Name, Order: string(sys.Order), Context: sys.Context,
			Inputs: map[string]string{sys.Name: sys.SHA256}, Tasks: len(tasks), N: PassN, Temperature: PassTemperature, TopP: PassTopP,
			Sandbox: "sandbox-exec profile: no network, writes only under the scratch tree, the temp dir and the bytecode cache"}
		if row.Load1Start, err = probe.Load1(); err != nil {
			return err
		}
		start := time.Now()
		opts := serve.Options{Bin: e.Llama, Model: sys.Model, Ctx: PassSlots * ServerCtx, Slots: PassSlots, SPM: sys.Order == fim.SPM, CacheReuse: 256}
		srv, err := serve.Start(ctx, opts, 15*time.Minute)
		if err != nil {
			return err
		}
		row.Llama, row.Args = srv.Build, serve.Args(opts, 0)
		err = e.SampleTasks(ctx, srv, sys, tasks, log)
		_ = srv.Close()
		if err != nil {
			return err
		}
		if err := e.GradeTasks(ctx, sys, tree, tasks, log); err != nil {
			return err
		}
		m, err := e.ScorePassK(sys, tasks, e.Run)
		if err != nil {
			return err
		}
		if i == 0 {
			ref = m
		} else {
			m.Compare(ref, systems[0].Key())
		}
		row.Gradable, row.WallSeconds = m.Gradable, time.Since(start).Seconds()
		if row.Load1End, err = probe.Load1(); err != nil {
			return err
		}
		for _, r := range m.Rows {
			if err := probe.Append(e.Evals, r); err != nil {
				return err
			}
		}
		if err := probe.Append(e.Runs, row); err != nil {
			return err
		}
	}
	return nil
}
