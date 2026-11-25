// Command train runs the stage S0 probes of the local-models plan over the inputs
// libs/train/inputs.yaml pins, and appends one row per run to libs/train/runs.jsonl.
// It never serves a model: mlx_lm.lora, llama-bench and llama-server do the model work,
// and this command measures them.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/train/embed/teacher"
	"github.com/egladman/magus/libs/train/eval"
	"github.com/egladman/magus/libs/train/loop"
	"github.com/egladman/magus/libs/train/probe"
	"github.com/egladman/magus/libs/xdg"
)

func main() {
	command.Main(root())
}

// root is the train command: one child for each stage's probe or step.
func root() command.Command {
	return command.Command{
		Name:  "train",
		Short: "run the stage S0 to S3 probes of the local-models plan",
		Children: []command.Command{
			fertilityCmd, pilotCmd, benchCmd,
			s1Command("dedup", "find exact and near-duplicate files among the split's candidates (stage S1)"),
			s1Command("pin-split", "pin the split split.buzz wrote in gen/split.digest (stage S1)"),
			s1Command("eval-sets", "write the FIM dev and test sets and the Buzz pass@k tasks (stage S1)"),
			s1Command("fim", "write the FIM training samples, decontaminated against the eval sets (stage S1)"),
			s1Command("g1", "recount the G1 bullets from the datasets and write the report (stage S1)"),
			evalCompleteCmd, passAtKCmd, latencyCmd, unrollCmd,
		},
	}
}

type common struct {
	inputs, cache, runs, sources, root string
}

func (c *common) heldIn(languages ...string) ([]probe.Text, string, int, error) {
	sources, err := probe.HeldIn(c.sources, languages...)
	if err != nil {
		return nil, "", 0, err
	}
	texts, stale, err := probe.ReadHeldIn(c.root, sources)
	if err != nil {
		return nil, "", 0, err
	}
	sum, err := probe.FileSHA256(c.sources)
	return texts, sum, stale, err
}

