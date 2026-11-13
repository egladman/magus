package contract

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// revisionFiles is version control holding each revision's files by path.
type revisionFiles map[string]map[string]string

func (r revisionFiles) ReadFileAt(_ context.Context, _, rev, p string) (string, error) {
	body, ok := r[rev][p]
	if !ok {
		return "", fmt.Errorf("%s is absent at %s", p, rev)
	}
	return body, nil
}

func (r revisionFiles) ListFilesAt(_ context.Context, _, rev, dir string) ([]string, error) {
	files, ok := r[rev]
	if !ok {
		return nil, fmt.Errorf("unknown revision %s", rev)
	}
	var out []string
	for p := range files {
		if strings.HasPrefix(p, dir+"/") {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out, nil
}

// embeddedFiles is the contract this test binary embeds, by path.
func (s *DigestSuite) embeddedFiles() map[string]string {
	files := map[string]string{lockPath: string(commands.Lock())}
	buzz := spell.GeneratedBuzz()
	s.Require().NoError(fs.WalkDir(buzz, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(buzz, p)
		files[spellDir+"/"+p] = string(body)
		return err
	}))
	return files
}

func (s *DigestSuite) TestARevisionCarryingTheBinarysFilesAgrees() {
	vc := revisionFiles{"auth": s.embeddedFiles()}
	skew, err := CheckAt(s.T().Context(), vc, s.root, "auth")
	s.Require().NoError(err)
	s.False(skew.Differs())
	s.Equal("auth", skew.Revision)
}

func (s *DigestSuite) TestTheRevisionIsReadAndNotTheWorkingTree() {
	s.carry()
	s.write(filepath.Join(spellDir, "gen/decls/fs.buzz"), []byte("export extern fun newer() > void;\n"))
	moved, err := Check(s.root)
	s.Require().NoError(err)
	s.Require().True(moved.Differs(), "the working tree carries another contract")

	skew, err := CheckAt(s.T().Context(), revisionFiles{"auth": s.embeddedFiles()}, s.root, "auth")
	s.Require().NoError(err)
	s.False(skew.Differs())
}

func (s *DigestSuite) TestAChangedDeclarationAtTheRevisionDiffers() {
	files := s.embeddedFiles()
	files[spellDir+"/gen/decls/fs.buzz"] = "export extern fun newer() > void;\n"
	skew, err := CheckAt(s.T().Context(), revisionFiles{"auth": files}, s.root, "auth")
	s.Require().NoError(err)
	s.True(skew.Differs())
	s.Contains(skew.Diagnostic().Error(), s.root+" at auth")
}

func (s *DigestSuite) TestTheRevisionLeavesOutWhatEmbedLeavesOut() {
	files := s.embeddedFiles()
	files[spellDir+"/gen/decls/.hidden.buzz"] = "x"
	files[spellDir+"/gen/decls/notes.txt"] = "x"
	files[spellDir+"/other/outside.buzz"] = "x"
	skew, err := CheckAt(s.T().Context(), revisionFiles{"auth": files}, s.root, "auth")
	s.Require().NoError(err)
	s.False(skew.Differs())
}

func (s *DigestSuite) TestARevisionCarryingNoContractNeverDiffers() {
	vc := revisionFiles{"auth": {"README.md": "x"}}
	_, err := TreeAt(s.T().Context(), vc, s.root, "auth")
	s.ErrorIs(err, ErrNotCarried)
	skew, err := CheckAt(s.T().Context(), vc, s.root, "auth")
	s.Require().NoError(err)
	s.False(skew.Differs())
}

func (s *DigestSuite) TestAnUnreadableRevisionIsAnError() {
	_, err := CheckAt(s.T().Context(), revisionFiles{}, s.root, "gone")
	s.Require().Error(err)
	s.NotErrorIs(err, ErrNotCarried)
}
