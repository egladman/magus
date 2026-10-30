package hint

import (
	"fmt"
	"path"
	"strings"
)

// Magusfile lines a hint tells the reader to paste. Each carries its own
// `import "magus";`, so it loads whether or not the file already imports magus
// (a repeated import is harmless). internal/dry type-checks every one.

// TargetExample declares the smallest target a magusfile can export.
const TargetExample = `import "magus"; export fun build(ctx: magus\Context, args: [str]) > void {}`

// CITargetExample declares a ci target that composes the build, test and lint
// targets the magusfile already exports.
const CITargetExample = `import "magus"; export fun ci(ctx: magus\Context, args: [str]) > void { ctx.needs(build, test, lint); }`

// BindSpellExample imports the spell package at dir, relative to the magusfile,
// and binds it into the project. The handle is name with each '-' spelled '_',
// since a spell name may carry a dash and a Buzz identifier may not.
func BindSpellExample(dir, name string) string {
	if !path.IsAbs(dir) && !strings.HasPrefix(dir, "../") {
		// A bare "spells/x" is magus's built-in spell namespace, not this directory.
		dir = "./" + strings.TrimPrefix(dir, "./")
	}
	handle := strings.ReplaceAll(name, "-", "_")
	return fmt.Sprintf(`import "magus"; import %q as %s; magus\project({ "spells": [%s] });`, dir, handle, handle)
}