func runFertility(c *common, tokenizers string) error {
	inputs, err := probe.ReadInputs(c.inputs)
	if err != nil {
		return err
	}
	texts, sourcesSum, stale, err := c.heldIn("buzz", "go", "typescript")
	if err != nil {
		return err
	}
	for _, name := range strings.Split(tokenizers, ",") {
		pinned, err := probe.WithPrefix(inputs, name)
		if err != nil {
			return err
		}
		sums, err := probe.Present(c.cache, pinned)
		if err != nil {
			return err
		}
		start := time.Now()
		bpe, err := teacher.LoadBPE(filepath.Join(c.cache, pinned[0].Path))
		if err != nil {
			return err
		}
		langs := probe.MeasureFertility(bpe, texts)
		load, err := probe.Load1()
		if err != nil {
			return err
		}
		row := probe.FertilityRow{Kind: "fertility", Tokenizer: name, Inputs: sums, SourcesSHA256: sourcesSum,
			Stale: stale, Languages: langs, WallSeconds: time.Since(start).Seconds(), Load1: load}
		if err := probe.Append(c.runs, row); err != nil {
			return err
		}
		for _, lang := range sortedKeys(langs) {
			f := langs[lang]
			fmt.Printf("%-44s %-10s files %5d  bytes %10d  tokens %9d  tokens/byte %.4f\n", name, lang, f.Files, f.Bytes, f.Tokens, f.TokensPerByte)
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var fineTuneRE = regexp.MustCompile(`(?m)^fine_tune_type:\s*(\w+)\s*$`)

type pilotOptions struct {
	config, model        string
	maxBytes, validEvery int
}

func runPilot(c *common, o *pilotOptions) error {
	config, model, maxBytes, validEvery := &o.config, &o.model, &o.maxBytes, &o.validEvery
	if *config == "" || *model == "" {
		return errors.New("needs -config and -model")
	}
	cfg, err := os.ReadFile(*config)
	if err != nil {
		return err
	}
	m := fineTuneRE.FindSubmatch(cfg)
	if m == nil {
		return fmt.Errorf("%s sets no fine_tune_type", *config)
	}
	cfgSum, err := probe.FileSHA256(*config)
	if err != nil {
		return err
	}
	inputs, err := probe.ReadInputs(c.inputs)
	if err != nil {
		return err
	}
	pinned, err := probe.WithPrefix(inputs, *model)
	if err != nil {
		return err
	}
	sums, err := probe.Present(c.cache, pinned)
	if err != nil {
		return err
	}
	modelDir, err := commonDir(c.cache, pinned)
	if err != nil {
		return err
	}
	texts, _, _, err := c.heldIn("buzz")
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(*config), filepath.Ext(*config))
	work := filepath.Join(c.cache, "train", "pilot")
	data, err := probe.WriteData(filepath.Join(work, "data"), texts, *maxBytes, *validEvery)
	if err != nil {
		return err
	}
	lora, version, err := mlxLM()
	if err != nil {
		return err
	}
	logPath := filepath.Join(work, name, "mlx_lm.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()
	row := probe.PilotRow{Kind: "pilot", Pilot: name, Model: *model, FineTune: string(m[1]), Inputs: sums,
		Config: *config, ConfigSHA256: cfgSum, MLXLM: version, Data: data, Log: rel(c.cache, logPath)}
	row.Data.Dir = rel(c.cache, data.Dir)
	cmd := exec.Command("/usr/bin/time", "-l", lora, "--config", *config, "--model", modelDir,
		"--data", data.Dir, "--adapter-path", filepath.Join(work, name, "adapters"))
	out, runErr := measure(cmd, io.MultiWriter(os.Stdout, logFile), &row.WallSeconds, &row.Load1Start, &row.Load1End)
	if out == nil {
		return runErr
	}
	row.Exit = cmd.ProcessState.ExitCode()
	row.OOM = runErr != nil && probe.OutOfMemory(string(out))
	row.Summarize(probe.ParseReports(string(out)))
	if row.Usage, err = probe.ParseUsage(string(out)); err != nil {
		return err
	}
	if runErr == nil && row.Steps == 0 {
		return fmt.Errorf("mlx_lm.lora exited 0 but printed no training report, see %s", logPath)
	}
	if err := probe.Append(c.runs, row); err != nil {
		return err
	}
	fmt.Printf("%s: exit %d, oom %t, %d steps, %.0f tok/s, peak footprint %.2f GiB, %.1f h for %d tokens, load %.1f -> %.1f\n",
		name, row.Exit, row.OOM, row.Steps, row.TokensPerSec, float64(row.PeakFootprintBytes)/(1<<30),
		row.ProjectedHours, probe.CompletionTokens, row.Load1Start, row.Load1End)
	if runErr != nil {
		return fmt.Errorf("mlx_lm.lora (output in %s): %w", logPath, runErr)
	}
	return nil
}

// measure runs cmd with its stdout and stderr copied to w, and returns everything it
// printed. It records the wall time and the load average before and after. A nil
// output means the command never ran; a non-nil one comes with the command's own error.
func measure(cmd *exec.Cmd, w io.Writer, wall, loadStart, loadEnd *float64) ([]byte, error) {
	var err error
	if *loadStart, err = probe.Load1(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, w)
	cmd.Stderr = cmd.Stdout
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	runErr := cmd.Wait()
	*wall = time.Since(start).Seconds()
	if *loadEnd, err = probe.Load1(); err != nil {
		return nil, err
	}
	return buf.Bytes(), runErr
}

// commonDir is the one directory every pinned file of a model sits in, which mlx-lm
// loads as the model.
func commonDir(cache string, inputs []probe.Input) (string, error) {
	dir := filepath.Dir(inputs[0].Path)
	for _, in := range inputs[1:] {
		if filepath.Dir(in.Path) != dir {
			return "", fmt.Errorf("%s and %s sit in different directories", inputs[0].Name, in.Name)
		}
	}
	return filepath.Join(cache, dir), nil
}

var distInfoRE = regexp.MustCompile(`mlx_lm-([^/]+)\.dist-info$`)

// mlxLM finds the mlx_lm.lora mise installs from mise.toml and the mlx-lm version its
// environment holds. mise links the command from its bin/ into the venv's bin/.
func mlxLM() (path, version string, err error) {
	out, err := exec.Command("mise", "which", "mlx_lm.lora").Output()
	if err != nil {
		return "", "", fmt.Errorf("mise which mlx_lm.lora (mise install puts it on the path): %w", err)
	}
	if path, err = filepath.EvalSymlinks(strings.TrimSpace(string(out))); err != nil {
		return "", "", err
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "..", "lib", "python*", "site-packages", "mlx_lm-*.dist-info"))
	if err != nil || len(matches) != 1 {
		return "", "", fmt.Errorf("want one mlx_lm dist-info beside %s, found %d", path, len(matches))
	}
	return path, distInfoRE.FindStringSubmatch(matches[0])[1], nil
}

func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(r, "..") {
		return path
	}
	return r
}

func runBench(c *common, prefix, reps string) error {
	inputs, err := probe.ReadInputs(c.inputs)
	if err != nil {
		return err
	}
	pinned, err := probe.WithPrefix(inputs, prefix)
	if err != nil {
		return err
	}
	bench, err := exec.LookPath("llama-bench")
	if err != nil {
		return fmt.Errorf("llama-bench (brew install llama.cpp): %w", err)
	}
	for _, in := range pinned {
		if !strings.HasSuffix(in.Name, ".gguf") {
			continue
		}
		sums, err := probe.Present(c.cache, []probe.Input{in})
		if err != nil {
			return err
		}
		row := probe.BenchRow{Kind: "gguf-bench", Model: in.Name, Inputs: sums}
		cmd := exec.Command("/usr/bin/time", "-l", bench, "-m", filepath.Join(c.cache, in.Path),
			"-p", "512", "-n", "128", "-r", reps, "-o", "json")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)
		if row.Load1Start, err = probe.Load1(); err != nil {
			return err
		}
		start := time.Now()
		runErr := cmd.Run()
		row.WallSeconds = time.Since(start).Seconds()
		if row.Load1End, err = probe.Load1(); err != nil {
			return err
		}
		if runErr != nil {
			return fmt.Errorf("llama-bench %s: %w", in.Name, runErr)
		}
		if row.Results, err = probe.ParseLlamaBench(stdout.Bytes()); err != nil {
			return fmt.Errorf("%s: %w", in.Name, err)
		}
		if row.Usage, err = probe.ParseUsage(stderr.String()); err != nil {
			return err
		}
		if err := probe.Append(c.runs, row); err != nil {
			return err
		}
		for _, r := range row.Results {
			fmt.Printf("%-64s %-6s %9.1f ± %.1f t/s  load %.1f -> %.1f\n", in.Name, r.Test, r.AvgTS, r.StddevTS, row.Load1Start, row.Load1End)
		}
	}
	return nil
}

