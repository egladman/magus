package knowledge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// boundarySymbols is a command beside a library, an interface, a store writing through
// internal/file, and a package that exports a path but keeps no state.
func boundarySymbols() []types.KnowledgeSymbol {
	const (
		lib, ifc, file, store, plain, tool = "/internal/lib", "/internal/ifc", "/internal/file", "/internal/store", "/internal/plain", "/cmd/tool"
		libGo, ifcGo, fileGo, storeGo      = "internal/lib/lib.go", "internal/ifc/ifc.go", "internal/file/file.go", "internal/store/store.go"
		storeTest, plainGo, mainGo         = "internal/store/store_test.go", "internal/plain/plain.go", "cmd/tool/main.go"
	)
	fn := func(pkg, name, file, kind, sig string, line, tokens int, refs ...string) types.KnowledgeSymbol {
		s := conformanceSym(pkg, name, file, line, line+5, 40, "S"+name, tokens, refs...)
		s.Label, s.SymbolKind, s.Signature = strings.TrimRight(name, "().#"), kind, sig
		return s
	}
	delegate := fn(tool, "delegate().", mainGo, "Function", "func delegate() error", 20, 150)
	delegate.Calls = []types.KnowledgeSymbolCall{{Key: conformanceNS(lib) + "Do().", Count: 1}}
	return []types.KnowledgeSymbol{
		conformancePkg(lib, []string{libGo}, mainGo),
		conformancePkg(ifc, []string{ifcGo}, mainGo),
		conformancePkg(file, []string{fileGo}, storeGo),
		conformancePkg(store, []string{storeGo, storeTest}, mainGo, libGo),
		conformancePkg(plain, []string{plainGo}, mainGo),
		conformancePkg(tool, []string{mainGo}),

		fn(lib, "Do().", libGo, "Function", "func Do() error", 3, 40, mainGo),
		fn(lib, "Name().", libGo, "Function", "func Name() string", 10, 40, mainGo),
		fn(ifc, "Store#", ifcGo, "Interface", "type Store interface", 3, 20, mainGo),
		fn(file, "WriteFileAtomic().", fileGo, "Function", "func WriteFileAtomic(path string) error", 3, 40, storeGo),
		fn(store, "LogPath.", storeGo, "Constant", "const LogPath untyped string = \"log.jsonl\"", 3, 0, mainGo, libGo, storeTest),
		fn(store, "AnchorFile.", storeGo, "Constant", "const AnchorFile Kind = \"file\"", 5, 0, libGo),
		fn(store, "IsDir().", storeGo, "Function", "func IsDir(p string) bool", 7, 40, libGo),
		fn(store, "StoreDir().", storeGo, "Function", "func StoreDir() string", 10, 40, storeTest),
		fn(store, "Open().", storeGo, "Function", "func Open() error", 20, 40, mainGo),
		fn(plain, "QueueDir.", plainGo, "Constant", "const QueueDir untyped string = \"queue\"", 3, 0, mainGo),

		fn(tool, "main().", mainGo, "Function", "func main()", 3, 300),
		fn(tool, "compute().", mainGo, "Function", "func compute(n int) int", 10, 150),
		delegate,
		fn(tool, "parse().", mainGo, "Function", "func parse(args []string) error", 30, 150),
		fn(tool, "print().", mainGo, "Function", "func print(w io.Writer, rows []string)", 40, 150),
		fn(tool, "viaStore().", mainGo, "Function", "func viaStore(s ifc.Store, n int) error", 50, 150),
		fn(tool, "small().", mainGo, "Function", "func small(a, b string) string", 60, 50),
	}
}

func boundaryOptions() types.ConformanceOptions {
	return types.ConformanceOptions{MinSample: 1, Step: 0.05, Limits: map[types.ConformanceKey]types.ConformanceLimit{
		{Metric: types.ConformanceMainLogic, Population: types.ConformancePopMain}:         {Anchor: 119},
		{Metric: types.ConformanceStorePathExport, Population: types.ConformancePopSource}: {Anchor: 0},
	}}
}

func TestConformanceMainLogic(t *testing.T) {
	out := assembleLens(boundarySymbols()).Conformance(boundaryOptions())
	row := findConformanceRow(t, out, types.ConformanceMainLogic, types.ConformancePopMain, "")
	assert.Equal(t, 7, row.N, "every callable of the command is sampled")
	assert.Equal(t, []types.ConformanceItem{
		{ID: "cmd/tool/main.go#compute", Source: "cmd/tool/main.go:10", Measure: 150, Home: "internal/lib"},
	}, row.Violations, "main, a delegate, an args parser, a writer, an interface user and a small helper pass")
}

func TestConformanceStorePathExport(t *testing.T) {
	out := assembleLens(boundarySymbols()).Conformance(boundaryOptions())
	row := findConformanceRow(t, out, types.ConformanceStorePathExport, types.ConformancePopSource, "")
	assert.Equal(t, 2, row.N, "LogPath and StoreDir: Open, a typed constant and a predicate are no paths, and QueueDir's package keeps no state")
	assert.Equal(t, []types.ConformanceItem{
		{ID: "internal/store/store.go#LogPath", Source: "internal/store/store.go:3", Measure: 2, Home: "internal/store"},
	}, row.Violations, "the test file and the store's own files do not count")
}
