package job

import (
	"path/filepath"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/vcs"
)

// placedMainFile holds each row head a placement read found on a base, beside
// jobs.jsonl. A retention pass reads it: the pass runs under the store's lock
// and reads no version control, yet must not retire work no base holds.
const placedMainFile = "placed-main.jsonl"

// placedMain is one line of placedMainFile. A base never loses content, so an
// answer never goes stale.
type placedMain struct {
	ID   string `json:"id"`
	Head string `json:"head"`
}

// placedMainPath is "" when no state dir answers, which only keeps rows longer.
func placedMainPath(root string) string {
	base, err := xdg.StateDir()
	if err != nil {
		return ""
	}
	dir, err := vcs.StateDir(base, "jobs", root)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, placedMainFile)
}

// readPlacedMain is the row heads a base holds, as read so far. A line that
// does not parse is skipped, which only keeps a row longer.
func readPlacedMain(root string) map[placedMain]bool {
	out := map[placedMain]bool{}
	path := placedMainPath(root)
	if path == "" {
		return out
	}
	read, err := codec.ReadLines(path)
	if err != nil {
		return out
	}
	for _, line := range read.Lines {
		var m placedMain
		if codec.Unmarshal(line.Text, &m) == nil && m.ID != "" && m.Head != "" {
			out[m] = true
		}
	}
	return out
}

// recordPlacedMain appends the heads in placed a base holds that the file
// lacks, and returns the write's error.
func recordPlacedMain(root string, placed []Placed) error {
	path := placedMainPath(root)
	if path == "" {
		return nil
	}
	known := readPlacedMain(root)
	var fresh [][]byte
	for _, p := range placed {
		m := placedMain{ID: p.Job, Head: p.Head}
		if p.Place != PlaceMain || known[m] {
			continue
		}
		known[m] = true
		if line, err := codec.Marshal(m); err == nil {
			fresh = append(fresh, line)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	return codec.AppendLines(path, fresh, 0o644)
}