type s1Options struct {
	rev, digests, magus string
}

// runS1 runs one stage S1 command. Every dataset goes under the cache root's train/s1
// directory; only the digests it pins reach git.
func runS1(cmd string, c *common, o *s1Options) error {
	rev, digests, binary := &o.rev, &o.digests, &o.magus
	cutoff, err := eval.ReadCutoff(c.inputs)
	if err != nil {
		return err
	}
	tip, err := revParse(c.root, *rev)
	if err != nil {
		return err
	}
	e := eval.Env{Root: c.root, Rev: tip, Cutoff: cutoff.Commit, Dir: filepath.Join(c.cache, "train", "s1"),
		Digests: *digests, Magus: *binary}
	if e.Magus, err = filepath.Abs(*binary); err != nil {
		return err
	}
	switch cmd {
	case "dedup":
		rep, err := eval.RunDedup(e)
		if err != nil {
			return err
		}
		return printJSON(rep)
	case "pin-split":
		return eval.PinSplit(e)
	case "eval-sets":
		counts, err := eval.RunSets(e)
		if err != nil {
			return err
		}
		return printJSON(counts)
	case "fim":
		counts, err := eval.RunFIM(e)
		if err != nil {
			return err
		}
		return printJSON(counts)
	default:
		rep, err := eval.RunG1(e, cutoff)
		if err != nil {
			return err
		}
		for _, b := range rep.Bullets {
			verdict := "pass"
			if !b.Pass {
				verdict = "FAIL"
			}
			fmt.Printf("%-4s %-24s %s (%s)\n", verdict, b.ID, b.Shown, b.Want)
		}
		if err := printJSON(map[string]any{"rev": rep.Rev, "cutoff": rep.Cutoff, "bytes": rep.Bytes, "files": rep.Files,
			"groups_over_3_percent": rep.BigGroups, "bytes_in_groups_over_3_percent": rep.BigGroupBytes, "dedup_pairs": rep.Pairs,
			"check_failures_by_reason": rep.CheckFails, "check_absent_at_cutoff": rep.CheckAbsent,
			"held_out_buzz_files": rep.CheckFilesTotal, "check_pass_in_place_at_rev": rep.CheckAtRev,
			"check_failures_at_rev_by_reason": rep.CheckAtRevFails, "fim_buzz_test_spans": rep.FIMBuzzTest, "fim_buzz_test_groups": rep.FIMBuzzGroups}); err != nil {
			return err
		}
		if len(rep.Failed) > 0 {
			return fmt.Errorf("G1 bullets that do not hold: %s", strings.Join(rep.Failed, ", "))
		}
		return nil
	}
}

