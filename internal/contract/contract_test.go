package contract

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/types"
)

type DigestSuite struct {
	suite.Suite
	root string
}

func TestDigestSuite(t *testing.T) {
	suite.Run(t, new(DigestSuite))
}

func (s *DigestSuite) SetupTest() {
	s.root = s.T().TempDir()
}

// carry writes the contract this test binary embeds into s.root.
func (s *DigestSuite) carry() {
	s.write(lockPath, commands.Lock())
	buzz := spell.GeneratedBuzz()
	s.Require().NoError(fs.WalkDir(buzz, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(buzz, p)
		if err != nil {
			return err
		}
		s.write(filepath.Join(spellDir, p), body)
		return nil
	}))
}

func (s *DigestSuite) write(rel string, body []byte) {
	p := filepath.Join(s.root, filepath.FromSlash(rel))
	s.Require().NoError(os.MkdirAll(filepath.Dir(p), 0o755))
	s.Require().NoError(os.WriteFile(p, body, 0o644))
}

func (s *DigestSuite) TestATreeCarryingTheBinarysFilesAgrees() {
	s.carry()
	skew, err := Check(s.root)
	s.Require().NoError(err)
	s.False(skew.Differs())
	s.Nil(skew.Diagnostic())
}

func (s *DigestSuite) TestAChangedDeclarationDiffers() {
	s.carry()
	s.write(filepath.Join(spellDir, "gen/decls/fs.buzz"), []byte("export extern fun newer() > void;\n"))
	skew, err := Check(s.root)
	s.Require().NoError(err)
	s.True(skew.Differs())
	d := skew.Diagnostic()
	s.Require().NotNil(d)
	s.Equal(types.WorkspaceNeedsNewerMagus, d.Code)
	s.Contains(d.Error(), fmt.Sprintf("go -C %s build -trimpath -o magus ./cmd/magus", s.root))
}

func (s *DigestSuite) TestAnAddedDeclarationDiffers() {
	s.carry()
	s.write(filepath.Join(spellDir, "gen/types/newer.buzz"), []byte("export object Newer {}\n"))
	skew, err := Check(s.root)
	s.Require().NoError(err)
	s.True(skew.Differs())
}

func (s *DigestSuite) TestAChangedLockDiffers() {
	s.carry()
	s.write(lockPath, append(commands.Lock(), "config newer.key\n"...))
	skew, err := Check(s.root)
	s.Require().NoError(err)
	s.True(skew.Differs())
}

func (s *DigestSuite) TestFilesEmbedLeavesOutAreNotHashed() {
	s.carry()
	s.write(filepath.Join(spellDir, "gen/decls/.DS_Store"), []byte("x"))
	s.write(filepath.Join(spellDir, "gen/decls/notes.txt"), []byte("x"))
	skew, err := Check(s.root)
	s.Require().NoError(err)
	s.False(skew.Differs())
}

func (s *DigestSuite) TestATreeCarryingNoContractNeverDiffers() {
	_, err := Tree(s.root)
	s.ErrorIs(err, ErrNotCarried)
	skew, err := Check(s.root)
	s.Require().NoError(err)
	s.False(skew.Differs())
}

func (s *DigestSuite) TestTheDigestIsOrderFree() {
	a := fstest.MapFS{"gen/decls/a.buzz": {Data: []byte("a")}, "gen/types/b.buzz": {Data: []byte("b")}}
	b := fstest.MapFS{"gen/types/b.buzz": {Data: []byte("b")}, "gen/decls/a.buzz": {Data: []byte("a")}}
	da, err := digest([]byte("lock"), a)
	s.Require().NoError(err)
	db, err := digest([]byte("lock"), b)
	s.Require().NoError(err)
	s.Equal(da, db)
}

func (s *DigestSuite) TestMovingBytesBetweenFilesChangesTheDigest() {
	a := fstest.MapFS{"gen/decls/a.buzz": {Data: []byte("ab")}, "gen/types/b.buzz": {Data: []byte("")}}
	b := fstest.MapFS{"gen/decls/a.buzz": {Data: []byte("a")}, "gen/types/b.buzz": {Data: []byte("b")}}
	da, err := digest(nil, a)
	s.Require().NoError(err)
	db, err := digest(nil, b)
	s.Require().NoError(err)
	s.NotEqual(da, db)
}

func (s *DigestSuite) TestReadsFormatReadsTheStoreLines() {
	s.write(lockPath, []byte("config a\nstore magus.job/v1alpha1\n"))
	reads, err := ReadsFormat(s.root, types.JobAPIVersion)
	s.Require().NoError(err)
	s.True(reads)
	reads, err = ReadsFormat(s.root, types.EventAPIVersion)
	s.Require().NoError(err)
	s.False(reads)
}

func (s *DigestSuite) TestReadsFormatOfATreeCarryingNoContract() {
	_, err := ReadsFormat(s.root, types.JobAPIVersion)
	s.ErrorIs(err, ErrNotCarried)
}

func (s *DigestSuite) TestTheEmbeddedLockNamesTheJobStore() {
	reads, err := ReadsFormat(s.writeLock(), types.JobAPIVersion)
	s.Require().NoError(err)
	s.True(reads, "this build reads the rows it writes")
}

func (s *DigestSuite) writeLock() string {
	s.write(lockPath, commands.Lock())
	return s.root
}
