package types

// MagusWorkspace is magus\workspace(): the workspace a script reads.
// Dir is the checkout whose branch its job facts are about, Root unless
// the call named another checkout of the same repository.
type MagusWorkspace struct {
	Root string `json:"root"`
	Dir  string `json:"dir"`
}

// Change is magus\change(): a change against Base and the tree it was
// built on. At and Files pin that tree, so a method refuses once the
// tree moves instead of answering about a different change.
type Change struct {
	// Base is the revision measured from; empty is the configured base.
	Base string `json:"base"`
	// Rev is a committed range, base...head, read instead of the tree.
	Rev string `json:"rev"`
	// Patch is a unified diff read instead of the tree.
	Patch string        `json:"patch"`
	At    VCSCheckpoint `json:"at"`
	// Files digests each path dirty at construction, "" when deleted.
	// A refusal names the paths whose digest moved.
	Files map[string]string `json:"files"`
}

// ChangeOptions are magus\change's options: a committed range or a
// patch to review instead of the working tree.
type ChangeOptions struct {
	Rev   string `json:"rev"`
	Patch string `json:"patch"`
}

// History is magus\history(): the window of history its metrics read.
type History struct {
	Dir     string `json:"dir"`
	Commits int    `json:"commits"`
	Since   string `json:"since"`
	Files   bool   `json:"files"`
}

// Options is h as the history scan reads it.
func (h History) Options() HistoryMetricsOptions {
	return HistoryMetricsOptions(h)
}

// Session is magus\session(): which session's guard record a read
// covers, and inside which window, each as trail.ReadOptions reads it.
type Session struct {
	Session string `json:"session"`
	Since   string `json:"since"`
	Until   string `json:"until"`
}

// JobRegistration is Job.register()'s answer: the stored row and the
// sentence derived from it, apart so a caller reads facts from the row.
type JobRegistration struct {
	Job    Job    `json:"job"`
	Advice string `json:"advice"`
}
