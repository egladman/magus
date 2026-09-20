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
		Command   string    `json:"command"`
		Mean      float64   `json:"mean"`
		Stddev    float64   `json:"stddev"`
		Median    float64   `json:"median"`
		Min       float64   `json:"min"`
		Max       float64   `json:"max"`
		Times     []float64 `json:"times"`
		ExitCodes []int     `json:"exit_codes"`
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
	// failExit is the first non-zero exit code hyperfine recorded, 0 when every
	// run succeeded. bench.sh passes --ignore-failure, so a tool that crashes
	// still yields a timing; without this the crash reads as the fastest result
	// in the table.
	failExit  int
	failCount int
}

func (r *benchResult) failed() bool { return r.failCount > 0 }

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
		return nil, fmt.Errorf("unparsable filename: %s", filepath.Base(path))
	}
	failExit, failCount := 0, 0
	for _, code := range r.ExitCodes {
		if code == 0 {
			continue
		}
		failCount++
		if failExit == 0 {
			failExit = code
		}
	}
	return &benchResult{
		key:       key,
		minMS:     r.Min * 1000,
		meanMS:    r.Mean * 1000,
		medianMS:  r.Median * 1000,
		stddevMS:  r.Stddev * 1000,
		p99MS:     p99(r.Times) * 1000,
		runs:      len(r.Times),
		failExit:  failExit,
		failCount: failCount,
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

// unknown is what every environment field reports when its source is
// unreadable. A published table that silently drops a line reads as if the
// field did not apply, which is a stronger claim than "we could not tell".
const unknown = "unknown"

// cpuModel reports the CPU model, per-OS. Linux reads /proc/cpuinfo; darwin has
// no procfs, so the same read there used to drop the line entirely.
func cpuModel() string {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return s
			}
		}
	case "linux":
		if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if !strings.HasPrefix(line, "model name") {
					continue
				}
				if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
					return strings.TrimSpace(parts[1])
				}
			}
		}
	}
	return unknown
}

// memTotal reports total RAM, per-OS. See cpuModel for the darwin gap.
func memTotal() string {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
			if n, convErr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); convErr == nil && n > 0 {
				return fmt.Sprintf("%d kB", n/1024)
			}
		}
	case "linux":
		if data, err := os.ReadFile("/proc/meminfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if !strings.HasPrefix(line, "MemTotal") {
					continue
				}
				// Report the value alone, so the published line reads the same
				// shape on every OS.
				if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
					return strings.TrimSpace(parts[1])
				}
			}
		}
	}
	return unknown
}

// capture runs bin and returns its first output line, or unknown.
func capture(bin string, args ...string) string {
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		return unknown
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if line == "" {
		return unknown
	}
	return line
}

func sysInfo() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Date: %s\n", time.Now().UTC().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Go: %s\n", runtime.Version()))
	sb.WriteString(fmt.Sprintf("Kernel: %s\n", capture("uname", "-a")))
	sb.WriteString(fmt.Sprintf("CPU: %s\n", cpuModel()))
	sb.WriteString(fmt.Sprintf("CPU cores: %d\n", runtime.NumCPU()))
	sb.WriteString(fmt.Sprintf("RAM: %s\n", memTotal()))
	sb.WriteString(fmt.Sprintf("magus commit: %s\n", capture("git", "rev-parse", "HEAD")))
	return sb.String()
}

// headline states which tools and fixtures this run actually measured. The
// fixed line it replaced named all seven tools of the suite whether or not they
// contributed a single row.
func headline(results []*benchResult) string {
	toolSeen, fixSeen := map[string]bool{}, map[string]bool{}
	var tools, fixtures []string
	for _, r := range results {
		if !toolSeen[r.key.tool] {
			toolSeen[r.key.tool] = true
			tools = append(tools, r.key.tool)
		}
		if !fixSeen[r.key.fixture] {
			fixSeen[r.key.fixture] = true
			fixtures = append(fixtures, r.key.fixture)
		}
	}
	sort.Strings(tools)
	sort.Strings(fixtures)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Measured in this run: %s, on the %s fixture(s).\n",
		strings.Join(tools, ", "), strings.Join(fixtures, ", "),
	))
	sb.WriteString("A tool absent from the tables below produced no results here and is\n")
	sb.WriteString("not being compared. A row marked FAILED exited non-zero and is not a\n")
	sb.WriteString("measurement of the work the scenario describes.\n\n")
	return sb.String()
}

