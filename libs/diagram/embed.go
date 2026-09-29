package diagram

import "embed"

// Source holds the renderer and the flow layout as Buzz source, keyed by file name, for a
// host that evaluates them without the workspace on disk.
//
//go:embed diagram.buzz flow.buzz
var Source embed.FS
