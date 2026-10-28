package shindex

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

type shindexSuite struct {
	suite.Suite
	dir string
}

func TestShindexSuite(t *testing.T) { suite.Run(t, new(shindexSuite)) }

const libSource = `# greet prints a greeting.
greet() {
  local who="$1"
  echo "hello $who $PREFIX"
}

PREFIX="hi"
readonly LIMIT=3
`

const mainSource = `#!/bin/sh
. "$(dirname "$0")/lib.sh"
COUNT=0
run() {
  COUNT=$((COUNT + LIMIT))
  greet "$COUNT"
}
run
`

func (s *shindexSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.write("lib.sh", libSource)
	s.write("main.sh", mainSource)
	s.write("broken.sh", "greet() {\n  echo\n")
	s.write("tasks/task-lib.sh", "task_fn() { :; }\n")
	s.write("tasks/x/check.bash", ". \"$(cd \"$(dirname \"$0\")/..\" && pwd)/task-lib.sh\"\ntask_fn\n")
	s.write("nested/magusfile.buzz", "fun main(args: [str]) > void {}\n")
	s.write("nested/n.sh", "n() { :; }\n")
}

func (s *shindexSuite) write(rel, content string) {
	path := filepath.Join(s.dir, filepath.FromSlash(rel))
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o644))
}

func (s *shindexSuite) indexer() *Indexer {
	return &Indexer{Dir: s.dir, WorkspaceRoot: s.dir}
}