func revParse(root, rev string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--verify", rev+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s: %w", rev, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// s3Common is the flags the three stage S3 commands share, bound into an eval.Config.
type s3Common struct {
	cfg             eval.Config
	run, llama      string
	magus, evals    string
	digests         string
	systems, ctxArg string
}

// config copies the shared flags into an eval.Config.
func (s *s3Common) config() *eval.Config {
	cfg := &eval.Config{Run: s.run, Llama: s.llama, Magus: s.magus, Evals: s.evals, Digests: s.digests, Systems: s.systems, Context: s.ctxArg}
	cfg.Inputs, cfg.Cache, cfg.Runs, cfg.Root = shared.inputs, shared.cache, shared.runs, shared.root
	return cfg
}

// Only one command runs per process, so the commands share one set of option variables, as
// the command trees of the other mains do.
var (
	shared      common
	fertilize   fertilityOptions
	pilot       pilotOptions
	gguf        benchOptions
	stageOne    s1Options
	stageThree  s3Common
	completion  completeOptions
	passAtK     passOptions
	latencyOpts latencyOptions
)

type (
	fertilityOptions struct{ tokenizers string }
	benchOptions     struct{ prefix, reps string }
	completeOptions  struct{ sets, max string }
	passOptions      struct{ tasks int }
	latencyOptions   struct {
		n   int
		set string
	}
)

var commonFlags = []command.Flag{
	{Name: "inputs", Kind: command.String, Default: "inputs.yaml", Doc: "the pinned inputs file", Target: &shared.inputs},
	{Name: "cache", Kind: command.String, Default: cacheDefault(), Doc: "the cache root inputs.yaml paths are relative to", Target: &shared.cache},
	{Name: "runs", Kind: command.String, Default: "runs.jsonl", Doc: "the run log rows are appended to", Target: &shared.runs},
	{Name: "sources", Kind: command.String, Default: "embed/gen/sources.jsonl", Doc: "the dealt split of tracked files", Target: &shared.sources},
	{Name: "root", Kind: command.String, Default: "../..", Doc: "the workspace root sources.jsonl paths are relative to", Target: &shared.root},
}

// cacheDefault is the cache root the commands default to, empty when no cache directory resolves.
func cacheDefault() string {
	dir, err := xdg.CacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "magus")
}

var fertilityCmd = command.Command{
	Name:   "fertility",
	Short:  "append tokens per byte of held-in hand-written Buzz, Go and TS under each tokenizer",
	Effect: command.EffectLocalWrite,
	Flags: slices.Concat(commonFlags, []command.Flag{
		{Name: "tokenizers", Kind: command.String, Default: "qwen2.5-coder-0.5b/tokenizer.json,mellum2-12b-a2.5b-base/tokenizer.json", Doc: "comma-separated input names of tokenizer.json files", Target: &fertilize.tokenizers},
	}),
	Run: func(_ context.Context, _ command.Invocation) error {
		return runFertility(&shared, fertilize.tokenizers)
	},
}

var pilotCmd = command.Command{
	Name:   "pilot",
	Short:  "run one mlx_lm.lora pilot on train-split Buzz and append its throughput and memory",
	Effect: command.EffectRun,
	Flags: slices.Concat(commonFlags, []command.Flag{
		{Name: "config", Kind: command.String, Doc: "the committed mlx-lm YAML, e.g. config/pilot-qwen2.5-coder-0.5b-full.yaml", Target: &pilot.config},
		{Name: "model", Kind: command.String, Doc: "the input name prefix of the model directory, e.g. qwen2.5-coder-0.5b/", Target: &pilot.model},
		{Name: "max-bytes", Kind: command.Int, Default: 12000, Doc: "chunk size; Buzz runs about 3 bytes a token, so 12000 bytes fill most of a 4,096-token sequence", Target: &pilot.maxBytes},
		{Name: "valid-every", Kind: command.Int, Default: 25, Doc: "every n-th chunk goes to valid.jsonl", Target: &pilot.validEvery},
	}),
	Run: func(_ context.Context, _ command.Invocation) error { return runPilot(&shared, &pilot) },
}

