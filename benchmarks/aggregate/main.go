// aggregate reads hyperfine JSON results and emits BENCHMARKS.md,
// results/summary.csv, and results/chart.mmd.
//
// Usage: go run ./aggregate/ <results-dir>
// Writes BENCHMARKS.md to stdout; csv/mmd alongside results-dir.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// hyperfineOutput is the JSON schema emitted by hyperfine --export-json.
type hyperfineOutput struct {
	Results []struct {
		Command string    `json:"command"`
		Mean    float64   `json:"mean"`
		Stddev  float64   `json:"stddev"`
		Median  float64   `json:"median"`
		Min     float64   `json:"min"`
		Max     float64   `json:"max"`
		Times   []float64 `json:"times"`
	} `json:"results"`
}

type benchKey struct {
	fixture  string
	size     int
	tool     string
	daemon   string
	scenario string
}

type benchResult struct {
	key      benchKey
	minMS    float64
	meanMS   float64
	medianMS float64
	stddevMS float64
	p99MS    float64
	runs     int
}

func p99(times []float64) float64 {
	if len(times) == 0 {
		return 0
	}
	s := make([]float64, len(times))
	copy(s, times)
	sort.Float64s(s)
	idx := int(math.Ceil(0.99*float64(len(s)))) - 1
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

func parseFilename(name string) (benchKey, bool) {
	// Format: <fixture>-<size>-<tool>-<daemon>-<scenarioID>.json
	base := strings.TrimSuffix(name, ".json")
	parts := strings.Split(base, "-")
	if len(parts) < 5 {
		return benchKey{}, false
	}
	// scenario is the last part, daemon is second-to-last, tool may contain hyphens
	// but in practice tool names are single words; daemon is "daemon"/"daemonless"
	scenario := parts[len(parts)-1]
	daemon := parts[len(parts)-2]
	fixture := parts[0]
	sizeStr := parts[1]
	tool := strings.Join(parts[2:len(parts)-2], "-")

	size, err := strconv.Atoi(sizeStr)
	if err != nil {
		// polyglot has no numeric size; use 0
		size = 0
	}

	return benchKey{
		fixture:  fixture,
		size:     size,
		tool:     tool,
		daemon:   daemon,
		scenario: scenario,
	}, true
}

func loadResult(path string) (*benchResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out hyperfineOutput
	if err := json.NewDecoder(f).Decode(&out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(out.Results) == 0 {
		return nil, fmt.Errorf("no results in %s", path)
	}
	r := out.Results[0]
	key, ok := parseFilename(filepath.Base(path))
	if !ok {
		return nil, fmt.Errorf("unparseable filename: %s", filepath.Base(path))
	}
	return &benchResult{
		key:      key,
		minMS:    r.Min * 1000,
		meanMS:   r.Mean * 1000,
		medianMS: r.Median * 1000,
		stddevMS: r.Stddev * 1000,
		p99MS:    p99(r.Times) * 1000,
		runs:     len(r.Times),
	}, nil
}

var scenarioNames = map[string]string{
	"S1": "Startup overhead (`--version`)",
	"S2": "Project discovery",
	"S3": "Affected dry-run (1 file changed)",
	"S4": "Cold build, parallel",
	"S5": "Warm cache replay",
	"S6": "One leaf file changed",
	"S7": "One upstream lib changed",
}

var scenarioOrder = []string{"S1", "S2", "S3", "S4", "S5", "S6", "S7"}

func fmtMS(ms float64) string {
	if ms < 1 {
		return fmt.Sprintf("%.2f", ms)
	}
	return fmt.Sprintf("%d", int(math.Round(ms)))
}

func sysInfo() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Date: %s\n", time.Now().UTC().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Go: %s\n", runtime.Version()))
	if out, err := exec.Command("uname", "-a").Output(); err == nil {
		sb.WriteString(fmt.Sprintf("Kernel: %s", strings.TrimSpace(string(out))))
		sb.WriteString("\n")
	}
	// CPU model from /proc/cpuinfo
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "model name") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					sb.WriteString(fmt.Sprintf("CPU: %s\n", strings.TrimSpace(parts[1])))
					break
				}
			}
		}
	}
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal") {
				sb.WriteString(fmt.Sprintf("RAM: %s\n", strings.TrimSpace(line)))
				break
			}
		}
	}
	if out, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
		sb.WriteString(fmt.Sprintf("magus commit: %s", strings.TrimSpace(string(out))))
		sb.WriteString("\n")
	}
	return sb.String()
}

