package image

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// mixedSpec places one of each item kind on scratch.
func (s *layerSuite) mixedSpec() Spec {
	root := s.T().TempDir()
	app := write(s.T(), filepath.Join(root, "app"), "binary", 0o755)
	return Spec{Scratch: true, Platform: amd64,
		Files:       []File{{Source: app, Path: "/usr/local/bin/app", Mode: ModeExecutable}},
		Trees:       []Tree{{Source: s.hostTree(), Path: "/", Exclude: []string{"bin"}, ModePolicy: ModePolicyFixed, Symlinks: SymlinkPolicyRefuse, UID: 7, GID: 8}},
		Symlinks:    []Symlink{{Path: "/bin/app", Target: "../usr/local/bin/app"}},
		Directories: []Directory{{Path: "/tmp", Mode: ModeSticky}},
	}
}

func (s *layerSuite) TestEntriesListsEveryEntryWithTheItemThatPlacedIt() {
	got, err := Entries(s.T().Context(), s.mixedSpec())
	s.Require().NoError(err)
	epoch := time.Unix(0, 0)
	dir := func(p string) Entry {
		return Entry{Path: p, Kind: EntryDirectory, Mode: ModeDirectory, ModTime: epoch, Origin: "parent"}
	}
	s.Equal([]Entry{
		dir("/bin"),
		{Path: "/bin/app", Kind: EntrySymlink, Mode: 0o777, Target: "../usr/local/bin/app", ModTime: epoch, Origin: "symlinks[0]"},
		dir("/etc"),
		{Path: "/etc/nsswitch.conf", Kind: EntryFile, Mode: ModeFile, UID: 7, GID: 8, Size: 13, ModTime: epoch, Origin: "trees[0] etc/nsswitch.conf"},
		dir("/etc/ssl"),
		{Path: "/etc/ssl/ca.pem", Kind: EntryFile, Mode: ModeFile, UID: 7, GID: 8, Size: 3, ModTime: epoch, Origin: "trees[0] etc/ssl/ca.pem"},
		{Path: "/tmp", Kind: EntryDirectory, Mode: ModeSticky, ModTime: epoch, Origin: "directories[0]"},
		dir("/usr"),
		dir("/usr/local"),
		dir("/usr/local/bin"),
		{Path: "/usr/local/bin/app", Kind: EntryFile, Mode: ModeExecutable, Size: 6, ModTime: epoch, Origin: "files[0]"},
	}, got)
}

func (s *layerSuite) TestAssembleWritesExactlyTheEntriesListed() {
	spec := s.mixedSpec()
	listed, err := Entries(s.T().Context(), spec)
	s.Require().NoError(err)
	m, err := Assemble(s.T().Context(), spec, filepath.Join(s.T().TempDir(), "out"))
	s.Require().NoError(err)
	s.Require().NotNil(m.Added)
	s.Equal(listed, m.Added.Entries, "the receipt is the list")

	headers := layerHeaders(s.T(), m)
	s.Len(headers, len(listed))
	for _, e := range listed {
		name := e.Path[1:]
		if e.Kind == EntryDirectory {
			name += "/"
		}
		h, ok := headers[name]
		s.Require().True(ok, name)
		s.Equal([]any{int64(e.Mode), e.UID, e.GID, e.Size, e.Target, e.ModTime.Unix()},
			[]any{h.Mode, h.Uid, h.Gid, h.Size, h.Linkname, h.ModTime.Unix()}, name)
	}
}

func (s *layerSuite) TestEntriesOverABaseAddsNoParents() {
	root := s.T().TempDir()
	app := write(s.T(), filepath.Join(root, "app"), "binary", 0o755)
	got, err := Entries(s.T().Context(), Spec{Base: filepath.Join(root, "unread"), Platform: amd64,
		Files: []File{{Source: app, Path: "/usr/local/bin/app", Mode: ModeExecutable}}})
	s.Require().NoError(err, "the base is never opened")
	s.Equal([]Entry{{Path: "/usr/local/bin/app", Kind: EntryFile, Mode: ModeExecutable, Size: 6, ModTime: time.Unix(0, 0), Origin: "files[0]"}}, got)
}

