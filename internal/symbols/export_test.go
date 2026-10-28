package symbols

import (
	"os"
	"path/filepath"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

const (
	symIface       = "magus gomod example.com/m . `example.com/m/a`/Reader#"
	symIfaceRead   = "magus gomod example.com/m . `example.com/m/a`/Reader#Read."
	symImpl        = "magus gomod example.com/m . `example.com/m/b`/File#"
	symImplRead    = "magus gomod example.com/m . `example.com/m/b`/File#Read()."
	symPartial     = "magus gomod example.com/m . `example.com/m/b`/Half#"
	readSignature  = "func(p []byte) (int, error)"
	closeSignature = "func() error"
)

func methodShards() []Shard {
	a := shardOf("a", "a/a.go", []string{symIface, symIfaceRead})
	a.MethodSets = []MethodSet{{Symbol: symIface, Interface: true, Methods: []Method{
		{Name: "Read", Signature: readSignature, Symbol: symIfaceRead},
		{Name: "Close", Signature: closeSignature},
	}}}
	b := shardOf("b", "b/b.go", []string{symImpl, symImplRead, symPartial})
	b.MethodSets = []MethodSet{
		{Symbol: symImpl, Methods: []Method{
			{Name: "Close", Signature: closeSignature},
			{Name: "Read", Signature: readSignature, Symbol: symImplRead},
		}},
		{Symbol: symPartial, Methods: []Method{{Name: "Read", Signature: readSignature}}},
	}
	return []Shard{a, b}
}

func infoOf(idx *scip.Index, symbol string) *scip.SymbolInformation {
	for _, d := range idx.Documents {
		for _, si := range d.Symbols {
			if si.Symbol == symbol {
				return si
			}
		}
	}
	return nil
}

func implemented(si *scip.SymbolInformation) []string {
	var out []string
	for _, r := range si.Relationships {
		if r.IsImplementation {
			out = append(out, r.Symbol)
		}
	}
	return out
}

// An interface in one shard relates to an implementer in another, though neither shard
// records the other.
func (s *symbolsSuite) TestPackIndexDerivesImplementationsAcrossShards() {
	idx := packIndex(methodShards())
	s.Equal([]string{symIface}, implemented(infoOf(idx, symImpl)))
	s.Equal([]string{symIfaceRead}, implemented(infoOf(idx, symImplRead)))
}

func (s *symbolsSuite) TestPackIndexSkipsATypeMissingAMethod() {
	idx := packIndex(methodShards())
	s.Empty(implemented(infoOf(idx, symPartial)))
}

func (s *symbolsSuite) TestPackIndexKeepsDocumentsAndRanges() {
	idx := packIndex(methodShards())
	s.Require().Len(idx.Documents, 2)
	s.Equal("a/a.go", idx.Documents[0].RelativePath)
	r, ok := idx.Documents[0].Occurrences[0].SourceRange()
	s.Require().True(ok)
	s.Equal(scip.Position{Line: 0, Character: 5}, r.Start)
}

func (s *symbolsSuite) TestReadIndexAssemblesAPackWhole() {
	pack := filepath.Join(s.T().TempDir(), "scip.pack")
	s.Require().NoError(writePack(pack, versionV1, []Unit{{ID: "a"}, {ID: "b"}}, methodShards()))

	idx, err := ReadIndex(pack, true)
	s.Require().NoError(err)
	s.Len(idx.Documents, 2)
	s.Equal([]string{symIface}, implemented(infoOf(idx, symImpl)))
}

// A SCIP index an external indexer wrote is stored one shard per directory and reads back
// to the same documents.
func (s *symbolsSuite) TestPackFromSCIPRoundTrips() {
	dir := s.T().TempDir()
	src, pack := filepath.Join(dir, "in.scip"), filepath.Join(dir, "scip-buzz.pack")
	want := packIndex(methodShards())
	data, err := proto.Marshal(want)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(src, data, 0o644))

	s.Require().NoError(PackFromSCIP(src, pack, "scip scip-buzz"))
	p, err := openPack(pack)
	s.Require().NoError(err)
	defer p.Close()
	s.Equal([]string{"a", "b"}, []string{p.Entries()[0].Unit, p.Entries()[1].Unit})

	got, err := ReadIndex(pack, true)
	s.Require().NoError(err)
	s.Require().Len(got.Documents, 2)
	s.Equal(want.Documents[1].RelativePath, got.Documents[1].RelativePath)
	s.Len(got.Documents[1].Occurrences, len(want.Documents[1].Occurrences))
}

func (s *symbolsSuite) TestReadIndexRefusesBytesThatAreNotSCIP() {
	path := filepath.Join(s.T().TempDir(), "index.scip")
	s.Require().NoError(os.WriteFile(path, []byte{0xff, 0xff, 0xff}, 0o644))
	_, err := ReadIndex(path, false)
	s.ErrorIs(err, ErrIndexFormat)
}
