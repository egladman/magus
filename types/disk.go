package types

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
	// timeout, or the one-day floor that keeps every file used since yesterday.
	Stopped string
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