func (s *shindexSuite) shards() map[string]symbols.Shard {
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

// occurrences is every occurrence of sym in unit's document.
func (s *shindexSuite) occurrences(unit, sym string) []symbols.Occurrence {
	var out []symbols.Occurrence
	for _, occ := range s.shards()[unit].Documents[0].Occurrences {
		if occ.Symbol == sym {
			out = append(out, occ)
		}
	}
	return out
}

func (s *shindexSuite) info(unit, sym string) symbols.SymbolInfo {
	for _, si := range s.shards()[unit].Documents[0].Symbols {
		if si.Symbol == sym {
			return si
		}
	}
	s.Failf("no symbol information", "%s in %s", sym, unit)
	return symbols.SymbolInfo{}
}

func (s *shindexSuite) TestUnitsAreOnePerScriptOutsideNestedProjects() {
	units, err := s.indexer().Units(s.T().Context())
	s.Require().NoError(err)
	deps := map[string][]string{}
	for _, u := range units {
		deps[u.ID] = u.Deps
	}
	s.Equal(map[string][]string{
		"broken.sh":          nil,
		"lib.sh":             nil,
		"main.sh":            {"lib.sh"},
		"tasks/task-lib.sh":  nil,
		"tasks/x/check.bash": {"tasks/task-lib.sh"},
	}, deps)
}

func (s *shindexSuite) TestKeysFollowTheScipDescriptorGrammar() {
	s.Equal("magus shell . . `lib.sh`/greet().", funcSymbol("lib.sh", "greet"))
	s.Equal("magus shell . . `lib.sh`/LIMIT.", varSymbol("lib.sh", "LIMIT"))
	s.Equal("magus shell . . `lib.sh`/", symbolName("lib.sh"))
}

func (s *shindexSuite) TestFunctionDefinitionCarriesItsBodyAndDoc() {
	sym := funcSymbol("lib.sh", "greet")
	s.Equal([]symbols.Occurrence{{Symbol: sym, Range: []int32{1, 0, 5}, Roles: int32(scip.SymbolRole_Definition), Enclosing: []int32{1, 0, 4, 1}}},
		s.occurrences("lib.sh", sym))
	info := s.info("lib.sh", sym)
	s.Equal([]string{"greet prints a greeting."}, info.Documentation)
	s.Equal("greet()", info.Signature)
	s.Equal(int32(scip.SymbolInformation_Function), info.Kind)
}

func (s *shindexSuite) TestCallResolvesIntoTheSourcedScript() {
	sym := funcSymbol("lib.sh", "greet")
	s.Equal([]symbols.Occurrence{{Symbol: sym, Range: []int32{5, 2, 7}}}, s.occurrences("main.sh", sym))
}

func (s *shindexSuite) TestSourceRecordsAnImportOfTheScript() {
	sym := symbolName("lib.sh")
	s.Equal([]symbols.Occurrence{{Symbol: sym, Range: []int32{1, 2, 26}, Roles: int32(scip.SymbolRole_Import)}}, s.occurrences("main.sh", sym))
}

func (s *shindexSuite) TestArithmeticNameResolvesToTheSourcedGlobal() {
	sym := varSymbol("lib.sh", "LIMIT")
	s.Equal([]symbols.Occurrence{{Symbol: sym, Range: []int32{4, 19, 24}}}, s.occurrences("main.sh", sym))
}

func (s *shindexSuite) TestReadonlyGlobalIsAConstant() {
	info := s.info("lib.sh", varSymbol("lib.sh", "LIMIT"))
	s.Equal(int32(scip.SymbolInformation_Constant), info.Kind)
	s.Equal("readonly LIMIT", info.Signature)
}

func (s *shindexSuite) TestGlobalIsDefinedAtItsFirstAssignment() {
	sym := varSymbol("main.sh", "COUNT")
	s.Equal([]symbols.Occurrence{
		{Symbol: sym, Range: []int32{2, 0, 5}, Roles: int32(scip.SymbolRole_Definition)},
		{Symbol: sym, Range: []int32{4, 2, 7}, Roles: int32(scip.SymbolRole_WriteAccess)},
		{Symbol: sym, Range: []int32{4, 11, 16}},
		{Symbol: sym, Range: []int32{5, 10, 15}},
	}, s.occurrences("main.sh", sym))
}

func (s *shindexSuite) TestLocalStaysInItsFunction() {
	s.Equal([]symbols.Occurrence{
		{Symbol: "local 0", Range: []int32{2, 8, 11}, Roles: int32(scip.SymbolRole_Definition)},
		{Symbol: "local 0", Range: []int32{3, 15, 18}},
	}, s.occurrences("lib.sh", "local 0"))
	s.Empty(s.occurrences("lib.sh", varSymbol("lib.sh", "who")))
}

func (s *shindexSuite) TestComputedDirectoryAboveTheScriptResolves() {
	sym := funcSymbol("tasks/task-lib.sh", "task_fn")
	s.Equal([]symbols.Occurrence{{Symbol: sym, Range: []int32{1, 0, 7}}}, s.occurrences("tasks/x/check.bash", sym))
}

func (s *shindexSuite) TestParseFailureIsAGapNamingTheFile() {
	shard := s.shards()["broken.sh"]
	s.Empty(shard.Documents)
	s.Require().Len(shard.Gaps, 1)
	s.True(strings.HasPrefix(shard.Gaps[0], "broken.sh:"), shard.Gaps[0])
}

func (s *shindexSuite) updated(edit func()) symbols.UpdateResult {
	pack := filepath.Join(s.T().TempDir(), "shell.pack")
	_, err := symbols.UpdatePack(s.T().Context(), pack, s.indexer())
	s.Require().NoError(err)
	edit()
	res, err := symbols.UpdatePack(s.T().Context(), pack, s.indexer())
	s.Require().NoError(err)
	return res
}

func (s *shindexSuite) TestBodyEditReindexesOneScript() {
	res := s.updated(func() { s.write("lib.sh", strings.Replace(libSource, "hello", "hi", 1)) })
	s.Equal([]string{"lib.sh"}, res.Indexed)
	s.Empty(res.Dependents)
	s.Equal([]string{"broken.sh:1:9: reached EOF without matching `{` with `}`"}, res.Gaps)
}

func (s *shindexSuite) TestNameAddedToASourcedScriptReindexesItsSourcers() {
	res := s.updated(func() { s.write("lib.sh", libSource+"wave() { :; }\n") })
	s.Equal([]string{"lib.sh", "main.sh"}, res.Indexed)
}

const (
	benchScripts = 120
	benchFuncs   = 20
)

// writeBenchProject generates benchScripts scripts, each sourcing the one before it, so an
// edit low in the chain has every other script as a dependent.
func writeBenchProject(b *testing.B, dir string) {
	b.Helper()
	for f := range benchScripts {
		var src strings.Builder
		if f > 0 {
			fmt.Fprintf(&src, ". \"$(dirname \"$0\")/s%03d.sh\"\n\n", f-1)
		}
		fmt.Fprintf(&src, "TOTAL_%d=0\n\n", f)
		for fn := range benchFuncs {
			fmt.Fprintf(&src, "# f%d_%d is generated.\nf%d_%d() {\n  local x=\"$1\"\n  echo $((x + %d + TOTAL_%d))\n}\n\n", f, fn, f, fn, fn, f)
		}
		if f > 0 {
			fmt.Fprintf(&src, "f%d_0 1\n", f-1)
		}
		path := filepath.Join(dir, fmt.Sprintf("s%03d.sh", f))
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

// BenchmarkFullIndex is a cold index of the generated project, every script parsed.
func BenchmarkFullIndex(b *testing.B) {
	dir := b.TempDir()
	writeBenchProject(b, dir)
	pack := filepath.Join(b.TempDir(), "shell.pack")
	for b.Loop() {
		removePack(b, pack)
		if _, err := symbols.UpdatePack(b.Context(), pack, &Indexer{Dir: dir, WorkspaceRoot: dir}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOneLineEditAtTheRoot is a body edit in the script every other one sources
// through the chain: its names hold, so one unit.
func BenchmarkOneLineEditAtTheRoot(b *testing.B) {
	dir := b.TempDir()
	writeBenchProject(b, dir)
	pack := filepath.Join(b.TempDir(), "shell.pack")
	if _, err := symbols.UpdatePack(b.Context(), pack, &Indexer{Dir: dir, WorkspaceRoot: dir}); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(dir, "s000.sh")
	orig, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	versions := [][]byte{[]byte(strings.Replace(string(orig), "x + 3 +", "x + 4 +", 1)), orig}
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

// BenchmarkWorkspaceFullIndex is a cold index of this repository's root project's
// scripts, reporting how many units it indexed and how many gaps it left.
func BenchmarkWorkspaceFullIndex(b *testing.B) {
	root := workspaceRoot(b)
	pack := filepath.Join(b.TempDir(), "shell.pack")
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

// BenchmarkWorkspaceEditSourcedLibrary is a body edit to a library other scripts source.
func BenchmarkWorkspaceEditSourcedLibrary(b *testing.B) {
	root := workspaceRoot(b)
	pack := filepath.Join(b.TempDir(), "shell.pack")
	ix := &dirtyUnit{Indexer: &Indexer{Dir: root, WorkspaceRoot: root}, unit: "benchmarks/agent/arms/lib.sh"}
	if _, err := symbols.UpdatePack(b.Context(), pack, ix); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		res, err := symbols.UpdatePack(b.Context(), pack, ix)
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Indexed) != 1 {
			b.Fatalf("indexed %v, want %s alone", res.Indexed, ix.unit)
		}
	}
}
