package replay

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/suite"

	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

type ClassSuite struct {
	suite.Suite
}

func TestClass(t *testing.T) { suite.Run(t, new(ClassSuite)) }

// The registries are walked, not a list kept here. A module that reaches the seam
// unclassed would make a replay refuse it by accident.
func (s *ClassSuite) TestEveryModuleHasAReplayClass() {
	var modules []string
	for name := range bindgen.Modules {
		modules = append(modules, name)
	}
	for _, m := range buzzstd.Modules {
		modules = append(modules, m.Name)
	}
	modules = append(modules, "magus")

	var unclassed []string
	for _, name := range modules {
		if _, ok := NativeClass(name + ".any"); !ok {
			unclassed = append(unclassed, name)
		}
	}
	slices.Sort(unclassed)
	s.Empty(unclassed, "these modules have no replay class: add each to a table in class.go")
}

func (s *ClassSuite) TestAModuleHoldingProcessStateIsLive() {
	for _, name := range []string{"screen.open", "notify.send", "progress.start", "termkit.size", "layout.cols", "File.read", "Socket.send", "TcpServer.accept", "ffi.alloc", "fs.watch", "http.server"} {
		class, ok := NativeClass(name)
		s.True(ok, name)
		s.Equal(classLive, class, name)
	}
}

func (s *ClassSuite) TestAModuleOfDataIsAnAnswer() {
	for _, name := range []string{"fs.readFile", "vcs.status", "http.get", "magus.describe.file", "std.print", "os.time"} {
		class, ok := NativeClass(name)
		s.True(ok, name)
		s.Equal(classAnswer, class, name)
	}
}

func (s *ClassSuite) TestAnUnknownModuleHasNoClass() {
	_, ok := NativeClass("nowhere.method")
	s.False(ok)
}

func (s *ClassSuite) TestOnlyReadsAreRepeatable() {
	s.True(mayRepeat("fs.readFile"))
	s.True(mayRepeat("fs.exists"))
	s.True(mayRepeat("vcs.status"))
	s.True(mayRepeat("math.sqrt"), "a module of pure functions is repeatable whole")
	s.True(mayRepeat("os.time"))
	for _, name := range []string{"fs.writeFile", "fs.remove", "proc.exec", "http.get", "vcs.cmd", "std.print", "os.sleep", "magus.run", "nowhere.method"} {
		s.False(mayRepeat(name), name)
	}
}
