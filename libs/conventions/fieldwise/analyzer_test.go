package fieldwise

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer runs the zero Options over complete, where only a value asserted
// on every field it declares is reported, and each value changed between its
// assertions is not.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "complete")
}

// TestReportPartial runs over results, whose test files pin each shape
// reported and each one left alone; external_test.go is the same types seen
// from outside the package. Result has more fields than any test asserts.
func TestReportPartial(t *testing.T) {
	analyzer, err := New(Options{ReportPartial: true})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "results")
}

// TestReportPartialAssertions runs over flagged and clean. The analyzer reads
// test files only, so each is copied in as a _test.go file, beside testify
// stubs that declare the partial assertions.
func TestReportPartialAssertions(t *testing.T) {
	analyzer, err := New(Options{ReportPartialAssertions: true})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"github.com/stretchr/testify/assert/assert.go":   assertStub,
		"github.com/stretchr/testify/require/require.go": requireStub,
	}
	for _, pkg := range []string{"flagged", "clean"} {
		b, err := os.ReadFile(filepath.Join(analysistest.TestData(), "src", pkg, fmt.Sprintf("%s.go", pkg)))
		if err != nil {
			t.Fatal(err)
		}
		files[fmt.Sprintf("%s/%s.go", pkg, pkg)] = fmt.Sprintf("package %s\n", pkg)
		files[fmt.Sprintf("%s/%s_test.go", pkg, pkg)] = string(b)
	}
	gopath := t.TempDir()
	for name, body := range files {
		path := filepath.Join(gopath, "src", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	analysistest.Run(t, gopath, analyzer, "flagged", "clean")
}

const assertStub = `package assert

type TestingT interface {
	Errorf(format string, args ...any)
}

func Equal(t TestingT, expected, actual any, msgAndArgs ...any) bool      { return true }
func Contains(t TestingT, s, contains any, msgAndArgs ...any) bool        { return true }
func Containsf(t TestingT, s, contains any, msg string, args ...any) bool { return true }
func True(t TestingT, value bool, msgAndArgs ...any) bool                 { return true }
func Regexp(t TestingT, rx, str any, msgAndArgs ...any) bool              { return true }
func NotRegexp(t TestingT, rx, str any, msgAndArgs ...any) bool           { return true }
func Len(t TestingT, object any, length int, msgAndArgs ...any) bool      { return true }
func Subset(t TestingT, list, subset any, msgAndArgs ...any) bool         { return true }
func NotSubset(t TestingT, list, subset any, msgAndArgs ...any) bool      { return true }

type Assertions struct{ t TestingT }

func New(t TestingT) *Assertions { return &Assertions{t} }

func (a *Assertions) Contains(s, contains any, msgAndArgs ...any) bool { return true }
`

const requireStub = `package require

type TestingT interface {
	Errorf(format string, args ...any)
	FailNow()
}

func Equal(t TestingT, expected, actual any, msgAndArgs ...any)               {}
func NoError(t TestingT, err error, msgAndArgs ...any)                        {}
func ErrorContains(t TestingT, err error, contains string, msgAndArgs ...any) {}
func EqualError(t TestingT, err error, errString string, msgAndArgs ...any)   {}
func NotContains(t TestingT, s, contains any, msgAndArgs ...any)              {}
func False(t TestingT, value bool, msgAndArgs ...any)                         {}
func Len(t TestingT, object any, length int, msgAndArgs ...any)               {}
`