func (s *layerSuite) TestEntriesRefusesAFileSourceThatIsNotRegular() {
	_, err := Entries(s.T().Context(), Spec{Scratch: true, Platform: amd64,
		Files: []File{{Source: s.T().TempDir(), Path: "/x", Mode: ModeFile}}})
	s.Require().ErrorContains(err, "files[0] /x")
	s.Require().ErrorContains(err, "not a regular file")
}

type layerSuite struct{ suite.Suite }

func TestLayerSuite(t *testing.T) { suite.Run(t, new(layerSuite)) }

// hostTree holds a config, a CA bundle one directory down, and a tool
// without its execute bit.
func (s *layerSuite) hostTree() string {
	tree := filepath.Join(s.T().TempDir(), "tree")
	write(s.T(), filepath.Join(tree, "etc", "nsswitch.conf"), "hosts: files\n", 0o644)
	write(s.T(), filepath.Join(tree, "etc", "ssl", "ca.pem"), "pem", 0o644)
	write(s.T(), filepath.Join(tree, "bin", "tool"), "tool", 0o644)
	return tree
}

func (s *layerSuite) TestATreeMatchingNoFileIsRefusedNamingItsInclude() {
	dest := filepath.Join(s.T().TempDir(), "out")
	_, err := Assemble(s.T().Context(), Spec{Scratch: true, Platform: amd64, Trees: []Tree{
		{Source: s.hostTree(), Path: "/", Include: []string{"*.conf"}, ModePolicy: ModePolicyFixed, Symlinks: SymlinkPolicyRefuse},
	}}, dest)
	s.Require().ErrorContains(err, `trees[0]`)
	s.Require().ErrorContains(err, `include ["*.conf"] matches no file`)
	s.NoDirExists(dest, "a tree that places nothing writes no image")
}

func (s *layerSuite) TestADirectoryExcludeDropsEverythingBelowIt() {
	root := s.T().TempDir()
	m, err := Assemble(s.T().Context(), Spec{Scratch: true, Platform: amd64, Trees: []Tree{
		{Source: s.hostTree(), Path: "/", Exclude: []string{"etc/ssl"}, ModePolicy: ModePolicyFixed, Symlinks: SymlinkPolicyRefuse},
	}}, filepath.Join(root, "out"))
	s.Require().NoError(err)
	names := []string{}
	for name := range layerHeaders(s.T(), m) {
		names = append(names, name)
	}
	s.ElementsMatch([]string{"bin/", "bin/tool", "etc/", "etc/nsswitch.conf"}, names)
}

func (s *layerSuite) TestExecutableBitOverATreeWithNoExecutableFileIsRefused() {
	dest := filepath.Join(s.T().TempDir(), "out")
	_, err := Assemble(s.T().Context(), Spec{Scratch: true, Platform: amd64, Trees: []Tree{
		{Source: s.hostTree(), Path: "/opt", ModePolicy: ModePolicyExecutableBit, Symlinks: SymlinkPolicyRefuse},
	}}, dest)
	s.Require().ErrorContains(err, "trees[0]")
	s.Require().ErrorContains(err, "none of its 3 files has the host owner-execute bit")
	s.Require().ErrorContains(err, "bin/tool")
	s.NoDirExists(dest)
}

func (s *layerSuite) TestAManifestWithNoLayerWritesAnEmptyLayersArray() {
	m, err := Assemble(s.T().Context(), Spec{Scratch: true, Platform: amd64}, filepath.Join(s.T().TempDir(), "out"))
	s.Require().NoError(err)
	data, err := os.ReadFile(blobPath(m.Path, m.Descriptor.Digest))
	s.Require().NoError(err)
	s.Contains(string(data), `"layers":[]`, "image-spec types layers as an array, never null")
	s.Nil(m.Added)
}
