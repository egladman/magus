package figure

import _ "embed"

// Source is the figure module's Buzz source, served as `import "magus/figure"` by a host
// with no checkout on disk.
//
//go:embed figure.buzz
var Source string
