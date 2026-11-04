package buzzindex

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/symbols"
)

type buzzindexSuite struct {
	suite.Suite
	dir string
}

func TestBuzzindexSuite(t *testing.T) { suite.Run(t, new(buzzindexSuite)) }

const greetSource = `// greet says hello.
export fun greet(name: str) > str {
    return "hello {name}";
}

fun helper() > int {
    return 1;
}
`

func (s *buzzindexSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.write("a.buzz", greetSource)
	s.write("b.buzz", `import "a";

export fun main(args: [str]) > void {
    final message = greet("x");
}
`)
	s.write("broken.buzz", "fun ( {\n")
	s.write("nested/magusfile.buzz", "fun main(args: [str]) > void {}\n")
	s.write("nested/c.buzz", "fun c() > void {}\n")
}

func (s *buzzindexSuite) write(rel, content string) {
	path := filepath.Join(s.dir, filepath.FromSlash(rel))
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o644))
}

func (s *buzzindexSuite) indexer() *Indexer {
	return &Indexer{Dir: s.dir, WorkspaceRoot: s.dir}
}

func (s *buzzindexSuite) shards() map[string]symbols.Shard {
	ix := s.indexer()
	units, err := ix.Units(s.T().Context())
	s.Require().NoError(err)
	shards, err := ix.Index(s.T().Context(), units)
	s.Require().NoError(err)
	out := map[string]symbols.Shard{}
	for _, sh := range shards {
		out[sh.Unit] = sh
	}
	return out
}

func greetSymbol() string { return "scip-buzz buzz . . `a.buzz`/greet()." }

func (s *buzzindexSuite) TestUnitsAreOnePerFileOutsideNestedProjects() {
	units, err := s.indexer().Units(s.T().Context())
	s.Require().NoError(err)
	deps := map[string][]string{}
	for _, u := range units {
		deps[u.ID] = u.Deps
	}
	s.Equal(map[string][]string{"a.buzz": nil, "b.buzz": {"a.buzz"}, "broken.buzz": nil}, deps)
}

func (s *buzzindexSuite) TestDefinitionCarriesItsBodyDocAndSignature() {
	doc := s.shards()["a.buzz"].Documents[0]
	var def symbols.Occurrence
	for _, occ := range doc.Occurrences {
		if occ.Symbol == greetSymbol() && occ.Roles&int32(scip.SymbolRole_Definition) != 0 {
			def = occ
		}
	}
	s.Equal(symbols.Occurrence{Symbol: greetSymbol(), Range: []int32{1, 11, 16}, Roles: int32(scip.SymbolRole_Definition), Enclosing: []int32{1, 0, 3, 1}}, def)
	var info symbols.SymbolInfo
	for _, si := range doc.Symbols {
		if si.Symbol == greetSymbol() {
			info = si
		}
	}
	want := info // kind and relationships are asserted by the cases around this one
	want.Documentation, want.Signature = []string{"greet says hello."}, "fun greet(name: str) > str"
	s.Equal(want, info)
}

func (s *buzzindexSuite) TestReferenceResolvesIntoTheImportedFile() {
	var refs []symbols.Occurrence
	for _, occ := range s.shards()["b.buzz"].Documents[0].Occurrences {
		if occ.Symbol == greetSymbol() {
			refs = append(refs, occ)
		}
	}
	s.Equal([]symbols.Occurrence{{Symbol: greetSymbol(), Range: []int32{3, 20, 25}}}, refs)
}

func (s *buzzindexSuite) TestParseFailureIsAGapNamingTheFile() {
	shard := s.shards()["broken.buzz"]
	s.Empty(shard.Documents)
	s.Require().Len(shard.Gaps, 1)
	s.True(strings.HasPrefix(shard.Gaps[0], "broken.buzz: "), shard.Gaps[0])
}

func (s *buzzindexSuite) TestGapIsNamedAgainOnAnUpdateThatKeepsItsShard() {
	pack := filepath.Join(s.T().TempDir(), "buzz.pack")
	_, err := symbols.UpdatePack(s.T().Context(), pack, s.indexer())
	s.Require().NoError(err)
	res, err := symbols.UpdatePack(s.T().Context(), pack, s.indexer())
	s.Require().NoError(err)
	s.Empty(res.Indexed)
	s.Require().Len(res.Gaps, 1)
	s.True(strings.HasPrefix(res.Gaps[0], "broken.buzz: "), res.Gaps[0])
}

func (s *buzzindexSuite) updated(edit func()) symbols.UpdateResult {
	pack := filepath.Join(s.T().TempDir(), "buzz.pack")
	_, err := symbols.UpdatePack(s.T().Context(), pack, s.indexer())
	s.Require().NoError(err)
	edit()
	res, err := symbols.UpdatePack(s.T().Context(), pack, s.indexer())
	s.Require().NoError(err)
	return res
}

func (s *buzzindexSuite) TestBodyEditReindexesOneFile() {
	res := s.updated(func() { s.write("a.buzz", strings.Replace(greetSource, `"hello {name}"`, `"hi {name}"`, 1)) })
	s.Equal([]string{"a.buzz"}, res.Indexed)
	s.Empty(res.Dependents)
	s.Equal(2, res.Kept)
}

func (s *buzzindexSuite) TestAddedExportReindexesItsImporters() {
	res := s.updated(func() { s.write("a.buzz", greetSource+"\nexport fun wave() > void {}\n") })
	s.Equal([]string{"a.buzz", "b.buzz"}, res.Indexed)
}

