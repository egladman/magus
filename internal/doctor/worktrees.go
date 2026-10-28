package doctor

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
)

// worktreesDirRel is where the agent tooling parks per-branch checkouts. It is
// inside the workspace, so anything left there is walked as if it were source.
const worktreesDirRel = ".claude/worktrees"

// checkStaleWorktrees reports checkout directories under .claude/worktrees that git
// no longer tracks.
//
// A leftover directory is a second copy of the workspace's spell sources, so magus
// discovers them twice at the repo root: it trips MGS1002 (spell shadowed) and fails
// tests that walk the tree, with a failure that points at the duplicate rather than
// at the cause.
//
// Workspaces that hit this have tended to write "remove dead worktrees first" into
// their contributor notes, which makes it a rule a human has to remember. A check
// costs nothing to run and reports the cause directly, which is the whole argument
// for it being here rather than in prose.
//
// Detection is by absence of the .git link file that git writes into a live
// worktree and removes on `git worktree remove`/`prune`. A pruned-but-not-deleted
// directory therefore reads as stale without shelling out to git, which keeps the
// check cheap enough to run every time and correct when git is unavailable.
func checkStaleWorktrees(root string) types.Check {
	const name = "stale-worktrees"
	dir := filepath.Join(root, filepath.FromSlash(worktreesDirRel))

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return types.Check{Name: name, Status: types.CheckOK, Message: "no " + worktreesDirRel + " directory"}
		}
		return types.Check{Name: name, Status: types.CheckFail, Message: fmt.Sprintf("scan %s: %v", worktreesDirRel, err)}
	}

	var stale []string
	live := 0
	for _, e := range entries {
		// A symlinked worktree reports as a link, not a dir, so resolve before judging:
		// skipping it would both under-count and never report it stale.
		if !e.IsDir() {
			if info, serr := os.Stat(filepath.Join(dir, e.Name())); serr != nil || !info.IsDir() {
				continue
			}
		}
		// A live worktree carries a .git FILE pointing at the real gitdir; a directory
		// git has forgotten has neither.
		if _, statErr := os.Stat(filepath.Join(dir, e.Name(), ".git")); statErr == nil {
			live++
			continue
		}
		stale = append(stale, e.Name())
	}
	if len(stale) == 0 {
		return types.Check{Name: name, Status: types.CheckOK, Message: fmt.Sprintf("%d live worktree(s); none orphaned", live)}
	}
	sort.Strings(stale)

	details := make([]string, 0, len(stale)+1)
	for _, s := range stale {
		details = append(details, filepath.Join(worktreesDirRel, s))
	}
	details = append(details, "remove the directory, or restore it with `git worktree add` if the work is still wanted")
	return types.Check{
		Name:    name,
		Status:  types.CheckFail,
		Message: fmt.Sprintf("%d directory(ies) under %s are not live worktrees; they duplicate spell sources and can trip MGS1002 and fail tests", len(stale), worktreesDirRel),
		Details: details,
	}
}

// DiskSamplesPath is the store the reclaim hook appends a types.DiskSample to, one JSON
// object a line: $XDG_STATE_HOME/magus/disk/samples.jsonl, the path the magus/reclaimdisk
// module builds with fs\stateDir("disk").
func DiskSamplesPath() (string, error) {
	base, err := xdg.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "magus", "disk", "samples.jsonl"), nil
}

// readDiskSamples reads every line of the store; a line that does not decode is an error
// naming its number rather than a gap in the trend.
func readDiskSamples(path string) ([]types.DiskSample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var samples []types.DiskSample
	scan := bufio.NewScanner(f)
	for n := 1; scan.Scan(); n++ {
		var s types.DiskSample
		if err := json.Unmarshal(scan.Bytes(), &s); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		samples = append(samples, s)
	}
	return samples, scan.Err()
}

// diskTrend grades the store's samples against the disk thresholds: the newest free space,
// how fast it fell over the last day, and the last reclaim chunk. Below disk.min_free_mb or
// falling faster than disk.growth_mb_per_hour is advice, never a failure.
func diskTrend(samples []types.DiskSample, cfg config.Disk) types.Check {
	const name = "disk-trend"
	thresholds := fmt.Sprintf("disk.min_free_mb %d%s, disk.growth_mb_per_hour %d%s", cfg.MinFreeMB, defaulted(cfg.MinFreeMB, config.Defaults().Disk.MinFreeMB),
		cfg.GrowthMBPerHour, defaulted(cfg.GrowthMBPerHour, config.Defaults().Disk.GrowthMBPerHour))
	var last, oldest *types.DiskSample
	var reclaimed []string
	for i := range samples {
		s := &samples[i]
		if s.Action == "reclaimed" {
			reclaimed = append(reclaimed, fmt.Sprintf("%s: %s", time.UnixMilli(int64(s.At)).Format(time.DateTime), s.Detail))
		}
		if s.TotalBytes > 0 {
			last = s
		}
	}
	if last == nil {
		return types.Check{Name: name, Status: types.CheckOK, Message: "no disk samples yet: the reclaim hook a harness spell wires records them; " + thresholds}
	}
	for i := range samples {
		if s := &samples[i]; s.TotalBytes > 0 && s.Path == last.Path && last.At-s.At <= float64(24*time.Hour/time.Millisecond) {
			oldest = s
			break
		}
	}
	freeMB := last.FreeBytes >> 20
	msg := fmt.Sprintf("%d MB free of %d MB on %s at %s", freeMB, last.TotalBytes>>20, last.Path, time.UnixMilli(int64(last.At)).Format(time.DateTime))
	hours := (last.At - oldest.At) / float64(time.Hour/time.Millisecond)
	var perHour int64
	if hours > 0 {
		perHour = int64(float64((oldest.FreeBytes-last.FreeBytes)>>20) / hours)
		msg += fmt.Sprintf("; free space changed %+d MB an hour over %.1fh", -perHour, hours)
	}
	status := types.CheckOK
	if (cfg.MinFreeMB > 0 && freeMB < int64(cfg.MinFreeMB)) || (cfg.GrowthMBPerHour > 0 && perHour > int64(cfg.GrowthMBPerHour)) {
		status = types.CheckAdvice
	}
	if len(reclaimed) > 3 {
		reclaimed = reclaimed[len(reclaimed)-3:]
	}
	return types.Check{Name: name, Status: status, Message: msg + "; " + thresholds, Details: reclaimed}
}

func defaulted(value, def int) string {
	if value == def {
		return " (the default)"
	}
	return ""
}

// checkDiskTrend reads the reclaim hook's samples and grades them; a store that is not
// there yet is a fact, and one that cannot be read is advice naming why.
func (r *runner) checkDiskTrend() types.Check {
	path, err := DiskSamplesPath()
	if err != nil {
		return types.Check{Name: "disk-trend", Status: types.CheckAdvice, Message: fmt.Sprintf("no state directory to read disk samples from: %v", err)}
	}
	samples, err := readDiskSamples(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return types.Check{Name: "disk-trend", Status: types.CheckAdvice, Message: fmt.Sprintf("cannot read the disk samples: %v", err)}
	}
	return diskTrend(samples, r.opts.cfg.Disk)
}
