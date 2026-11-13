package job

import (
	"fmt"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/types"
)

// staleStoreFix is how a person gets past a job store an older magus
// wrote. A newer magus's lines are kept whole.
const staleStoreFix = "move jobs.jsonl aside, or delete it, and fork the jobs again"

// refuseRecord refuses the record at line of the store, got where this
// magus reads only want. Another version names staleStoreFix or a newer
// magus. Another kind is a line no magus writes.
func (s *Store) refuseRecord(want, got apiversion.TypeMeta, line int) error {
	what := fmt.Sprintf("job: %s:%d", s.path, line)
	if got.APIVersion != want.APIVersion {
		return types.RefuseVersion(types.APIVersion(want.APIVersion), types.APIVersion(got.APIVersion), what, staleStoreFix)
	}
	return types.DiagnosticErrorf(types.StoreLineUndecodable, "%s: %v", what, &apiversion.RefusedError{Want: want, Got: got})
}
