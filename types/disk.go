package types

// ReclaimDiskCommand is the shipped command a person or an agent runs to trim
// declared caches and remove finished worktrees, printing what really happened.
const ReclaimDiskCommand = `magus buzz -e 'import "magus/reclaimdisk" as r; r\reclaimNow();'`

// DiskUsage mirrors fs\diskUsage's record: the filesystem holding Path. FreeBytes is
// what an unprivileged process may still write, which on most filesystems is less than
// the blocks the kernel reports free, because some are reserved for root.
type DiskUsage struct {
	Path       string
	TotalBytes int64
	FreeBytes  int64
}

// TrimReport mirrors fs\trimOldest's record. Sizes are in bytes and times in Unix
// milliseconds.
type TrimReport struct {
	Dir          string
	BeforeBytes  int64
	AfterBytes   int64
	RemovedFiles int
	RemovedBytes int64
	// CutoffMs is the modification time older than which files were removed, 0 when the
	// directory was already within budget and nothing was.
	CutoffMs float64
	// Stopped is why the trim ended before reaching the budget: "" when it did, else the
	// timeout, or the fence that keeps every file modified within keepWithin.
	Stopped string
}

// SpellCache is one cache a spell the workspace resolves declares a trim budget for, where
// this process's environment locates it.
type SpellCache struct {
	Spell string `json:"spell"`
	Env   string `json:"env"`
	Dir   string `json:"dir"`
	// TrimMB is the size in MiB a reclaim trims it back to, never below 1: a cache declared
	// with no budget is left out.
	TrimMB int `json:"trimMb" buzz:"trimMb"`
}

// DiskReclaim is what a reclaim reads off the workspace: the caches it may trim, and the
// command a person runs to see what it left for them.
type DiskReclaim struct {
	Caches []SpellCache `json:"caches"`
	// ListCommand is the script serving event:disk-low here, "" when none does.
	ListCommand string `json:"listCommand" buzz:"listCommand"`
}

// DiskSample is one line of the disk store the reclaim hook appends to: the free space it
// saw, and what it started or finished. At is Unix milliseconds. The diskwatch module
// writes the line from Buzz with these JSON keys, and doctor reads it here.
type DiskSample struct {
	At         float64 `json:"at"`
	Path       string  `json:"path"`
	TotalBytes int64   `json:"totalBytes"`
	FreeBytes  int64   `json:"freeBytes"`
	// Action is "sample" for a reading alone, "started" when the hook began a reclaim
	// chunk, and "reclaimed" when a chunk finished; Detail says what that chunk did.
	Action string `json:"action"`
	Detail string `json:"detail"`
}
