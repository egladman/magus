package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

// writeServedIndex plants an index with the given expires_at under a fake root.
func writeServedIndex(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.Dir(servedIndexPath))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, servedIndexPath), []byte(body), 0o644))
	return root
}

func TestCheckReleaseIndexExpiry(t *testing.T) {
	inDays := func(d int) string {
		return time.Now().Add(time.Duration(d) * 24 * time.Hour).Format(time.RFC3339)
	}
	cases := []struct {
		name       string
		body       string
		wantStatus types.DoctorCheckStatus
		wantMsg    string
	}{
		{"good for months", fmt.Sprintf(`{"expires_at":%q}`, inDays(170)), types.DoctorOK, "good for another"},
		{"inside the warning window", fmt.Sprintf(`{"expires_at":%q}`, inDays(10)), types.DoctorAdvice, "expires in"},
		{"already expired", fmt.Sprintf(`{"expires_at":%q}`, inDays(-3)), types.DoctorFail, "is refusing it"},
		{"no bound at all", `{"schema_version":1}`, types.DoctorFail, "declares no expires_at"},
		{"unreadable bound", `{"expires_at":"soon"}`, types.DoctorFail, "not RFC3339"},
		{"not json", `{`, types.DoctorFail, "does not parse"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &runner{root: writeServedIndex(t, c.body)}
			got := r.checkReleaseIndexExpiry()
			require.Equal(t, c.wantStatus, got.Status)
			require.Contains(t, got.Message, c.wantMsg)
			// No Fix, on the consent grounds checkRegistryFreshness states: re-signing is
			// a human running a workflow. The arms this used to carry dispatched `run
			// release-index`, a target no workspace declares, so the first finding would
			// have taken `doctor --fix` down with it.
			require.Empty(t, got.Fix)
		})
	}
}

// TestCheckReleaseIndexExpirySilentElsewhere: doctor runs in every magus workspace,
// and only this repository serves the index. A check that failed on its absence would
// fail for every user of magus.
func TestCheckReleaseIndexExpirySilentElsewhere(t *testing.T) {
	got := (&runner{root: t.TempDir()}).checkReleaseIndexExpiry()
	require.Equal(t, types.DoctorOK, got.Status)
	require.Contains(t, got.Message, "not served from this workspace")
}

// TestServedIndexPassesItsOwnCheck runs the check against the real repository, so a
// freshly signed index that is somehow already inside the warning window is caught
// here rather than by a user.
func TestServedIndexPassesItsOwnCheck(t *testing.T) {
	got := (&runner{root: filepath.Join("..", "..")}).checkReleaseIndexExpiry()
	require.Equal(t, types.DoctorOK, got.Status, got.Message)
}