func readVersionsLock(resultsDir string) string {
	// Try to find versions.lock relative to results dir
	candidates := []string{
		filepath.Join(filepath.Dir(resultsDir), "versions.lock"),
		filepath.Join(resultsDir, "..", "versions.lock"),
	}
	for _, c := range candidates {
		if data, err := os.ReadFile(c); err == nil {
			var sb strings.Builder
			for _, line := range strings.Split(string(data), "\n") {
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				sb.WriteString("  " + line + "\n")
			}
			return sb.String()
		}
	}
	return ""
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: aggregate <results-dir>")
		os.Exit(1)
	}
	resultsDir := os.Args[1]

	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", resultsDir, err)
		os.Exit(1)
	}

	var results []*benchResult
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, err := loadResult(filepath.Join(resultsDir, e.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", e.Name(), err)
			continue
		}
		results = append(results, r)
	}

	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "no results found")
		os.Exit(1)
	}

	// Group by (fixture, size, scenario)
	type groupKey struct {
		fixture  string
		size     int
		scenario string
	}
	groups := make(map[groupKey][]*benchResult)
	for _, r := range results {
		k := groupKey{r.key.fixture, r.key.size, r.key.scenario}
		groups[k] = append(groups[k], r)
	}

	// Collect and sort group keys
	var gkeys []groupKey
	for k := range groups {
		gkeys = append(gkeys, k)
	}
	sort.Slice(gkeys, func(i, j int) bool {
		a, b := gkeys[i], gkeys[j]
		if a.fixture != b.fixture {
			return a.fixture < b.fixture
		}
		if a.size != b.size {
			return a.size < b.size
		}
		// scenario order
		si, sj := scenarioIdx(a.scenario), scenarioIdx(b.scenario)
		return si < sj
	})

	var md strings.Builder
	md.WriteString("# magus benchmarks\n\n")
	md.WriteString("Head-to-head: magus vs turbo, nx, lage, moon, bazel, make.\n\n")
	md.WriteString("## Environment\n\n```\n")
	md.WriteString(sysInfo())
	md.WriteString("```\n\n")

	vl := readVersionsLock(resultsDir)
	if vl != "" {
		md.WriteString("### Tool versions\n\n```\n")
		md.WriteString(vl)
		md.WriteString("```\n\n")
	}

	md.WriteString("---\n\n")

	// Per-scenario tables
	prevFixture := ""
	prevSize := -1
	for _, gk := range gkeys {
		if gk.fixture != prevFixture || gk.size != prevSize {
			sizeStr := strconv.Itoa(gk.size)
			if gk.size == 0 {
				sizeStr = "fixed"
			}
			md.WriteString(fmt.Sprintf("## Fixture: %s (N=%s)\n\n", gk.fixture, sizeStr))
			prevFixture = gk.fixture
			prevSize = gk.size
		}

		scenarioName := scenarioNames[gk.scenario]
		if scenarioName == "" {
			scenarioName = gk.scenario
		}
		md.WriteString(fmt.Sprintf("### %s: %s\n\n", gk.scenario, scenarioName))
		md.WriteString("| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |\n")
		md.WriteString("| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |\n")

		rows := groups[gk]
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].minMS < rows[j].minMS
		})
		for _, r := range rows {
			daemon := r.key.daemon
			if daemon == "daemonless" {
				daemon = "off"
			} else if daemon == "daemon" {
				daemon = "on"
			}
			md.WriteString(fmt.Sprintf(
				"| %-10s | %-10s | %8s | %9s | %11s | %6s | %8s | %4d |\n",
				r.key.tool, daemon,
				fmtMS(r.minMS), fmtMS(r.meanMS), fmtMS(r.medianMS),
				fmtMS(r.stddevMS), fmtMS(r.p99MS),
				r.runs,
			))
		}
		md.WriteString("\n")
	}

	fmt.Print(md.String())

	// Write CSV
	csvPath := filepath.Join(resultsDir, "summary.csv")
	csvF, err := os.Create(csvPath)
	if err == nil {
		fmt.Fprintln(csvF, "fixture,size,scenario,tool,daemon,min_ms,mean_ms,median_ms,stddev_ms,p99_ms,runs")
		for _, r := range results {
			fmt.Fprintf(
				csvF, "%s,%d,%s,%s,%s,%.2f,%.2f,%.2f,%.2f,%.2f,%d\n",
				r.key.fixture, r.key.size, r.key.scenario,
				r.key.tool, r.key.daemon,
				r.minMS, r.meanMS, r.medianMS, r.stddevMS, r.p99MS,
				r.runs,
			)
		}
		csvF.Close()
		fmt.Fprintf(os.Stderr, "wrote %s\n", csvPath)
	}

	// Write S5 chart (warm cache) for README embedding
	writeMermaidChart(results, resultsDir)
}