func (s *buzzindexSuite) TestRenamedExportLeavesTheImporterUnresolved() {
	s.updated(func() { s.write("a.buzz", strings.Replace(greetSource, "fun greet(", "fun hello(", 1)) })
	for _, occ := range s.shards()["b.buzz"].Documents[0].Occurrences {
		s.NotEqual(greetSymbol(), occ.Symbol)
	}
}

const (
	benchFiles = 120
	benchFuncs = 20
)

// writeBenchProject generates benchFiles modules, each importing the one before it, so an
// edit low in the chain has every other file as a dependent.
func writeBenchProject(b *testing.B, dir string) {
	b.Helper()
	for f := range benchFiles {
		var src strings.Builder
		if f > 0 {
			fmt.Fprintf(&src, "import \"m%03d\";\n\n", f-1)
		}
		for fn := range benchFuncs {
			fmt.Fprintf(&src, "// f%d_%d is generated.\nexport fun f%d_%d(x: int) > int {\n    return x + %d;\n}\n\n", f, fn, f, fn, fn)
		}
		if f > 0 {
			fmt.Fprintf(&src, "export fun last%d() > int {\n    return f%d_0(1);\n}\n", f, f-1)
		}
		path := filepath.Join(dir, fmt.Sprintf("m%03d.buzz", f))
		if err := os.WriteFile(path, []byte(src.String()), 0o644); err != nil {
			b.Fatal(err)
		}
	}
}

// removePack deletes pack so the next update rebuilds it whole; one already gone is fine.
func removePack(b *testing.B, pack string) {
	b.Helper()
	if err := os.Remove(pack); err != nil && !errors.Is(err, fs.ErrNotExist) {
		b.Fatal(err)
	}
}

// BenchmarkFullIndex is a cold index of the generated project, every file bound.
func BenchmarkFullIndex(b *testing.B) {
	dir := b.TempDir()
	writeBenchProject(b, dir)
	pack := filepath.Join(b.TempDir(), "buzz.pack")
	for b.Loop() {
		removePack(b, pack)
		if _, err := symbols.UpdatePack(b.Context(), pack, &Indexer{Dir: dir, WorkspaceRoot: dir}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOneLineEditAtTheRoot is a body edit in the module every other one imports
// through the chain: its exports hold, so one unit.
func BenchmarkOneLineEditAtTheRoot(b *testing.B) {
	dir := b.TempDir()
	writeBenchProject(b, dir)
	pack := filepath.Join(b.TempDir(), "buzz.pack")
	if _, err := symbols.UpdatePack(b.Context(), pack, &Indexer{Dir: dir, WorkspaceRoot: dir}); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(dir, "m000.buzz")
	orig, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	versions := [][]byte{[]byte(strings.Replace(string(orig), "return x + 3;", "return x + 4;", 1)), orig}
	i := 0
	for b.Loop() {
		if err := os.WriteFile(path, versions[i%2], 0o644); err != nil {
			b.Fatal(err)
		}
		i++
		res, err := symbols.UpdatePack(b.Context(), pack, &Indexer{Dir: dir, WorkspaceRoot: dir})
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Indexed) != 1 {
			b.Fatalf("indexed %v, want one unit", res.Indexed)
		}
	}
}

// dirtyUnit is an Indexer whose one unit reads as edited on every Units call, so a
// benchmark measures an edit to a real tree without writing to it.
type dirtyUnit struct {
	*Indexer
	unit string
	n    int
}

func (d *dirtyUnit) Units(ctx context.Context) ([]symbols.Unit, error) {
	units, err := d.Indexer.Units(ctx)
	for i := range units {
		if units[i].ID == d.unit {
			units[i].Digest += strconv.Itoa(d.n)
		}
	}
	d.n++
	return units, err
}

// workspaceRoot is this repository's root, three levels above the package directory go
// test runs in.
func workspaceRoot(b *testing.B) string {
	wd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", "..")
}

// BenchmarkWorkspaceFullIndex is a cold index of this repository's root project's Buzz,
// reporting how many units it indexed and how many gaps it left.
func BenchmarkWorkspaceFullIndex(b *testing.B) {
	root := workspaceRoot(b)
	pack := filepath.Join(b.TempDir(), "buzz.pack")
	var res symbols.UpdateResult
	for b.Loop() {
		removePack(b, pack)
		var err error
		if res, err = symbols.UpdatePack(b.Context(), pack, &Indexer{Dir: root, WorkspaceRoot: root}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(res.Indexed)), "units")
	b.ReportMetric(float64(len(res.Gaps)), "gaps")
	for _, gap := range res.Gaps {
		b.Log(gap)
	}
}

// BenchmarkWorkspaceEditMagusfile is an edit to the root magusfile, which nothing imports.
func BenchmarkWorkspaceEditMagusfile(b *testing.B) {
	root := workspaceRoot(b)
	pack := filepath.Join(b.TempDir(), "buzz.pack")
	ix := &dirtyUnit{Indexer: &Indexer{Dir: root, WorkspaceRoot: root}, unit: "magusfile.buzz"}
	if _, err := symbols.UpdatePack(b.Context(), pack, ix); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		res, err := symbols.UpdatePack(b.Context(), pack, ix)
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Indexed) != 1 {
			b.Fatalf("indexed %v, want magusfile.buzz alone", res.Indexed)
		}
	}
}
