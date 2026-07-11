package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

type leafEntry struct {
	target   string        // target name
	duration time.Duration // zero when StartedAt was unset upstream
	step     string        // current cache step label, e.g. "archive.uncompress foo.tar.zst [4×]"
}

const (
	runningLineWidth = 70 // max chars per leaf line before truncation
)

type cellKind int

const (
	cellRunning        cellKind = iota // running slot
	cellIdle                           // capacity slot, not running
	cellOutOfPool                      // CPU thread outside configured capacity
	cellOverSubscribed                 // configured capacity slot beyond NumCPU
)

func cellState(i, running, capacity, numCPU int) cellKind {
	isRunning := i < running
	inPool := i < capacity
	inMachine := i < numCPU

	switch {
	case isRunning:
		return cellRunning
	case inPool && !inMachine:
		return cellOverSubscribed
	case inPool:
		return cellIdle
	default:
		return cellOutOfPool
	}
}

const (
	ansiReset       = "\x1b[0m"
	ansiBrightGreen = "\x1b[1;32m"
	ansiDimGrey     = "\x1b[2;37m"
	ansiYellow      = "\x1b[33m"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// drawPoolGrid renders the dot-matrix pool visualization; animFrame drives the pulse (0 = static).
func drawPoolGrid(w io.Writer, pool *types.StatusOutput, numCPU int, animFrame int) {
	total := numCPU
	if pool.Capacity > total {
		total = pool.Capacity
	}
	if total == 0 {
		return
	}

	// Compute grid dimensions: up to 8 cols per row.
	cols := 8
	if total < cols {
		cols = total
	}
	rows := (total + cols - 1) / cols

	// Header: mode-aware label + counts. The header is static; the only
	// animated element is the spinner attached to the "running" subtree.
	fmt.Fprintln(w, poolHeader(pool, numCPU))
	fmt.Fprintln(w)

	// Grid rows. No per-cell animation — the header carries no motion
	// and the spinner lives on the running tree below.
	for r := 0; r < rows; r++ {
		var sb strings.Builder
		sb.WriteString("  ")
		for c := 0; c < cols; c++ {
			i := r*cols + c
			if i >= total {
				sb.WriteString("  ")
				continue
			}
			kind := cellState(i, pool.Running, pool.Capacity, numCPU)
			switch kind {
			case cellRunning:
				sb.WriteString(ansiBrightGreen + "●" + ansiReset + " ")
			case cellIdle:
				sb.WriteString("○ ")
			case cellOutOfPool:
				sb.WriteString(ansiDimGrey + "·" + ansiReset + " ")
			case cellOverSubscribed:
				sb.WriteString(ansiYellow + "●" + ansiReset + " ")
			}
		}
		fmt.Fprintln(w, sb.String())
	}

	if len(pool.RunningTargets) > 0 {
		spinner := spinnerFrames[animFrame%len(spinnerFrames)]
		fmt.Fprintf(w, "\n  %s running\n", spinner)
		drawRunningTree(w, pool.RunningTargets, time.Now())
	}

	fmt.Fprintf(w, "\n  %s●%s running  ○ idle  %s·%s cpu\n",
		ansiBrightGreen, ansiReset, ansiDimGrey, ansiReset)
}

func poolHeader(pool *types.StatusOutput, numCPU int) string {
	label := "pool"
	if pool.Mode == "daemon" {
		label = "daemon"
	}
	parts := []string{label}
	parts = append(parts, fmt.Sprintf("pid %d", pool.ParentPID))
	if pool.DaemonVersion != "" {
		parts = append(parts, pool.DaemonVersion)
	}
	parts = append(parts, fmt.Sprintf("%d/%d running", pool.Running, pool.Capacity))
	parts = append(parts, fmt.Sprintf("%d cpu", numCPU))
	out := strings.Join(parts, " · ")
	if pool.Queued > 0 {
		out += fmt.Sprintf("  (+%d queued)", pool.Queued)
	}
	return out
}

// drawRunningTree renders running targets grouped by workspace → project → target; collapses workspace when single.
func drawRunningTree(w io.Writer, running []types.StatusRunningTarget, now time.Time) {
	const indent = "  "
	// Group: workspace → project → []leafEntry
	type projMap map[string][]leafEntry
	wsGroups := map[string]projMap{}
	for _, e := range running {
		project, target := parseRunning(e.Args)
		if project == "" && target == "" {
			project = "(?)"
			target = truncate(strings.Join(e.Args, " "), runningLineWidth)
		}
		ws := workspaceLabel(e.Workspace)
		var dur time.Duration
		if !e.StartedAt.IsZero() {
			dur = now.Sub(e.StartedAt)
		}
		if wsGroups[ws] == nil {
			wsGroups[ws] = projMap{}
		}
		wsGroups[ws][project] = append(wsGroups[ws][project], leafEntry{target: target, duration: dur, step: e.Step})
	}

	wsKeys := make([]string, 0, len(wsGroups))
	for k := range wsGroups {
		wsKeys = append(wsKeys, k)
	}
	sort.Strings(wsKeys)

	showWorkspace := len(wsKeys) > 1

	if !showWorkspace {
		drawProjectTree(w, indent, wsGroups[wsKeys[0]])
		return
	}

	for i, ws := range wsKeys {
		wsLast := i == len(wsKeys)-1
		wsPrefix, childPrefix := branchPrefix(indent, wsLast)
		fmt.Fprintf(w, "%s%s\n", wsPrefix, ws)
		drawProjectTree(w, childPrefix, wsGroups[ws])
	}
}

func drawProjectTree(w io.Writer, indent string, projects map[string][]leafEntry) {
	projKeys := make([]string, 0, len(projects))
	for k := range projects {
		projKeys = append(projKeys, k)
	}
	sort.Strings(projKeys)

	for i, p := range projKeys {
		pLast := i == len(projKeys)-1
		pPrefix, vIndent := branchPrefix(indent, pLast)
		label := p
		if label == "" {
			label = "(all)"
		}
		fmt.Fprintf(w, "%s%s\n", pPrefix, label)

		leaves := projects[p]
		// Stable order: oldest first, then target name.
		sort.SliceStable(leaves, func(a, b int) bool {
			if leaves[a].duration != leaves[b].duration {
				return leaves[a].duration > leaves[b].duration
			}
			return leaves[a].target < leaves[b].target
		})
		for j, lf := range leaves {
			vLast := j == len(leaves)-1
			vPrefix, actIndent := branchPrefix(vIndent, vLast)
			line := truncate(lf.target, runningLineWidth)
			if d := formatDur(lf.duration); d != "" {
				line += " " + ansiDimGrey + "(" + d + ")" + ansiReset
			}
			fmt.Fprintf(w, "%s%s\n", vPrefix, line)
			if lf.step != "" {
				actPrefix, _ := branchPrefix(actIndent, true)
				fmt.Fprintf(w, "%s%s%s%s\n", actPrefix, ansiDimGrey, truncate(lf.step, runningLineWidth), ansiReset)
			}
		}
	}
}

// formatDur renders a wall-clock running duration. Returns "" for zero
// or negative durations (unset upstream / clock skew).
func formatDur(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	switch {
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		return fmt.Sprintf("%dh%dm", h, m)
	}
}

func branchPrefix(indent string, last bool) (string, string) {
	if last {
		return indent + "└── ", indent + "    "
	}
	return indent + "├── ", indent + "│   "
}

func workspaceLabel(root string) string {
	if root == "" {
		return "(unknown)"
	}
	return filepath.Base(root)
}

func parseRunning(args []string) (project, target string) {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i++
	}
	if i >= len(args) {
		return "", ""
	}
	subcmd := args[i]
	i++

	switch subcmd {
	case "run":
		// magus run <target[:modes]> [project ...]
		if i < len(args) {
			target = args[i]
			if t, err := types.ParseTarget(args[i]); err == nil {
				target = t.Name
			}
			i++
		}
		if i < len(args) {
			project = args[i]
		}
		return project, target
	case "build", "test", "lint", "format", "gen", "watch":
		// magus <target> [project]
		target = subcmd
		if i < len(args) {
			project = args[i]
		}
		return project, target
	default:
		target = subcmd
		if i < len(args) {
			project = args[i]
		}
		return project, target
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
