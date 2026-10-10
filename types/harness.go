package types

// HarnessPlan is what a host needs merged for one harness descriptor, computed against
// the files on disk. Magus prints it and never writes it: the person runs Merge.
// `magus describe harness` prints it and magus\describe.harness returns it.
type HarnessPlan struct {
	ID string `json:"id"`
	// Files is keyed by workspace-relative path and holds only the files missing something.
	Files map[string]HarnessFile `json:"files,omitempty"`
	// Merge is one POSIX shell command that brings every file in Files current, empty when
	// none is missing anything. It reads this plan back through `magus describe harness
	// <id> -o json` and pipes it into `magus buzz`, which merges each file with merge\json.
	Merge   string `json:"merge,omitempty"`
	MCPHint string `json:"mcp_hint,omitempty"`
	// AgentHints name the person-owned setting that picks a shipped agent's model, one per
	// distinct setting, when the harness spell left the model to the host's default.
	AgentHints []string `json:"agent_hints,omitempty"`
	// Wired is every entry group the descriptor manages, as the host file reads once it is
	// current, whether or not it already does. A current harness carries no Files, and this is
	// where a reader finds what the host runs (each hook's command) without parsing the host file.
	Wired []HarnessWired `json:"wired"`
}

// Current reports whether every host file already carries what the descriptor declares.
func (p HarnessPlan) Current() bool { return len(p.Files) == 0 }

// HarnessWired is one managed array in one host file: the file, the dotted key inside it,
// and the entries the descriptor declares there, verbatim host JSON.
type HarnessWired struct {
	File    string           `json:"file"`
	Key     string           `json:"key"`
	Entries []map[string]any `json:"entries"`
}

// HarnessFile is what one host file needs: Fragment merged into the JSON document the way
// merge\deep does (objects merge key by key, any other value replaces the one on disk), or
// Content as the whole file. A managed array appears in Fragment whole, with the entries a
// person added kept in place, so replacing the array on disk is exactly the merge.
type HarnessFile struct {
	Exists   bool            `json:"exists"`
	Fragment map[string]any  `json:"fragment,omitempty"`
	Content  string          `json:"content,omitempty"`
	Changes  []HarnessChange `json:"changes"`
}

// HarnessOwnedMarker ends every host hook command a harness spell renders that runs no
// shipped template, after a space: a shell comment, so the shell running the command
// never reads it. Host schemas refuse an unknown key, so the command string is the only
// place host JSON can carry it. A merge retires an entry carrying it once no declared
// entry takes its place, and a descriptor declaring an entry that neither runs a shipped
// template nor carries it is refused.
const HarnessOwnedMarker = "# magus:" + string(MarkerHarness)

// HarnessChange is one thing merging a HarnessFile changes.
type HarnessChange struct {
	Op    HarnessChangeOp `json:"op"`
	Key   string          `json:"key,omitempty"`
	Value any             `json:"value,omitempty"`
}

// HarnessChangeOp names what a HarnessChange does to its key.
type HarnessChangeOp string

const (
	// HarnessAdd appends an entry the managed array lacks.
	HarnessAdd HarnessChangeOp = "add"
	// HarnessReplace swaps an entry with the same matcher and commands for the declared one,
	// so an edited timeout does not leave a second copy of the hook.
	HarnessReplace HarnessChangeOp = "replace"
	// HarnessRetire drops an entry a descriptor wrote that no declared entry takes the place
	// of, so an upgraded hook is not judged twice.
	HarnessRetire HarnessChangeOp = "retire"
	// HarnessSet sets a key the file does not hold yet.
	HarnessSet HarnessChangeOp = "set"
	// HarnessWrite writes the whole file.
	HarnessWrite HarnessChangeOp = "write"
)
