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
	if _, old := schema.Unknown["schema_version"]; old || (schema.Version != "" && schema.Version.Compare(types.JobAPIVersion) < 0) {
		return types.RefuseVersion(types.JobAPIVersion, schema.Version, fmt.Sprintf("job: %s:%d", s.path, line), staleStoreFix)
	}
	return nil
}

// newestVersion is the stamp a rewrite carries: this build's, or the
// stored one when a newer magus wrote it, so no rewrite lowers a stamp.
func newestVersion(stored types.APIVersion) types.APIVersion {
	if stored.Compare(types.JobAPIVersion) > 0 {
		return stored
	}
	return types.JobAPIVersion
}
