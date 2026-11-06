package job

import (
	"fmt"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// staleStoreFix is how a person gets past a job store an older magus
// wrote. A newer magus's lines are kept whole.
const staleStoreFix = "move jobs.jsonl aside, or delete it, and fork the jobs again"

// decodeLine decodes one store line into into, whose envelope is schema,
// and refuses a line older than JobAPIVersion, naming staleStoreFix.
func (s *Store) decodeLine(text []byte, line int, into any, schema *types.Schema) error {
	if err := json.Unmarshal(text, into); err != nil {
		return types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", s.path, line, err)
	}
	adoptIntegerStamp(schema)
	if _, old := schema.Unknown["schema_version"]; old || (schema.Version != "" && schema.Version.Compare(types.JobAPIVersion) < 0) {
		return types.JobAPIVersion.Refuse(fmt.Sprintf("job: %s:%d", s.path, line), schema.Version, staleStoreFix)
	}
	return nil
}

// adoptIntegerStamp reads a line stamped with an integer schema_version
// as JobAPIVersion; its next rewrite writes the new stamp.
// compat(until: the next release): every worktree shares one store, and
// older binaries keep writing integers during development.
func adoptIntegerStamp(schema *types.Schema) {
	raw, ok := schema.Unknown["schema_version"]
	var n int
	if !ok || schema.Version != "" || json.Unmarshal(raw, &n) != nil {
		return
	}
	delete(schema.Unknown, "schema_version")
	if len(schema.Unknown) == 0 {
		schema.Unknown = nil
	}
	schema.Version = types.JobAPIVersion
}

// newestVersion is the stamp a rewrite carries: this build's, or the
// stored one when a newer magus wrote it, so no rewrite lowers a stamp.
func newestVersion(stored types.APIVersion) types.APIVersion {
	if stored.Compare(types.JobAPIVersion) > 0 {
		return stored
	}
	return types.JobAPIVersion
}
