package types

// KnowledgeRenameDefinition is the human-readable description of the rename view.
const KnowledgeRenameDefinition = "refs --rename replaces a symbol's name at every site its verified occurrences " +
	"name, after grading every file under the acting lease: every file is written or none is. " +
	"Its inverse is the same verb with the two names swapped."

// KnowledgeRenameOutput is the result of `magus refs <symbol> --rename <new>`. A refused
// rename carries Refused and wrote nothing; a dry run carries the sites it would
// change and Applied false.
type KnowledgeRenameOutput struct {
	Definition    string `json:"definition"     yaml:"definition"`
	SchemaVersion int    `json:"schema_version" yaml:"schema_version"`
	Symbol        string `json:"symbol"         yaml:"symbol"`
	Label         string `json:"label"          yaml:"label"`
	// From is the spelling every site held; To replaces it.
	From      string        `json:"from"                yaml:"from"`
	To        string        `json:"to"                  yaml:"to"`
	Applied   bool          `json:"applied"             yaml:"applied"`
	FileCount int           `json:"file_count"          yaml:"file_count"`
	SiteCount int           `json:"site_count"          yaml:"site_count"`
	Files     []EditedFile  `json:"files,omitempty"     yaml:"files,omitempty"`
	Sites     []EditSpan    `json:"sites,omitempty"     yaml:"sites,omitempty"`
	Refused   []EditRefusal `json:"refused,omitempty"   yaml:"refused,omitempty"`
}

// EditedFile is one file an edit rewrites, with its digest before and after.
type EditedFile struct {
	Path         string `json:"path"          yaml:"path"`
	DigestBefore string `json:"digest_before" yaml:"digest_before"`
	DigestAfter  string `json:"digest_after"  yaml:"digest_after"`
}

// EditSpan is one replaced range in the file as it was read.
type EditSpan struct {
	Path  string       `json:"path"  yaml:"path"`
	Start EditPosition `json:"start" yaml:"start"`
	// End is exclusive.
	End EditPosition `json:"end" yaml:"end"`
}

// EditPosition is a 1-based line and a 1-based byte column.
type EditPosition struct {
	Line   int `json:"line"   yaml:"line"`
	Column int `json:"column" yaml:"column"`
}

// EditRefusal is one reason an edit was not applied. Line is 0 for a reason that belongs
// to the whole file; Rule names the guard rule behind a lease refusal.
type EditRefusal struct {
	Path   string `json:"path,omitempty"   yaml:"path,omitempty"`
	Line   int    `json:"line,omitempty"   yaml:"line,omitempty"`
	Column int    `json:"column,omitempty" yaml:"column,omitempty"`
	Rule   string `json:"rule,omitempty"   yaml:"rule,omitempty"`
	Reason string `json:"reason"           yaml:"reason"`
}
