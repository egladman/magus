package buzz

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
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
	declared := regexp.MustCompile(`diagnostics\.Code = "(BZZ\d+)"`).FindAllStringSubmatch(string(src), -1)
	if len(declared) == 0 {
		t.Fatal("no BZZ codes found in diagnostics.go")
	}
	enum := map[diagnostics.Code]bool{}
	for _, c := range allBZZCodes {
		if enum[c] {
			t.Errorf("duplicate code %s in allBZZCodes", c)
		}
		enum[c] = true
	}
	for _, m := range declared {
		if !enum[diagnostics.Code(m[1])] {
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

// TestTypeErrorNoCodeRendersPlain pins that an unclassified error (empty Code) renders as a plain message
// with no [BZZ] tag and no see: link - matching Rust/TS, where an error either earns a code or has none.
func TestTypeErrorNoCodeRendersPlain(t *testing.T) {
	got := typeError{Line: 2, Col: 1, Msg: "void function cannot return a value"}.Error()
	want := "buzz: line 2:1: void function cannot return a value"
	if got != want {
		t.Errorf("Error() = %q, want exactly %q (no code, no see: link)", got, want)
	}
}

// TestTypeErrorAsDiagnostic pins the errors.As bridge an embedder branches on. magus sits
// above this module and its lowest layers below it, so nothing outside this package can name
// typeError - without As the BZZ code is reachable only by substring-matching a sentence
// written for humans, and the one caller that tried instead matched nothing at all.
func TestTypeErrorAsDiagnostic(t *testing.T) {
	// Wrapped the way a workspace load wraps it, so this exercises the chain rather than a
	// top-level assertion that would pass for the wrong reason.
	err := fmt.Errorf("magusfile: exec magusfile.buzz: %w",
		typeError{Line: 4, Col: 3, Code: UndefinedType, Msg: `undefined type "Secret"`})

	var d *diagnostics.Error
	if !errors.As(err, &d) {
		t.Fatal("errors.As found no *diagnostics.Error in a coded type error")
	}
	if d.Code != UndefinedType {
		t.Errorf("code = %q, want %q", d.Code, UndefinedType)
	}
	if !strings.Contains(d.Error(), `undefined type "Secret"`) {
		t.Errorf("message did not survive: %q", d.Error())
	}
}

// TestTypeErrorWithNoCodeIsNotADiagnostic keeps the bridge honest: an unclassified error has
// no diagnostic to hand back, and answering with an empty code would make every plain type
// error look like a coded one to a caller switching on the code.
func TestTypeErrorWithNoCodeIsNotADiagnostic(t *testing.T) {
	var d *diagnostics.Error
	if errors.As(typeError{Line: 2, Col: 1, Msg: "void function cannot return a value"}, &d) {
		t.Errorf("an uncoded type error reported itself as %v", d)
	}
}
