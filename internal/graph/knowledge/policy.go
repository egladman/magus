package knowledge

import (
	"context"
	"path/filepath"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/readlog"
	"github.com/egladman/magus/types"
)

// PolicyRecord is the target policy one open of the workspace read, beside
// the domain fast stamp that open's reads compute. A checkout no sync has
// reached has no manifest to answer from. The guard writes one whenever it
// opens the workspace to learn the policy.
type PolicyRecord struct {
	// FastDomain is the domain fast stamp over Reads, never "".
	FastDomain string                  `json:"fast_domain"`
	Reads      readlog.Reads           `json:"reads"`
	Policies   types.TargetPolicyIndex `json:"policies"`
}

// policyRecords is the store of PolicyRecord: one entry, the last open's.
func policyRecords(cacheDir string) file.JSONL[PolicyRecord] {
	return file.JSONL[PolicyRecord]{
		Path: filepath.Join(StoreDir(cacheDir), "target-policies.jsonl"),
		Key:  func(PolicyRecord) string { return "workspace" },
	}
}

// RecordTargetPolicies appends r as the record under cacheDir. ctx bounds
// only the wait for the store's lock.
func RecordTargetPolicies(ctx context.Context, cacheDir string, r PolicyRecord) error {
	return policyRecords(cacheDir).Update(ctx, func([]PolicyRecord) []PolicyRecord {
		return []PolicyRecord{r}
	})
}

// RecordedPolicies returns the last record under cacheDir, false when there
// is none. The caller checks its FastDomain against the tree.
func RecordedPolicies(cacheDir string) (PolicyRecord, bool, error) {
	entries, err := policyRecords(cacheDir).Load()
	if err != nil || len(entries) == 0 {
		return PolicyRecord{}, false, err
	}
	return entries[len(entries)-1], true, nil
}