var benchCmd = command.Command{
	Name:   "gguf-bench",
	Short:  "run llama-bench (pp512, tg128) over each pinned GGUF and append its rates",
	Effect: command.EffectRun,
	Flags: slices.Concat(commonFlags, []command.Flag{
		{Name: "prefix", Kind: command.String, Default: "gguf/", Doc: "benchmark every pinned .gguf whose name starts with this", Target: &gguf.prefix},
		{Name: "r", Kind: command.String, Default: "5", Doc: "llama-bench repetitions per test", Target: &gguf.reps},
	}),
	Run: func(_ context.Context, _ command.Invocation) error { return runBench(&shared, gguf.prefix, gguf.reps) },
}

var s1Flags = slices.Concat(commonFlags, []command.Flag{
	{Name: "rev", Kind: command.String, Default: "HEAD", Doc: "the commit the split, the dedup and the test sets read", Target: &stageOne.rev},
	{Name: "digests", Kind: command.String, Default: "gen/split.digest", Doc: "the digest file datasets are pinned in", Target: &stageOne.digests},
	{Name: "magus", Kind: command.String, Default: "../../magus", Doc: "the magus binary --check runs with", Target: &stageOne.magus},
})

func s1Command(name, short string) command.Command {
	return command.Command{
		Name:   name,
		Short:  short,
		Effect: command.EffectLocalWrite,
		Flags:  s1Flags,
		Run:    func(_ context.Context, _ command.Invocation) error { return runS1(name, &shared, &stageOne) },
	}
}

// s3Flags are the flags the three stage S3 commands share, less --systems, whose default each names.
var s3Flags = slices.Concat(commonFlags, []command.Flag{
	{Name: "run", Kind: command.String, Doc: "the run name: its predictions and scratch trees live under <cache>/tmp/codemodel-eval/<run>, and a rerun with the same name resumes", Target: &stageThree.run},
	{Name: "llama-server", Kind: command.String, Default: "llama-server", Doc: "the llama-server binary, built at the pinned commit", Target: &stageThree.llama},
	{Name: "magus", Kind: command.String, Default: "../../magus", Doc: "the magus binary --check and -t run with", Target: &stageThree.magus},
	{Name: "evals", Kind: command.String, Default: "evals.jsonl", Doc: "the evaluation log rows are appended to", Target: &stageThree.evals},
	{Name: "digests", Kind: command.String, Default: "gen/split.digest", Doc: "the digest file datasets are pinned in", Target: &stageThree.digests},
	{Name: "context", Kind: command.String, Default: "none", Doc: "the context provider in front of each base; none until stage S2 builds one, and any other name needs spans that carry context chunks", Target: &stageThree.ctxArg},
})

// systemsFlag is --systems with the bases a command serves by default.
func systemsFlag(defaults string) command.Flag {
	return command.Flag{Name: "systems", Kind: command.String, Default: defaults, Doc: "comma-separated base names under gguf/ in inputs.yaml, each name[:psm|:spm]; the first is the reference the others' p-values pair against", Target: &stageThree.systems}
}

var evalCompleteCmd = command.Command{
	Name:   "eval-complete",
	Short:  "serve each base with llama-server, complete the FIM test sets and score them (stage S3)",
	Effect: command.EffectRun,
	Flags: slices.Concat(s3Flags, []command.Flag{
		systemsFlag("qwen2.5-coder-0.5b:spm,qwen2.5-coder-1.5b:spm,mellum-4b-base:spm"),
		{Name: "sets", Kind: command.String, Default: "buzz-test,go-test,typescript-test", Doc: "comma-separated <language>-<test|dev> sets", Target: &completion.sets},
		{Name: "max", Kind: command.String, Doc: "comma-separated set=N caps on a set's span count, kept by span id order; absent keeps the whole set", Target: &completion.max},
	}),
	Run: func(ctx context.Context, _ command.Invocation) error {
		e, systems, err := stageThree.config().Open()
		if err != nil {
			return err
		}
		specs, err := eval.ParseSets(completion.sets, completion.max)
		if err != nil {
			return err
		}
		return e.CompleteAll(ctx, systems, specs, os.Stderr)
	},
}

