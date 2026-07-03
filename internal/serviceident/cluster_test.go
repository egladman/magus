package serviceident

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNearDuplicatesCanonicalPostgresSprawl(t *testing.T) {
	// The canonical foot-gun: three projects each spin up "their own" Postgres.
	members := []Member{
		{Name: "web/api-db", Service: dockerRun("-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15")},
		{Name: "billing/db", Service: dockerRun("-e", "POSTGRES_DB=billing", "-p", "5432:5432", "postgres:15")},
		{Name: "search/pg", Service: dockerRun("-e", "POSTGRES_DB=search", "-p", "5432:5432", "postgres:16")},
	}
	clusters := NearDuplicates(members)
	require.Len(t, clusters, 1)
	c := clusters[0]
	assert.Equal(t, "postgres", c.Image)
	assert.Equal(t, "5432", c.Port)
	require.Len(t, c.Members, 3)

	// Members are name-sorted; each delta reports only the attributes that vary
	// across the cluster. Tag varies (one is postgres:16), so every member reports
	// its tag for comparison alongside its differing DB.
	assert.Equal(t, "billing/db", c.Members[0].Name)
	assert.Equal(t, []string{"tag=15", "POSTGRES_DB=billing"}, c.Members[0].Delta)

	assert.Equal(t, "search/pg", c.Members[1].Name)
	assert.Equal(t, []string{"tag=16", "POSTGRES_DB=search"}, c.Members[1].Delta)

	assert.Equal(t, "web/api-db", c.Members[2].Name)
	assert.Equal(t, []string{"tag=15", "POSTGRES_DB=api"}, c.Members[2].Delta)
}

func TestNearDuplicatesIdenticalCopiesNotWarned(t *testing.T) {
	// Byte-identical services would auto-share silently: not a foot-gun, no warning.
	members := []Member{
		{Name: "a/db", Service: dockerRun("-e", "POSTGRES_DB=shared", "-p", "5432:5432", "postgres:15")},
		{Name: "b/db", Service: dockerRun("-e", "POSTGRES_DB=shared", "-p", "5432:5432", "postgres:15")},
	}
	assert.Empty(t, NearDuplicates(members))
}

func TestNearDuplicatesDifferentPortNotClustered(t *testing.T) {
	// Different container ports imply intent to run separately: no runtime warning.
	members := []Member{
		{Name: "a/pg", Service: dockerRun("-p", "5432:5432", "postgres:15")},
		{Name: "b/pg", Service: dockerRun("-p", "5432:5433", "postgres:15")},
	}
	assert.Empty(t, NearDuplicates(members))
}

func TestNearDuplicatesSingletonNotClustered(t *testing.T) {
	members := []Member{
		{Name: "only/db", Service: dockerRun("-p", "5432:5432", "postgres:15")},
	}
	assert.Empty(t, NearDuplicates(members))
}

func TestNearDuplicatesIgnoresNonContainer(t *testing.T) {
	members := []Member{
		{Name: "a/db", Service: dockerRun("-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15")},
		{Name: "b/db", Service: dockerRun("-e", "POSTGRES_DB=billing", "-p", "5432:5432", "postgres:15")},
		{Name: "svc/app", Service: types.Service{Command: types.Command{Bin: "go", Args: []string{"run", "./server"}}}},
	}
	clusters := NearDuplicates(members)
	require.Len(t, clusters, 1)
	assert.Len(t, clusters[0].Members, 2)
}

func TestFormatWarning(t *testing.T) {
	assert.Empty(t, FormatWarning(nil))

	members := []Member{
		{Name: "web/api-db", Service: dockerRun("-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15")},
		{Name: "billing/db", Service: dockerRun("-e", "POSTGRES_DB=billing", "-p", "5432:5432", "postgres:15")},
	}
	out := FormatWarning(NearDuplicates(members))
	assert.Contains(t, out, `2 services share image "postgres" on container port 5432`)
	assert.Contains(t, out, "billing/db  (POSTGRES_DB=billing)")
	assert.Contains(t, out, "web/api-db  (POSTGRES_DB=api)")
	assert.Contains(t, out, "extract a shared target both need")
	// Plain ASCII only (no em-dashes or fancy punctuation in user-facing strings).
	for _, r := range out {
		assert.Less(t, r, rune(128), "non-ASCII rune in warning: %q", string(r))
	}
}

// distinct marks a service intentionally-separate with a reason.
func distinct(s types.Service, reason string) types.Service {
	s.Distinct = reason
	return s
}

func TestNearDuplicatesDistinctOptsOut(t *testing.T) {
	// Three near-duplicates, one marked distinct: it drops out of the warning, the
	// other two still cluster.
	members := []Member{
		{Name: "web/db", Service: dockerRun("-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15")},
		{Name: "billing/db", Service: dockerRun("-e", "POSTGRES_DB=billing", "-p", "5432:5432", "postgres:15")},
		{Name: "legacy/db", Service: distinct(dockerRun("-e", "POSTGRES_DB=legacy", "-p", "5432:5432", "postgres:15"), "legacy schema, must stay separate")},
	}
	clusters := NearDuplicates(members)
	require.Len(t, clusters, 1)
	require.Len(t, clusters[0].Members, 2)
	assert.Equal(t, "billing/db", clusters[0].Members[0].Name)
	assert.Equal(t, "web/db", clusters[0].Members[1].Name)
}

func TestNearDuplicatesDistinctBreaksPair(t *testing.T) {
	// Only two near-duplicates and one is distinct: nothing left to warn about.
	members := []Member{
		{Name: "web/db", Service: dockerRun("-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15")},
		{Name: "legacy/db", Service: distinct(dockerRun("-e", "POSTGRES_DB=legacy", "-p", "5432:5432", "postgres:15"), "legacy")},
	}
	assert.Empty(t, NearDuplicates(members))
}

func TestUnusedDistinct(t *testing.T) {
	// A distinct service with a real differing peer: the suppression is used.
	used := []Member{
		{Name: "web/db", Service: dockerRun("-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15")},
		{Name: "legacy/db", Service: distinct(dockerRun("-e", "POSTGRES_DB=legacy", "-p", "5432:5432", "postgres:15"), "legacy")},
	}
	assert.Empty(t, UnusedDistinct(used))

	// A distinct service that is the only one on its cluster key: stale suppression.
	stale := []Member{
		{Name: "solo/db", Service: distinct(dockerRun("-p", "5432:5432", "postgres:15"), "no longer shared")},
		{Name: "other/cache", Service: dockerRun("-p", "6379:6379", "redis:7")},
	}
	assert.Equal(t, []string{"solo/db"}, UnusedDistinct(stale))
}

func TestNearDuplicatesVolumeDelta(t *testing.T) {
	members := []Member{
		{Name: "a/db", Service: dockerRun("-v", "adata:/var/lib/postgresql/data", "-p", "5432:5432", "postgres:15")},
		{Name: "b/db", Service: dockerRun("-v", "bdata:/data", "-p", "5432:5432", "postgres:15")},
	}
	clusters := NearDuplicates(members)
	require.Len(t, clusters, 1)
	assert.Equal(t, []string{"volumes=[/var/lib/postgresql/data]"}, clusters[0].Members[0].Delta)
	assert.Equal(t, []string{"volumes=[/data]"}, clusters[0].Members[1].Delta)
}
