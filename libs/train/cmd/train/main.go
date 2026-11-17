// Command train runs the stage S0 probes of the local-models plan over the inputs
// libs/train/inputs.jsonl pins, and appends one row per run to libs/train/runs.jsonl.
// It never serves a model: mlx_lm.lora, llama-bench and llama-server do the model work,
// and this command measures them.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/egladman/magus/libs/train/embed/teacher"
	"github.com/egladman/magus/libs/train/eval"
	"github.com/egladman/magus/libs/train/probe"
	"github.com/egladman/magus/libs/xdg"
)

const usage = `usage: train <command> [flags]

commands:
  fertility   append tokens per byte of held-in hand-written Buzz, Go and TS under each tokenizer
  pilot       run one mlx_lm.lora pilot on train-split Buzz and append its throughput and memory
  gguf-bench  run llama-bench (pp512, tg128) over each pinned GGUF and append its rates
  dedup       find exact and near-duplicate files among the split's candidates (stage S1)
  pin-split   pin the split split.buzz wrote in gen/split.digest (stage S1)
  eval-sets   write the FIM dev and test sets and the Buzz pass@k tasks (stage S1)
  fim         write the FIM training samples, decontaminated against the eval sets (stage S1)
  g1          recount the G1 bullets from the datasets and write the report (stage S1)
  eval-complete  serve each base with llama-server, complete the FIM test sets and score them (stage S3)
  pass-at-k   sample the Buzz pass@k tasks from each base and grade them with magus buzz -t (stage S3)
  latency     replay FIM-Buzz dev spans in typing order against each base (stage S3)

run "train <command> -h" for a command's flags`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "fertility":
		err = runFertility(os.Args[2:])
	case "pilot":
		err = runPilot(os.Args[2:])
	case "gguf-bench":
		err = runBench(os.Args[2:])
	case "dedup", "pin-split", "eval-sets", "fim", "g1":
		err = runS1(os.Args[1], os.Args[2:])
	case "eval-complete", "pass-at-k", "latency":
		err = runS3(os.Args[1], os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "train %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

type common struct {
	inputs, cache, runs, sources, root string
}

func (c *common) register(fs *flag.FlagSet) {
	cache := ""
	if dir, err := xdg.CacheDir(); err == nil {
		cache = filepath.Join(dir, "magus")
	}
	fs.StringVar(&c.inputs, "inputs", "inputs.jsonl", "the pinned inputs file")
	fs.StringVar(&c.cache, "cache", cache, "the cache root inputs.jsonl paths are relative to")
	fs.StringVar(&c.runs, "runs", "runs.jsonl", "the run log rows are appended to")
	fs.StringVar(&c.sources, "sources", "embed/gen/sources.jsonl", "the dealt split of tracked files")
	fs.StringVar(&c.root, "root", "../..", "the workspace root sources.jsonl paths are relative to")
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

func runFertility(args []string) error {
	fs := flag.NewFlagSet("fertility", flag.ExitOnError)
	var c common
	c.register(fs)
	tokenizers := fs.String("tokenizers", "qwen2.5-coder-0.5b/tokenizer.json,mellum2-12b-a2.5b-base/tokenizer.json", "comma-separated input names of tokenizer.json files")
	_ = fs.Parse(args)
	inputs, err := probe.ReadInputs(c.inputs)
	if err != nil {
		return err
	}
	texts, sourcesSum, stale, err := c.heldIn("buzz", "go", "typescript")
	if err != nil {
		return err
	}
	for _, name := range strings.Split(*tokenizers, ",") {
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

func runPilot(args []string) error {
	fs := flag.NewFlagSet("pilot", flag.ExitOnError)
	var c common
	c.register(fs)
	config := fs.String("config", "", "the committed mlx-lm YAML, e.g. config/pilot-qwen2.5-coder-0.5b-full.yaml")
	model := fs.String("model", "", "the input name prefix of the model directory, e.g. qwen2.5-coder-0.5b/")
	maxBytes := fs.Int("max-bytes", 12000, "chunk size; Buzz runs about 3 bytes a token, so 12000 bytes fill most of a 4,096-token sequence")
	validEvery := fs.Int("valid-every", 25, "every n-th chunk goes to valid.jsonl")
	_ = fs.Parse(args)
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

func runBench(args []string) error {
	fs := flag.NewFlagSet("gguf-bench", flag.ExitOnError)
	var c common
	c.register(fs)
	prefix := fs.String("prefix", "gguf/", "benchmark every pinned .gguf whose name starts with this")
	reps := fs.String("r", "5", "llama-bench repetitions per test")
	_ = fs.Parse(args)
	inputs, err := probe.ReadInputs(c.inputs)
	if err != nil {
		return err
	}
	pinned, err := probe.WithPrefix(inputs, *prefix)
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
			"-p", "512", "-n", "128", "-r", *reps, "-o", "json")
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

// runS1 runs one stage S1 command. Every dataset goes under the cache root's train/s1
// directory; only the digests it pins reach git.
func runS1(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var c common
	c.register(fs)
	rev := fs.String("rev", "HEAD", "the commit the split, the dedup and the test sets read")
	digests := fs.String("digests", "gen/split.digest", "the digest file datasets are pinned in")
	binary := fs.String("magus", "../../magus", "the magus binary --check runs with")
	_ = fs.Parse(args)
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

// s3Tools binds the flags that name binaries and logs.
func s3Tools(fs *flag.FlagSet, cfg *eval.Config) {
	fs.StringVar(&cfg.Run, "run", "", "the run name: its predictions and scratch trees live under <cache>/tmp/codemodel-eval/<run>, and a rerun with the same name resumes")
	fs.StringVar(&cfg.Llama, "llama-server", "llama-server", "the llama-server binary, built at the pinned commit")
	fs.StringVar(&cfg.Magus, "magus", "../../magus", "the magus binary --check and -t run with")
	fs.StringVar(&cfg.Evals, "evals", "evals.jsonl", "the evaluation log rows are appended to")
	fs.StringVar(&cfg.Digests, "digests", "gen/split.digest", "the digest file datasets are pinned in")
}

// s3Models binds the flags that name what is served.
func s3Models(fs *flag.FlagSet, cfg *eval.Config, defaultSystems string) {
	fs.StringVar(&cfg.Systems, "systems", defaultSystems, "comma-separated base names under gguf/ in inputs.jsonl, each name[:psm|:spm]; the first is the reference the others' p-values pair against")
	fs.StringVar(&cfg.Context, "context", "none", "the context provider in front of each base; none until stage S2 builds one, and any other name needs spans that carry context chunks")
}

// s3Flags binds the flags the three stage S3 commands share into an eval.Config. The
// returned function parses args and then copies the shared flags in.
func s3Flags(fs *flag.FlagSet, defaultSystems string) (*eval.Config, func(args []string)) {
	var c common
	c.register(fs)
	cfg := &eval.Config{}
	s3Tools(fs, cfg)
	s3Models(fs, cfg, defaultSystems)
	return cfg, func(args []string) {
		_ = fs.Parse(args)
		cfg.Inputs, cfg.Cache, cfg.Runs, cfg.Root = c.inputs, c.cache, c.runs, c.root
	}
}

func runS3(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	ctx := context.Background()
	switch cmd {
	case "eval-complete":
		cfg, parse := s3Flags(fs, "qwen2.5-coder-0.5b:spm,qwen2.5-coder-1.5b:spm,mellum-4b-base:spm")
		sets := fs.String("sets", "buzz-test,go-test,typescript-test", "comma-separated <language>-<test|dev> sets")
		max := fs.String("max", "", "comma-separated set=N caps on a set's span count, kept by span id order; absent keeps the whole set")
		parse(args)
		e, systems, err := cfg.Open()
		if err != nil {
			return err
		}
		specs, err := eval.ParseSets(*sets, *max)
		if err != nil {
			return err
		}
		return e.CompleteAll(ctx, systems, specs, os.Stderr)
	case "pass-at-k":
		cfg, parse := s3Flags(fs, "qwen2.5-coder-0.5b:spm,qwen2.5-coder-1.5b:spm")
		tasks := fs.Int("tasks", 0, "sample and grade only this many tasks, by id order; 0 takes all")
		parse(args)
		e, systems, err := cfg.Open()
		if err != nil {
			return err
		}
		return e.PassAll(ctx, systems, *tasks, os.Stderr)
	default:
		cfg, parse := s3Flags(fs, "qwen2.5-coder-0.5b:spm,qwen2.5-coder-1.5b:spm")
		n := fs.Int("n", 1000, "replay this many dev spans in typing order, or all of them when the set is smaller")
		set := fs.String("set", "buzz-dev", "the dev set to replay")
		parse(args)
		e, systems, err := cfg.Open()
		if err != nil {
			return err
		}
		return e.LatencyAll(ctx, systems, *set, *n, os.Stderr)
	}
}