// toolVersionArgs names the arguments that make a tool report its own version.
var toolVersionArgs = map[string][]string{
	"magus": {"version"},
	"make":  {"--version"},
	"turbo": {"--version"},
	"nx":    {"--version"},
	"lage":  {"--version"},
	"moon":  {"--version"},
	"bazel": {"--version"},
}

// toolBinary resolves the executable a tool name was measured through. bench.sh
// exports MAGUS_BIN and MAKE_BIN, and on macOS `make` and `gmake` are different
// GNU Make releases, so probing the bare name would record the wrong one.
func toolBinary(tool string) string {
	switch tool {
	case "magus":
		if v := os.Getenv("MAGUS_BIN"); v != "" {
			return v
		}
	case "make":
		if v := os.Getenv("MAKE_BIN"); v != "" {
			return v
		}
	}
	return tool
}

// observedVersions reports the versions of the tools that actually produced
// rows, probed now rather than copied out of versions.lock: the lock states an
// intent, and printing it as a measurement credited five tools with versions in
// a run where they produced nothing.
func observedVersions(results []*benchResult) string {
	seen := map[string]bool{}
	var tools []string
	for _, r := range results {
		if seen[r.key.tool] {
			continue
		}
		seen[r.key.tool] = true
		tools = append(tools, r.key.tool)
	}
	sort.Strings(tools)

	var sb strings.Builder
	sb.WriteString("  hyperfine: " + capture("hyperfine", "--version") + "\n")
	for _, t := range tools {
		args, ok := toolVersionArgs[t]
		if !ok {
			sb.WriteString("  " + t + ": " + unknown + " (no version probe)\n")
			continue
		}
		bin := toolBinary(t)
		label := t
		if bin != t {
			label = fmt.Sprintf("%s (%s)", t, bin)
		}
		sb.WriteString("  " + label + ": " + capture(bin, args...) + "\n")
	}
	return sb.String()
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
	md.WriteString(headline(results))
	md.WriteString("## Environment\n\n```text\n")
	md.WriteString(sysInfo())
	md.WriteString("```\n\n")

	md.WriteString("### Tool versions (observed)\n\n```text\n")
	md.WriteString(observedVersions(results))
	md.WriteString("```\n\n")

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
		// A failed run's timing measures the crash, not the build, so it never
		// competes for the fastest slot: failures sort last regardless of time.
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].failed() != rows[j].failed() {
				return !rows[i].failed()
			}
			return rows[i].minMS < rows[j].minMS
		})
		var notes []string
		for _, r := range rows {
			daemon := r.key.daemon
			if daemon == "daemonless" {
				daemon = "off"
			} else if daemon == "daemon" {
				daemon = "on"
			}
			if r.failed() {
				md.WriteString(fmt.Sprintf(
					"| %-10s | %-10s | %8s | %9s | %11s | %6s | %8s | %4d |\n",
					r.key.tool, daemon,
					"FAILED", "FAILED", "FAILED", "FAILED", "FAILED",
					r.runs,
				))
				notes = append(notes, fmt.Sprintf(
					"`%s` (daemon %s) exited %d in %d of %d runs; timings withheld.",
					r.key.tool, daemon, r.failExit, r.failCount, r.runs,
				))
				continue
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
		for _, n := range notes {
			md.WriteString("> " + n + "\n")
		}
		if len(notes) > 0 {
			md.WriteString("\n")
		}
	}

	fmt.Print(md.String())

	// Write CSV
	csvPath := filepath.Join(resultsDir, "summary.csv")
	csvF, err := os.Create(csvPath)
	if err == nil {
		fmt.Fprintln(csvF, "fixture,size,scenario,tool,daemon,min_ms,mean_ms,median_ms,stddev_ms,p99_ms,runs,failed_runs,exit_code")
		for _, r := range results {
			fmt.Fprintf(
				csvF, "%s,%d,%s,%s,%s,%.2f,%.2f,%.2f,%.2f,%.2f,%d,%d,%d\n",
				r.key.fixture, r.key.size, r.key.scenario,
				r.key.tool, r.key.daemon,
				r.minMS, r.meanMS, r.medianMS, r.stddevMS, r.p99MS,
				r.runs, r.failCount, r.failExit,
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
		if r.key.scenario != "S5" || r.key.daemon != "daemonless" || r.failed() {
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
