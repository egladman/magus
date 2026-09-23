package queue

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/internal/json"
)

// ManifestVersion is the manifest format [Landing] accepts.
const ManifestVersion = 1

// ManifestFile is the manifest's name inside the directory validation writes.
const ManifestFile = "manifest.json"

// Decision is what validation decided for one change.
type Decision string

const (
	// DecisionLand: validated green; land in queue order.
	DecisionLand Decision = "land"
	// DecisionKick: a real conflict or a red gate; kick back with the report.
	DecisionKick Decision = "kick"
	// DecisionWait: not approved, or held behind a conflicting change; retried next run.
	DecisionWait Decision = "wait"
)

// Planned is one change's validation outcome.
type Planned struct {
	Change   Change   `json:"change"`
	Order    int      `json:"order"` // queue position; landing follows it
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason,omitempty"`
	Report   string   `json:"report,omitempty"` // kick-back body
	Group    int      `json:"group"`            // partition; -1 when never partitioned
	// After is the change validated beneath this one in its group, empty at the bottom.
	// Landing holds a change whose After did not land.
	After string `json:"after,omitempty"`
	// Stage is the validated staging commit: base plus every change beneath this one
	// plus this one, derived files regenerated.
	Stage   string `json:"stage,omitempty"`
	Message string `json:"message,omitempty"` // squash body: the change's own commits
	// Depth is the stage's speculation depth when its gate started: 1 ran on validated
	// commits alone, 2 on top of one unvalidated stage, and so on.
	Depth    int           `json:"depth,omitempty"`
	Duration time.Duration `json:"duration,omitempty"` // gate wall time
}

// Manifest is validation's output and landing's input: the whole contract between the
// read-only job and the write job.
type Manifest struct {
	Version     int        `json:"version"`
	Base        string     `json:"base"`
	BaseSHA     string     `json:"base_sha"` // tip every stage was built on
	Changes     []Planned  `json:"changes"`
	Groups      [][]string `json:"groups"` // change ids per partition
	Validations int        `json:"validations"`
}

// sorted returns the changes in queue order.
func (m Manifest) sorted() []Planned {
	out := slices.Clone(m.Changes)
	slices.SortStableFunc(out, func(a, b Planned) int { return a.Order - b.Order })
	return out
}

// Write stores the manifest as dir/manifest.json.
func (m Manifest) Write(dir string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ManifestFile), append(data, '\n'), 0o644)
}

// ReadManifest loads dir/manifest.json, refusing another format version.
func ReadManifest(dir string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("queue: read %s: %w", ManifestFile, err)
	}
	if m.Version != ManifestVersion {
		return Manifest{}, fmt.Errorf("queue: %s is format %d, this magus lands format %d", ManifestFile, m.Version, ManifestVersion)
	}
	if m.BaseSHA == "" || m.Base == "" {
		return Manifest{}, fmt.Errorf("queue: %s names no base", ManifestFile)
	}
	return m, nil
}
