package serviceident

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dockerRun builds a service whose process command is "docker run <args...>".
func dockerRun(args ...string) types.Service {
	return types.Service{Command: types.Command{Bin: "docker", Args: append([]string{"run"}, args...)}}
}

func TestParseExtractsIdentity(t *testing.T) {
	id := Parse(dockerRun(
		"--name", "randombox", "--rm", "-d",
		"-e", "POSTGRES_DB=api", "-e", "POSTGRES_PASSWORD=x",
		"-p", "5432:5432",
		"-v", "pgdata:/var/lib/postgresql/data:ro",
		"postgres:15",
		"postgres", "-c", "max_connections=200",
	).Command)

	assert.Equal(t, "postgres", id.Image)
	assert.Equal(t, "15", id.Tag)
	assert.Equal(t, []string{"5432"}, id.Ports)
	// Env is sorted; ephemeral --name/--rm/-d dropped; in-container command ignored.
	assert.Equal(t, []string{"POSTGRES_DB=api", "POSTGRES_PASSWORD=x"}, id.Env)
	assert.Equal(t, []string{"/var/lib/postgresql/data"}, id.Volumes)
	assert.True(t, id.IsContainer())
}

func TestParseInlineFlagValues(t *testing.T) {
	id := Parse(dockerRun("-e=POSTGRES_DB=api", "--publish=8080:5432", "postgres:16").Command)
	assert.Equal(t, "postgres", id.Image)
	assert.Equal(t, "16", id.Tag)
	assert.Equal(t, []string{"POSTGRES_DB=api"}, id.Env)
	// Container port is the last segment of the publish spec (host binding ignored).
	assert.Equal(t, []string{"5432"}, id.Ports)
}

func TestParseUnknownValueFlagDoesNotEatImage(t *testing.T) {
	// Regression: an unknown value-taking flag (--memory) must consume its value, not
	// let "512m" be mistaken for the image and drop the real one.
	id := Parse(dockerRun("--memory", "512m", "--cpus", "2", "-e", "POSTGRES_DB=api", "postgres:15").Command)
	assert.Equal(t, "postgres", id.Image)
	assert.Equal(t, "15", id.Tag)
	assert.Equal(t, []string{"POSTGRES_DB=api"}, id.Env)
}

func TestParseNonContainer(t *testing.T) {
	id := Parse(types.Command{Bin: "go", Args: []string{"run", "./server"}})
	assert.False(t, id.IsContainer())
	assert.Empty(t, id.Image)
}

func TestSplitImageTag(t *testing.T) {
	cases := []struct{ ref, repo, tag string }{
		{"postgres:15", "postgres", "15"},
		{"postgres", "postgres", ""},
		{"registry.io:5000/team/pg:15", "registry.io:5000/team/pg", "15"},
		{"registry.io:5000/team/pg", "registry.io:5000/team/pg", ""},
		{"pg@sha256:abc123", "pg", "sha256:abc123"},
	}
	for _, c := range cases {
		repo, tag := splitImageTag(c.ref)
		assert.Equal(t, c.repo, repo, c.ref)
		assert.Equal(t, c.tag, tag, c.ref)
	}
}

func TestContainerPort(t *testing.T) {
	assert.Equal(t, "5432", containerPort("5432"))
	assert.Equal(t, "5432", containerPort("5433:5432"))
	assert.Equal(t, "5432", containerPort("127.0.0.1:5433:5432"))
	assert.Equal(t, "5432", containerPort("5432/tcp"))
	assert.Equal(t, "5432", containerPort("127.0.0.1:5433:5432/tcp"))
}

func TestVolumeTarget(t *testing.T) {
	assert.Equal(t, "/data", volumeTarget("/host:/data"))
	assert.Equal(t, "/data", volumeTarget("name:/data:ro"))
	assert.Equal(t, "/data", volumeTarget("/data")) // anonymous
	assert.Equal(t, "/data", volumeTarget("/host:/data:rw"))
}

func TestFingerprintIdenticalConfigsMatch(t *testing.T) {
	a := dockerRun("-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15")
	// Same config, different flag order and a different ephemeral container name.
	b := dockerRun("--name", "other", "-p", "5432:5432", "-e", "POSTGRES_DB=api", "postgres:15")
	assert.Equal(t, Fingerprint(a), Fingerprint(b))
}

func TestFingerprintDistinguishesLoadBearingEnv(t *testing.T) {
	api := dockerRun("-e", "POSTGRES_DB=api", "-p", "5432:5432", "postgres:15")
	billing := dockerRun("-e", "POSTGRES_DB=billing", "-p", "5432:5432", "postgres:15")
	// This is the safety property: differing POSTGRES_DB must NOT auto-share.
	assert.NotEqual(t, Fingerprint(api), Fingerprint(billing))
}

func TestFingerprintIgnoresHostPortBinding(t *testing.T) {
	a := dockerRun("-p", "5432:5432", "postgres:15")
	b := dockerRun("-p", "5433:5432", "postgres:15")
	// Host binding is not identity; the shared instance is the same service.
	assert.Equal(t, Fingerprint(a), Fingerprint(b))
}

func TestFingerprintTagMatters(t *testing.T) {
	assert.NotEqual(t, Fingerprint(dockerRun("postgres:15")), Fingerprint(dockerRun("postgres:16")))
}

func TestFingerprintNonContainerFallsBackToArgv(t *testing.T) {
	a := types.Service{Command: types.Command{Bin: "go", Args: []string{"run", "./server"}}}
	b := types.Service{Command: types.Command{Bin: "go", Args: []string{"run", "./server"}}}
	c := types.Service{Command: types.Command{Bin: "go", Args: []string{"run", "./worker"}}}
	assert.Equal(t, Fingerprint(a), Fingerprint(b))
	assert.NotEqual(t, Fingerprint(a), Fingerprint(c))
}

func TestClusterKey(t *testing.T) {
	// Tag-insensitive and host-port-insensitive: same repo + container port cluster.
	k15, ok1 := ClusterKey(dockerRun("-p", "5432:5432", "postgres:15"))
	k16, ok2 := ClusterKey(dockerRun("-p", "5433:5432", "postgres:16"))
	require.True(t, ok1)
	require.True(t, ok2)
	assert.Equal(t, k15, k16)

	// Different container port is a different cluster (implies intent to separate).
	kOther, _ := ClusterKey(dockerRun("-p", "5432:5433", "postgres:15"))
	assert.NotEqual(t, k15, kOther)

	// Non-container service is not clusterable.
	_, ok := ClusterKey(types.Service{Command: types.Command{Bin: "go", Args: []string{"run", "."}}})
	assert.False(t, ok)
}
