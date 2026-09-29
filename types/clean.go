package types

// CleanReport is what magus\clean returns, and what `magus clean -o json` prints.
// Removed and Tracked are absolute paths. DryRun means nothing was deleted.
type CleanReport struct {
	Removed []string `json:"removed" yaml:"removed" jsonl:"primary"`
	Tracked []string `json:"tracked" yaml:"tracked"`
	DryRun  bool     `json:"dry_run" yaml:"dry_run"`
}
