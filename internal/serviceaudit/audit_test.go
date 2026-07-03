package serviceaudit

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dbProject builds a project at path whose one spell exposes a "db" service target
// rendering "docker run <args>".
func dbProject(path string, args ...string) *types.Project {
	spell := types.NewSpell("docker",
		types.WithTargets("db"),
		types.WithServiceTargets("db"),
		types.WithCommandRenderer(func(target string, _ []string) (string, []string, bool) {
			if target != "db" {
				return "", nil, false
			}
			return "docker", append([]string{"run"}, args...), true
		}),
	)
	return &types.Project{Path: path, ResolvedSpells: []*types.Spell{spell}}
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
	spell := types.NewSpell("go",
		types.WithTargets("build"),
		types.WithCommandRenderer(func(string, []string) (string, []string, bool) {
			return "go", []string{"build"}, true
		}),
	)
	p := &types.Project{Path: "svc", ResolvedSpells: []*types.Spell{spell}}
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
