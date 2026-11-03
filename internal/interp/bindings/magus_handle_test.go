package bindings

import (
	"os"
	"path/filepath"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// checkoutWorkspace is rooted at a real checkout, so a Change has a tree
// to record.
type checkoutWorkspace struct {
	jobNamespaceWorkspace
}

func (checkoutWorkspace) VCSOptions() types.VCSOptions { return types.VCSOptions{} }

// execMagusScript loads src over ws and returns a caller for its exports.
// The call carries ws too: host members read the call-time ctx.
func execMagusScript(t *testing.T, ws types.WorkspaceRepository, src string) func(export string, args ...vm.Value) (vm.Value, error) {
	t.Helper()
	ctx := types.WithWorkspace(t.Context(), ws)
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModules(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	if err := DeclareMagusTypes(sess); err != nil {
		t.Fatal(err)
	}
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatal(err)
	}
	return func(export string, args ...vm.Value) (vm.Value, error) {
		fn, ok := sess.Exports()[export]
		if !ok {
			t.Fatalf("export %q is missing", export)
		}
		return sess.CallValue(ctx, fn, args)
	}
}

const changeScript = `
import "magus";

export fun build() > magus\Change !> any {
    return magus\change();
}

export fun revision(c: magus\Change) > str !> any {
    return c.at.revision;
}

export fun mergeBase(c: magus\Change) > str !> any {
    return c.mergeBase();
}

export fun literal() > str !> any {
    return magus\Change{}.mergeBase();
}
`

type changeHandleSuite struct {
	suite.Suite
	root string
	call func(export string, args ...vm.Value) (vm.Value, error)
}

func TestChangeHandle(t *testing.T) { suite.Run(t, new(changeHandleSuite)) }

func (s *changeHandleSuite) SetupTest() {
	testkit.Isolate(s.T())
	repo := testkit.NewRepository(s.T(), map[string]string{"a.txt": "a\n"})
	s.root = repo.Root()
	ws := &checkoutWorkspace{jobNamespaceWorkspace{cacheDir: s.T().TempDir(), root: s.root}}
	s.call = execMagusScript(s.T(), ws, changeScript)
}

func (s *changeHandleSuite) build() vm.Value {
	c, err := s.call("build")
	s.Require().NoError(err)
	return c
}

func (s *changeHandleSuite) TestRecordsTheTreeItWasBuiltOn() {
	got, err := s.call("revision", s.build())
	s.Require().NoError(err)
	s.NotEmpty(got.AsString())
}

func (s *changeHandleSuite) TestRefusesOnceAPathMoved() {
	c := s.build()
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "a.txt"), []byte("moved\n"), 0o600))

	_, err := s.call("mergeBase", c)
	s.Require().ErrorContains(err, "the tree moved since magus\\change(...) built this Change: a.txt")
}

func (s *changeHandleSuite) TestRefusesALiteralWithNoTree() {
	_, err := s.call("literal")
	s.Require().ErrorContains(err, "records no tree; build it with magus\\change(...)")
}
