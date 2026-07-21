package buzz

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/diag"
)

// TestAllBZZCodesEnumerated guards allBZZCodes against the const block in diagnostics.go: every declared
// BZZ code must be enumerated, and the counts must match, so a new code cannot silently escape the
// doc-coverage check below.
func TestAllBZZCodesEnumerated(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	src, err := os.ReadFile(filepath.Join(dir, "diagnostics.go"))
	if err != nil {
		t.Fatal(err)
	}
	declared := regexp.MustCompile(`diag\.Code = "(BZZ\d+)"`).FindAllStringSubmatch(string(src), -1)
	if len(declared) == 0 {
		t.Fatal("no BZZ codes found in diagnostics.go")
	}
	enum := map[diag.Code]bool{}
	for _, c := range allBZZCodes {
		if enum[c] {
			t.Errorf("duplicate code %s in allBZZCodes", c)
		}
		enum[c] = true
	}
	for _, m := range declared {
		if !enum[diag.Code(m[1])] {
			t.Errorf("%s is declared but missing from allBZZCodes", m[1])
		}
	}
	if len(allBZZCodes) != len(declared) {
		t.Errorf("allBZZCodes has %d entries, the const block declares %d", len(allBZZCodes), len(declared))
	}
}

// TestEveryBZZCodeHasDocPage keeps a new code from shipping without its lookup page, at exactly the path
// its docs URL resolves to (docs/codes/<code>.md inside gopherbuzz's own tree).
func TestEveryBZZCodeHasDocPage(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for _, c := range allBZZCodes {
		path := filepath.Join(dir, "docs", "codes", string(c)+".md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: no doc page at %s", c, path)
		}
	}
}

// TestTypeErrorRendersCode pins the inline rendering: a coded type error shows [BZZ####], the position,
// the message, and a see: link.
func TestTypeErrorRendersCode(t *testing.T) {
	e := typeError{Line: 4, Col: 3, Code: UndefinedName, Msg: "undefined: foo"}
	got := e.Error()
	for _, want := range []string{"[BZZ1001]", "buzz: line 4:3", "undefined: foo", "see: "} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, want it to contain %q", got, want)
		}
	}
}