var passAtKCmd = command.Command{
	Name:   "pass-at-k",
	Short:  "sample the Buzz pass@k tasks from each base and grade them with magus buzz -t (stage S3)",
	Effect: command.EffectRun,
	Flags: slices.Concat(s3Flags, []command.Flag{
		systemsFlag("qwen2.5-coder-0.5b:spm,qwen2.5-coder-1.5b:spm"),
		{Name: "tasks", Kind: command.Int, Default: 0, Doc: "sample and grade only this many tasks, by id order; 0 takes all", Target: &passAtK.tasks},
	}),
	Run: func(ctx context.Context, _ command.Invocation) error {
		e, systems, err := stageThree.config().Open()
		if err != nil {
			return err
		}
		return e.PassAll(ctx, systems, passAtK.tasks, os.Stderr)
	},
}

var latencyCmd = command.Command{
	Name:   "latency",
	Short:  "replay FIM-Buzz dev spans in typing order against each base (stage S3)",
	Effect: command.EffectRun,
	Flags: slices.Concat(s3Flags, []command.Flag{
		systemsFlag("qwen2.5-coder-0.5b:spm,qwen2.5-coder-1.5b:spm"),
		{Name: "n", Kind: command.Int, Default: 1000, Doc: "replay this many dev spans in typing order, or all of them when the set is smaller", Target: &latencyOpts.n},
		{Name: "set", Kind: command.String, Default: "buzz-dev", Doc: "the dev set to replay", Target: &latencyOpts.set},
	}),
	Run: func(ctx context.Context, _ command.Invocation) error {
		e, systems, err := stageThree.config().Open()
		if err != nil {
			return err
		}
		return e.LatencyAll(ctx, systems, latencyOpts.set, latencyOpts.n, os.Stderr)
	},
}

type unrollOptions struct {
	src, dst           string
	start, end, passes int
}

var unrollOpts unrollOptions

var unrollCmd = command.Command{
	Name:   "unroll",
	Short:  "write a copy of a .gguf file or safetensors directory with layers start..end looped",
	Effect: command.EffectLocalWrite,
	Flags: []command.Flag{
		{Name: "src", Kind: command.String, Doc: "the .gguf file, or the directory holding config.json and the safetensors", Target: &unrollOpts.src},
		{Name: "dst", Kind: command.String, Doc: "the file or directory the copy is written to", Target: &unrollOpts.dst},
		{Name: "start", Kind: command.Int, Doc: "the first layer of the loop", Target: &unrollOpts.start},
		{Name: "end", Kind: command.Int, Doc: "the layer after the last one of the loop", Target: &unrollOpts.end},
		{Name: "passes", Kind: command.Int, Default: 2, Doc: "how many times the layers start..end run", Target: &unrollOpts.passes},
	},
	Run: func(_ context.Context, _ command.Invocation) error { return runUnroll(&unrollOpts) },
}

func runUnroll(o *unrollOptions) error {
	info, err := os.Stat(o.src)
	if err != nil {
		return err
	}
	var layers int
	var write func(dst, src string, plan loop.Plan) error
	switch {
	case filepath.Ext(o.src) == ".gguf":
		layers, err = loop.GGUFLayers(o.src)
		write = loop.UnrollGGUF
	case info.IsDir():
		layers, err = loop.ConfigLayers(filepath.Join(o.src, "config.json"))
		write = loop.UnrollCheckpoint
	default:
		return fmt.Errorf("--src %s is neither a .gguf file nor a directory", o.src)
	}
	if err != nil {
		return err
	}
	plan, err := loop.NewPlan(layers, o.start, o.end, o.passes)
	if err != nil {
		return err
	}
	if err := write(o.dst, o.src, plan); err != nil {
		return err
	}
	fmt.Printf("%s: %d layers, layers %d..%d run %d times, %d applications -> %s\n",
		o.src, layers, o.start, o.end, o.passes, plan.Applications(), o.dst)
	return nil
}
