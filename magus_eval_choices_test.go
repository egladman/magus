package magus

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/libs/testkit"
)

// EvalChoicesSuite opens workspaces whose magusfiles choose a secret provider and a
// remote cache backend, and checks that Open applies what the evaluation returned.
type EvalChoicesSuite struct {
	suite.Suite
}

func TestEvalChoicesSuite(t *testing.T) {
	suite.Run(t, new(EvalChoicesSuite))
}

const (
	chooseFakeCache   = "import \"magus\";\nmagus\\cache.remote({\"name\": \"fake-cache\"});\n"
	chooseFakeSecrets = "import \"magus\";\nmagus\\secret.provider({\"name\": \"fake-secrets\"});\n"
)

// open opens a workspace whose root magusfile is root and whose api project's is api.
// The trust set is waived so a wired backend reaches the cache.
func (s *EvalChoicesSuite) open(root, api string) (*Magus, error) {
	testkit.Isolate(s.T())
	w := testkit.NewWorkspace(s.T())
	w.Magusfile(root)
	w.Write(filepath.Join("api", "magusfile.buzz"), api)
	cfg := config.Defaults()
	cfg.Cache.Dir = filepath.Join(s.T().TempDir(), ".magus")
	cfg.Cache.Remote.Insecure = true
	cfg.Cache.Remote.InsecureReason = "the test backend is never trusted"
	m, err := Open(s.T().Context(), w.Root(), workspace.WithLoadedConfig(cfg), WithSpawnRule(tier.Ceiling))
	if m != nil {
		s.T().Cleanup(func() { _ = m.Close() })
	}
	return m, err
}

func (s *EvalChoicesSuite) TestOpenAppliesTheProviderToTheResolver() {
	m, err := s.open(chooseFakeSecrets, "")
	s.Require().NoError(err)
	s.Equal("fake-secrets", m.SecretProvider())
	r := secrets.ResolverFromContext(m.ContextWithSecrets(s.T().Context()))
	s.Require().NotNil(r)
	s.Equal("fake-secrets", r.ProviderName())
}

func (s *EvalChoicesSuite) TestOpenWiresTheBackendIntoTheCache() {
	m, err := s.open(chooseFakeCache, "")
	s.Require().NoError(err)
	cacheTier, _, _ := m.CacheDescription()
	s.Contains(cacheTier, "remote fake-cache unavailable", "the chosen backend was opened, and it is not registered")
	s.Empty(m.SecretProvider())
}

func (s *EvalChoicesSuite) TestIdenticalChoicesAcrossMagusfilesPass() {
	m, err := s.open(chooseFakeCache, chooseFakeCache)
	s.Require().NoError(err)
	cacheTier, _, _ := m.CacheDescription()
	s.Contains(cacheTier, "remote fake-cache unavailable")
}

func (s *EvalChoicesSuite) TestDifferentChoicesAcrossMagusfilesAreRefused() {
	_, err := s.open(chooseFakeCache, "import \"magus\";\nmagus\\cache.remote({\"name\": \"other-cache\"});\n")
	var conflict *interp.ChoiceConflictError
	s.Require().ErrorAs(err, &conflict)
	s.Equal(`magus\cache.remote`, conflict.Declaration)
	s.ElementsMatch([]string{"fake-cache", "other-cache"}, []string{conflict.First, conflict.Second})
	s.NotEqual(conflict.FirstProject, conflict.SecondProject)
}

func (s *EvalChoicesSuite) TestWithCacheBackendStillConflictsWithAMagusfileBackend() {
	testkit.Isolate(s.T())
	w := testkit.NewWorkspace(s.T())
	w.Magusfile(chooseFakeCache)
	backend, err := cache.NewFSRemoteBackend(s.T().TempDir())
	s.Require().NoError(err)
	cfg := config.Defaults()
	cfg.Cache.Dir = filepath.Join(s.T().TempDir(), ".magus")
	_, err = Open(s.T().Context(), w.Root(), workspace.WithLoadedConfig(cfg), WithSpawnRule(tier.Ceiling),
		WithCacheBackend(backend))
	s.Require().ErrorContains(err, `the magusfile wires magus.cache.remote("fake-cache")`)
}
