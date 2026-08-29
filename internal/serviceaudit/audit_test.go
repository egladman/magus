package serviceaudit

import (
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dbProject builds a project at path whose one spell exposes a "db" service target
// rendering "docker run <args>".
func dbProject(path string, args ...string) *types.Project {
	spell := spells.NewSpell("docker",
		spells.WithTargets("db"),
		spells.WithServiceTargets("db"),
		spells.WithCommandRenderer(func(target string, _ []string) (string, []string, bool, error) {
			if target != "db" {
				return "", nil, false, nil
			}
			return "docker", append([]string{"run"}, args...), true, nil
		}),
	)
	return &types.Project{Path: path, ResolvedSpells: []*spells.Spell{spell}}
}

// distinctDBProject is dbProject plus the static service view that carries the MGS5001
// opt-out. Separate because the reason lives on the op's view and not in the rendered argv:
// a helper that only rendered a command could never have caught the reason being dropped.
func distinctDBProject(path, reason string, args ...string) *types.Project {
	p := dbProject(path, args...)
	spells.WithServiceView(func(target string) (*spells.ServiceView, bool) {
		if target != "db" {
			return nil, false
		}
		return &spells.ServiceView{Distinct: reason}, true
	})(p.ResolvedSpells[0])
	return p
}

func TestCollectMembersRendersServiceTargets(t *testing.T) {
	members := collectMembers([]*types.Project{
		dbProject("web", "-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15"),
	}, nil)
	require.Len(t, members, 1)
	assert.Equal(t, "web:db", members[0].Name)
	assert.Equal(t, "docker", members[0].Service.Command.Bin)
}

func TestCollectMembersSkipsCommandTargets(t *testing.T) {
	// A non-service target must not be collected even if it renders a command.
	spell := spells.NewSpell("go",
		spells.WithTargets("build"),
		spells.WithCommandRenderer(func(string, []string) (string, []string, bool, error) {
			return "go", []string{"build"}, true, nil
		}),
	)
	p := &types.Project{Path: "svc", ResolvedSpells: []*spells.Spell{spell}}
	assert.Empty(t, collectMembers([]*types.Project{p}, nil))
}

func TestDetectFindsNearDuplicatePostgres(t *testing.T) {
	projects := []*types.Project{
		dbProject("web", "-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15"),
		dbProject("billing", "-e", "POSTGRES_DB=billing", "-p", "5432:5432", "postgres:15"),
	}
	clusters := NearDuplicates(projects, nil)
	require.Len(t, clusters, 1)
	assert.Equal(t, "postgres", clusters[0].Image)
	require.Len(t, clusters[0].Members, 2)
	assert.Equal(t, "billing:db", clusters[0].Members[0].Name)
	assert.Equal(t, "web:db", clusters[0].Members[1].Name)
}

func TestDetectIdenticalServicesNotFlagged(t *testing.T) {
	projects := []*types.Project{
		dbProject("a", "-e", "POSTGRES_DB=shared", "-p", "5432:5432", "postgres:15"),
		dbProject("b", "-e", "POSTGRES_DB=shared", "-p", "5432:5432", "postgres:15"),
	}
	assert.Empty(t, NearDuplicates(projects, nil))
}

// TestCollectMembersCarriesTheDistinctReason is the bridge MGS5001's documented remedy runs
// over. The reason is declared on the op and never appears in the rendered argv, so a member
// assembled from the command alone loses it - and every consumer downstream then behaves as
// though no service in the workspace had ever opted out.
func TestCollectMembersCarriesTheDistinctReason(t *testing.T) {
	members := collectMembers([]*types.Project{
		distinctDBProject("web", "pinned to 14 until the extension lands", "-p", "5432:5432", "postgres:15"),
	}, nil)

	require.Len(t, members, 1)
	assert.Equal(t, "pinned to 14 until the extension lands", members[0].Service.Distinct)
}

// TestDistinctSilencesTheNearDuplicateWarning is the remedy end to end: `magus describe target`
// already showed the mark, while the warning it is supposed to silence kept firing.
func TestDistinctSilencesTheNearDuplicateWarning(t *testing.T) {
	projects := []*types.Project{
		distinctDBProject("web", "api needs its own volume", "-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15"),
		dbProject("billing", "-e", "POSTGRES_DB=billing", "-p", "5432:5432", "postgres:15"),
	}

	assert.Empty(t, NearDuplicates(projects, nil),
		"one member opted out leaves a cluster of one, which is not a near-duplicate")
}

// TestUnusedDistinctFindsAStaleSuppression is doctor's service-suppressions check. It reported
// OK on every workspace ever run, because a check over members that carry no reasons can only
// ever find nothing.
func TestUnusedDistinctFindsAStaleSuppression(t *testing.T) {
	projects := []*types.Project{
		distinctDBProject("solo", "kept separate while we migrate", "-p", "5432:5432", "postgres:15"),
	}

	assert.Equal(t, []string{"solo:db"}, UnusedDistinct(projects, nil),
		"a distinct service with no near-duplicate left is a reason to prune")
}