func writeMermaidChart(results []*benchResult, resultsDir string) {
	// Find the fixture+size with the most tools for S5
	type fsKey struct {
		fixture string
		size    int
	}
	toolsByGroup := make(map[fsKey][]string)
	minByGroup := make(map[string]float64) // key: fixture-size-tool, value: min ms

	for _, r := range results {
		if r.key.scenario != "S5" || r.key.daemon != "daemonless" {
			continue
		}
		k := fsKey{r.key.fixture, r.key.size}
		toolsByGroup[k] = append(toolsByGroup[k], r.key.tool)
		mk := fmt.Sprintf("%s-%d-%s", r.key.fixture, r.key.size, r.key.tool)
		if _, ok := minByGroup[mk]; !ok || r.minMS < minByGroup[mk] {
			minByGroup[mk] = r.minMS
		}
	}

	if len(toolsByGroup) == 0 {
		return
	}

	// Pick the group with the most tools
	var best fsKey
	for k, tools := range toolsByGroup {
		if len(tools) > len(toolsByGroup[best]) {
			best = k
		}
	}

	tools := toolsByGroup[best]
	// Deduplicate
	seen := map[string]bool{}
	var uniqTools []string
	for _, t := range tools {
		if !seen[t] {
			seen[t] = true
			uniqTools = append(uniqTools, t)
		}
	}
	sort.Slice(uniqTools, func(i, j int) bool {
		ki := fmt.Sprintf("%s-%d-%s", best.fixture, best.size, uniqTools[i])
		kj := fmt.Sprintf("%s-%d-%s", best.fixture, best.size, uniqTools[j])
		return minByGroup[ki] < minByGroup[kj]
	})

	var sb strings.Builder
	sizeStr := strconv.Itoa(best.size)
	if best.size == 0 {
		sizeStr = "fixed"
	}
	sb.WriteString(fmt.Sprintf("```mermaid\nxychart-beta\n    title \"S5: Warm Cache Replay (%s, N=%s)\"\n", best.fixture, sizeStr))
	var toolLabels []string
	var vals []string
	for _, t := range uniqTools {
		toolLabels = append(toolLabels, fmt.Sprintf("%q", t))
		mk := fmt.Sprintf("%s-%d-%s", best.fixture, best.size, t)
		vals = append(vals, fmtMS(minByGroup[mk]))
	}
	sb.WriteString(fmt.Sprintf("    x-axis [%s]\n", strings.Join(toolLabels, ", ")))
	sb.WriteString("    y-axis \"time (ms)\"\n")
	sb.WriteString(fmt.Sprintf("    bar [%s]\n", strings.Join(vals, ", ")))
	sb.WriteString("```\n")

	chartPath := filepath.Join(resultsDir, "chart.mmd")
	if err := os.WriteFile(chartPath, []byte(sb.String()), 0o644); err == nil {
		fmt.Fprintf(os.Stderr, "wrote %s\n", chartPath)
	}
}

func scenarioIdx(s string) int {
	for i, sc := range scenarioOrder {
		if sc == s {
			return i
		}
	}
	return len(scenarioOrder)
}
