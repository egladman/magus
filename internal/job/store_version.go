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
	var result *types.JobResult
	if row, ok := into.(*types.Job); ok {
		result = row.Result
	}
	adoptIntegerStamp(schema, result)
	if _, old := schema.Unknown["schema_version"]; old || (schema.Version != "" && schema.Version.Compare(types.JobAPIVersion) < 0) {
		return types.JobAPIVersion.Refuse(fmt.Sprintf("job: %s:%d", s.path, line), schema.Version, staleStoreFix)
	}
	return nil
}

// adoptIntegerStamp reads a line stamped with an integer schema_version
// as JobAPIVersion; its next rewrite writes the new stamp. An older
// binary rewriting the store keeps api_version and adds its integer, so
// a line carrying both reads as its api_version. The row's result reads
// its integer as JobResultAPIVersion the same way.
// compat(until: the next release): every worktree shares one store, and
// older binaries keep writing integers during development.
func adoptIntegerStamp(schema *types.Schema, result *types.JobResult) {
	adoptStamp(schema, types.JobAPIVersion)
	if result != nil {
		adoptStamp(&result.Schema, types.JobResultAPIVersion)
	}
}

// compat: see adoptIntegerStamp.
func adoptStamp(schema *types.Schema, v types.APIVersion) {
	raw, ok := schema.Unknown["schema_version"]
	var n int
	if !ok || json.Unmarshal(raw, &n) != nil {
		return
	}
	delete(schema.Unknown, "schema_version")
	if len(schema.Unknown) == 0 {
		schema.Unknown = nil
	}
	if schema.Version == "" {
		schema.Version = v
	}
}

// newestVersion is the stamp a rewrite carries: this build's, or the
// stored one when a newer magus wrote it, so no rewrite lowers a stamp.
func newestVersion(stored types.APIVersion) types.APIVersion {
	if stored.Compare(types.JobAPIVersion) > 0 {
		return stored
	}
	return types.JobAPIVersion
}
