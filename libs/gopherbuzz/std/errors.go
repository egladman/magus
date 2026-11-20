package std

import _ "embed"

// errorsSource is upstream's src/lib/errors.buzz at UpstreamRef, byte
// for byte. It is declarations only, so it binds with no native half.
//
//go:embed errors.buzz
var errorsSource string
