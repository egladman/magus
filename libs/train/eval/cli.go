package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/train/completion/fim"
	"github.com/egladman/magus/libs/train/probe"
)

// Config is what the stage S3 commands' flags name.
type Config struct {
	Run     string
	Llama   string
	Magus   string
	Evals   string
	Runs    string
	Inputs  string
	Cache   string
	Root    string
	Digests string
	Systems string
	Context string
}

// ParseSystems reads comma-separated name[:psm|:spm] specs; an absent order is spm, the
// order plan 4.2 serves in.
func ParseSystems(inputs []probe.Input, cache, specs, context string) ([]System, error) {
	var out []System
	for _, spec := range strings.Split(specs, ",") {
		name, order, _ := strings.Cut(spec, ":")
		if order == "" {
			order = string(fim.SPM)
		}
		sys, err := ResolveSystem(inputs, cache, name, fim.Order(order), context)
		if err != nil {
			return nil, err
		}
		out = append(out, sys)
	}
	return out, nil
}

func gitHead(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Open resolves the config into the S3 environment and the systems it names.
func (c Config) Open() (S3, []System, error) {
	if c.Run == "" {
		return S3{}, nil, errors.New("-run names the run: its predictions persist under that name")
	}
	inputs, err := probe.ReadInputs(c.Inputs)
	if err != nil {
		return S3{}, nil, err
	}
	systems, err := ParseSystems(inputs, c.Cache, c.Systems, c.Context)
	if err != nil {
		return S3{}, nil, err
	}
	cutoff, err := ReadCutoff(c.Inputs)
	if err != nil {
		return S3{}, nil, err
	}
	head, err := gitHead(c.Root)
	if err != nil {
		return S3{}, nil, err
	}
	magus, err := filepath.Abs(c.Magus)
	if err != nil {
		return S3{}, nil, err
	}
	llama, err := exec.LookPath(c.Llama)
	if err != nil {
		return S3{}, nil, fmt.Errorf("llama-server (brew install llama.cpp): %w", err)
	}
	env := Env{Root: c.Root, Rev: head, Cutoff: cutoff.Commit, Dir: filepath.Join(c.Cache, "train", "s1"), Digests: c.Digests, Magus: magus}
	return S3{Env: env, Cache: c.Cache, Inputs: c.Inputs, Out: filepath.Join(c.Cache, "tmp", "codemodel-eval", c.Run), Llama: llama,
		Runs: c.Runs, Evals: c.Evals, Run: c.Run, HeadRev: head}, systems, nil
}

// ParseSets reads -sets (<language>-<test|dev>, comma-separated) and -max (set=N pairs)
// into the sets a run covers. A -max naming a set -sets lacks is an error.
func ParseSets(names, max string) ([]SetSpec, error) {
	caps := map[string]int{}
	for _, kv := range strings.Split(max, ",") {
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		n, err := strconv.Atoi(v)
		if !ok || err != nil {
			return nil, fmt.Errorf("-max %q is not set=N", kv)
		}
		caps[k] = n
	}
	var out []SetSpec
	for _, name := range strings.Split(names, ",") {
		lang, _, ok := strings.Cut(name, "-")
		if !ok {
			return nil, fmt.Errorf("set %q is not <language>-<test|dev>", name)
		}
		out = append(out, SetSpec{Name: name, Language: lang, Max: caps[name]})
		delete(caps, name)
	}
	for k := range caps {
		return nil, fmt.Errorf("-max names set %s, which -sets does not", k)
	}
	return out, nil
}

// CompleteAll is the eval-complete command: it takes the heavy lock, loads the sets,
// generates each system's completions and scores them all.
func (e S3) CompleteAll(ctx context.Context, systems []System, specs []SetSpec, log io.Writer) error {
	lock, err := LockHeavy(filepath.Dir(e.Out))
	if err != nil {
		return err
	}
	defer lock.Close()
	var loaded []Set
	sums := map[string]string{}
	for _, spec := range specs {
		set, sum, err := e.LoadSet(spec)
		if err != nil {
			return err
		}
		loaded = append(loaded, set)
		sums[set.Name] = sum
	}
	for _, sys := range systems {
		if err := e.GenerateAll(ctx, sys, loaded, sums, log); err != nil {
			return err
		}
	}
	return e.ScoreAll(systems, loaded, log)
}

// LatencyAll is the latency command: the heavy lock, then each system's replay and its
// speculation comparison.
func (e S3) LatencyAll(ctx context.Context, systems []System, set string, n int, log io.Writer) error {
	lock, err := LockHeavy(filepath.Dir(e.Out))
	if err != nil {
		return err
	}
	defer lock.Close()
	for _, sys := range systems {
		results, err := e.RunLatency(ctx, sys, set, n, log)
		if err != nil {
			return err
		}
		row, err := SpeculationRow(sys, set+"-latency", e.Run, results[""].Wall["warm.single"], results["ngram-simple"].Wall["warm.single"])
		if err != nil {
			return err
		}
		if err := probe.Append(e.Evals, row); err != nil {
			return err
		}
	}
	return nil
}

// PassAll is the pass-at-k command under the heavy lock.
func (e S3) PassAll(ctx context.Context, systems []System, tasks int, log io.Writer) error {
	lock, err := LockHeavy(filepath.Dir(e.Out))
	if err != nil {
		return err
	}
	defer lock.Close()
	return e.PassKAll(ctx, systems, tasks, log)
}
