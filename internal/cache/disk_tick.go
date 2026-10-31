package cache

import (
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
)

// diskTickSnippet is the tick every Go trigger starts. The rate limit is the
// store's, so a kick that loses a race does nothing.
const diskTickSnippet = `import "os"; import "magus/reclaimdisk" as r; _ = r\tick(".", deadline: os\time() + r\CHUNK_MS);`

// ReclaimNowSnippet runs a tick in the foreground and prints its result.
const ReclaimNowSnippet = `import "magus/reclaimdisk" as r; r\reclaimNow();`

// DiskFullFloorBytes is disk.critical_free_mb's default. config imports this
// package, so the print seat cannot read it; doctor's test holds them equal.
const DiskFullFloorBytes int64 = 10240 << 20

// diskFullFloor is a var so a test can stand in for a really low disk.
var diskFullFloor = DiskFullFloorBytes

// diskTickFloor is diskwatch's FLOOR_MS.
const diskTickFloor = time.Minute

// failureLogTail is how much of a failed run's log the ENOSPC check reads.
const failureLogTail = 64 << 10

const noSpaceText = "no space left on device"

// KickDiskTick starts the disk tick detached when the store is stale, at the
// cost of one stat. A binary not named magus is a test's, and starts nothing.
func KickDiskTick(exe, dir string, every time.Duration) {
	if every <= 0 || filepath.Base(exe) != "magus" {
		return
	}
	base, err := xdg.StateDir()
	if err != nil {
		return
	}
	info, err := os.Stat(filepath.Join(base, "magus", "disk", "samples.jsonl"))
	if err == nil && time.Since(info.ModTime()) < max(every, diskTickFloor) {
		return
	}
	cmd := osexec.Command(exe, "buzz", "-C", dir, "-e", diskTickSnippet)
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

// DiskBelow reports what the filesystem holding dir has free, and whether that
// is under floorBytes. A disk it cannot read is not below: the failure speaks.
func DiskBelow(dir string, floorBytes int64) (free int64, below bool) {
	_, free, err := StatDisk(dir)
	return free, err == nil && floorBytes > 0 && free < floorBytes
}

// DiskFullCause is the line naming the fix when output says the disk is full
// and the filesystem holding dir agrees, "" otherwise. Text alone cannot tell
// a fixture printing the string from a real failure.
func DiskFullCause(output, dir string, floorBytes int64) string {
	if !strings.Contains(strings.ToLower(output), noSpaceText) {
		return ""
	}
	free, below := DiskBelow(dir, floorBytes)
	if !below {
		return ""
	}
	return fmt.Sprintf("[%s] the disk is full: %d MiB free where this ran. Run `%s`, which trims caches and says what it did and what it leaves for you",
		types.DiskFull, free>>20, types.ReclaimDiskCommand)
}

// tailOf is the last n bytes of the file at path, "" when it cannot be read.
func tailOf(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > n {
		if _, err := f.Seek(-n, io.SeekEnd); err != nil {
			return ""
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return string(data)
}
