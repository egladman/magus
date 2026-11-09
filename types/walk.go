package types

// WalkStep is what an fs\walk callback returns about the entry it was given,
// the way a Go fs.WalkDirFunc returns nil, fs.SkipDir or fs.SkipAll.
type WalkStep string

const (
	// WalkNext continues the walk, descending into a directory.
	WalkNext WalkStep = "next"
	// WalkSkipDir skips a directory's contents. Returned for a file, it skips
	// the rest of that file's directory, as fs.SkipDir does.
	WalkSkipDir WalkStep = "skipDir"
	// WalkSkipAll ends the walk.
	WalkSkipAll WalkStep = "skipAll"
)
