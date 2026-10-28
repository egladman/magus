package symbols

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/suite"
)

type symbolsSuite struct{ suite.Suite }

func TestSymbolsSuite(t *testing.T) { suite.Run(t, new(symbolsSuite)) }

const (
	defRole   = int32(scip.SymbolRole_Definition)
	symA      = "magus gomod example.com/m . `example.com/m/a`/A()."
	symAOld   = "magus gomod example.com/m . `example.com/m/a`/Old()."
	symB      = "magus gomod example.com/m . `example.com/m/b`/B()."
	symC      = "magus gomod example.com/m . `example.com/m/c`/C()."
	versionV1 = "fake 1"
)

// fakeIndexer serves shards from a table and records which units Index was asked for.
type fakeIndexer struct {
	version string
	units   []Unit
	shards  map[string]Shard
	asked   []string
}

func (f *fakeIndexer) Version() string { return f.version }

func (f *fakeIndexer) Units(context.Context) ([]Unit, error) { return f.units, nil }

func (f *fakeIndexer) Index(_ context.Context, units []Unit) ([]Shard, error) {
	var out []Shard
	for _, u := range units {
		f.asked = append(f.asked, u.ID)
		out = append(out, f.shards[u.ID])
	}
	return out, nil
}

func shardOf(unit, path string, defs []string, refs ...string) Shard {
	d := Document{Path: path, Language: "go"}
	for i, s := range defs {
		d.Occurrences = append(d.Occurrences, Occurrence{Symbol: s, Range: []int32{int32(i), 5, 6}, Roles: defRole})
		d.Symbols = append(d.Symbols, SymbolInfo{Symbol: s, DisplayName: s})
	}
	for i, s := range refs {
		d.Occurrences = append(d.Occurrences, Occurrence{Symbol: s, Range: []int32{int32(10 + i), 1, 2}})
	}
	return Shard{Unit: unit, Documents: []Document{d}}
}

// threeUnits is a <- b, and c alone: b calls a's Old, c names nothing of a's.
func threeUnits() *fakeIndexer {
	return &fakeIndexer{
		version: versionV1,
		units: []Unit{
			{ID: "a", Digest: "a1"},
			{ID: "b", Digest: "b1", Deps: []string{"a"}},
			{ID: "c", Digest: "c1", Deps: []string{"a"}},
		},
		shards: map[string]Shard{
			"a": shardOf("a", "a/a.go", []string{symA, symAOld}),
			"b": shardOf("b", "b/b.go", []string{symB}, symAOld),
			"c": shardOf("c", "c/c.go", []string{symC}, symA),
		},
	}
}

func (s *symbolsSuite) packPath() string { return filepath.Join(s.T().TempDir(), "scip.pack") }

func (s *symbolsSuite) TestUpdatePackBuildsEveryUnitWhenNoPackExists() {
	path, ix := s.packPath(), threeUnits()
	res, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)
	s.True(res.Rebuilt)
	s.Equal([]string{"a", "b", "c"}, res.Indexed)
	s.Equal([]string{"a", "b", "c"}, ix.asked)

	p, err := openPack(path)
	s.Require().NoError(err)
	defer p.Close()
	s.Equal(versionV1, p.Indexer())
	b, err := p.Shard("b")
	s.Require().NoError(err)
	s.Equal(ix.shards["b"], b)
}

func (s *symbolsSuite) TestUpdatePackIndexesOnlyTheUnitWhoseDigestMoved() {
	path, ix := s.packPath(), threeUnits()
	_, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)

	ix.asked = nil
	ix.units[2].Digest = "c2"
	res, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)
	s.False(res.Rebuilt)
	s.Equal([]string{"c"}, ix.asked)
	s.Equal(2, res.Kept)
	s.Empty(res.Dependents)
}

// A body edit leaves the defined set alone, so no dependent is touched.
func (s *symbolsSuite) TestUpdatePackStopsAtAnUnchangedDefinedSet() {
	path, ix := s.packPath(), threeUnits()
	_, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)

	ix.asked = nil
	ix.units[0].Digest = "a2"
	res, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)
	s.Equal([]string{"a"}, ix.asked)
	s.Empty(res.Dependents)
}

// Removing a symbol re-indexes the dependents that referenced it, and only those.
func (s *symbolsSuite) TestUpdatePackReindexesDependentsReferencingARemovedSymbol() {
	path, ix := s.packPath(), threeUnits()
	_, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)

	ix.asked = nil
	ix.units[0].Digest = "a2"
	ix.shards["a"] = shardOf("a", "a/a.go", []string{symA})
	res, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)
	s.Equal([]string{"a", "b"}, ix.asked)
	s.Equal([]string{"b"}, res.Dependents)
	s.Equal(1, res.Kept)
}

func (s *symbolsSuite) TestUpdatePackDropsAUnitNoLongerDeclared() {
	path, ix := s.packPath(), threeUnits()
	_, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)

	ix.asked = nil
	ix.units = slices.Delete(ix.units, 2, 3)
	res, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)
	s.Equal([]string{"c"}, res.Dropped)
	s.Empty(ix.asked)

	p, err := openPack(path)
	s.Require().NoError(err)
	defer p.Close()
	_, ok := p.Entry("c")
	s.False(ok)
}

func (s *symbolsSuite) TestUpdatePackRebuildsAPackFromAnotherIndexerVersion() {
	path, ix := s.packPath(), threeUnits()
	_, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)

	ix.asked, ix.version = nil, "fake 2"
	res, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)
	s.True(res.Rebuilt)
	s.Equal([]string{"a", "b", "c"}, ix.asked)
}

func (s *symbolsSuite) TestOpenPackRefusesAnotherFile() {
	path := s.packPath()
	s.Require().NoError(os.WriteFile(path, []byte("not a pack, just bytes long enough to have a trailer"), 0o644))
	_, err := openPack(path)
	s.ErrorIs(err, ErrIndexFormat)
}

func (s *symbolsSuite) TestOpenPackMissingIsNotExist() {
	_, err := openPack(s.packPath())
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *symbolsSuite) TestShardOfAnAbsentUnitIsNotExist() {
	path, ix := s.packPath(), threeUnits()
	_, err := UpdatePack(s.T().Context(), path, ix)
	s.Require().NoError(err)
	p, err := openPack(path)
	s.Require().NoError(err)
	defer p.Close()
	_, err = p.Shard("zz")
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *symbolsSuite) TestDefinedIsSortedAndSkipsReferences() {
	sh := shardOf("a", "a/a.go", []string{symAOld, symA}, symB)
	s.Equal([]string{symA, symAOld}, sh.Defined())
}
