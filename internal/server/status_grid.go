package server

import (
	"cmp"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/termkit/layout"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/daemon"
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

// The pool grid's palette, as SGR parameter codes. The escape wrapping
// itself lives in tty.Colorize; these only choose the colors.
const (
	sgrPoolRunning = termkit.SGRBrightGreen // a slot doing work
	sgrPoolIdle    = termkit.SGRDimGrey     // a free slot
	sgrPoolQueued  = termkit.SGRYellow      // work waiting for a slot
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// drawPoolGrid renders the dot-matrix pool visualization; animFrame drives the pulse (0 = static).
func drawPoolGrid(w io.Writer, pool *daemon.Output, numCPU int, animFrame int) {
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
				sb.WriteString(termkit.Colorize("●", sgrPoolRunning) + " ")
			case cellIdle:
				sb.WriteString("○ ")
			case cellOutOfPool:
				sb.WriteString(termkit.Colorize("·", sgrPoolIdle) + " ")
			case cellOverSubscribed:
				sb.WriteString(termkit.Colorize("●", sgrPoolQueued) + " ")
			}
		}
		fmt.Fprintln(w, sb.String())
	}

	if len(pool.RunningTargets) > 0 {
		spinner := spinnerFrames[animFrame%len(spinnerFrames)]
		fmt.Fprintf(w, "\n  %s running\n", spinner)
		drawRunningTree(w, pool.RunningTargets, time.Now())
	}

	fmt.Fprintf(w, "\n  %s running  ○ idle  %s cpu\n",
		termkit.Colorize("●", sgrPoolRunning), termkit.Colorize("·", sgrPoolIdle))
}

func poolHeader(pool *daemon.Output, numCPU int) string {
	parts := []string{"pool"}
	parts = append(parts, fmt.Sprintf("pid %d", pool.ParentPID))
	if pool.Version != "" {
		parts = append(parts, pool.Version)
	}
	parts = append(parts, fmt.Sprintf("%d/%d running", pool.Running, pool.Capacity))
	parts = append(parts, fmt.Sprintf("%d available", pool.Available))
	parts = append(parts, fmt.Sprintf("%d cpu", numCPU))
	out := strings.Join(parts, " · ")
	if pool.Queued > 0 {
		out += fmt.Sprintf("  (+%d queued)", pool.Queued)
	}
	return out
}

// drawRunningTree renders running targets grouped by workspace → project → target; collapses workspace when single.
func drawRunningTree(w io.Writer, running []daemon.RunningTarget, now time.Time) {
	const indent = "  "
	// Group: workspace → project → []leafEntry
	type projMap map[string][]leafEntry
	wsGroups := map[string]projMap{}
	for _, e := range running {
		project, target := parseRunning(e.Args)
		if project == "" && target == "" {
			project = "(?)"
			target = Truncate(strings.Join(e.Args, " "), runningLineWidth)
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
	slices.Sort(wsKeys)

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
	slices.Sort(projKeys)

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
		slices.SortStableFunc(leaves, func(a, b leafEntry) int {
			if a.duration != b.duration {
				return cmp.Compare(b.duration, a.duration) // oldest first
			}
			return cmp.Compare(a.target, b.target)
		})
		for j, lf := range leaves {
			vLast := j == len(leaves)-1
			vPrefix, actIndent := branchPrefix(vIndent, vLast)
			line := Truncate(lf.target, runningLineWidth)
			if d := FormatDur(lf.duration); d != "" {
				line += " " + termkit.Colorize(fmt.Sprintf("(%s)", d), sgrPoolIdle)
			}
			fmt.Fprintf(w, "%s%s\n", vPrefix, line)
			if lf.step != "" {
				actPrefix, _ := branchPrefix(actIndent, true)
				fmt.Fprintf(w, "%s%s\n", actPrefix, termkit.Colorize(Truncate(lf.step, runningLineWidth), sgrPoolIdle))
			}
		}
	}
}

// FormatDur renders a wall-clock running duration. Returns "" for zero
// or negative durations (unset upstream / clock skew).
//
// Deliberately NOT internal/cache.fmtDur, which they otherwise resemble enough
// to invite a merge. That one measures how long a target TOOK and resolves to
// nanoseconds, because the difference between 2ms and 200ms is the answer
// somebody is looking for. This one is a clock a reader watches tick, so
// sub-second precision would be unreadable noise, and an unset value has to
// render as nothing rather than as zero.
func FormatDur(d time.Duration) string {
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

// Truncate shortens s to fit n bytes for the running-tree lines.
//
// It delegates to tty.Clip rather than slicing: a raw s[:n-1] splits a
// multi-byte rune, so a project or target name with a non-ASCII
// character rendered as a replacement glyph.
//
// The ellipsis is ASCII. The rest of this file is deliberately not: the pool
// grid, the spinner and the tree rules are GLYPHS, chosen for shape, and there
// is no ASCII substitute that draws them. That is a different question from
// prose typography, which the repo does keep to ASCII.
func Truncate(s string, n int) string {
	return layout.ClipBytes(s, n)
}
