// A cross-cutting concern that no single source file owns, in an external test
// package so that the pass holds no source files at all. Ordinary Go, and
// silent unless Unpaired is set.
package sprawl_test // want "declares .package sprawl_test.; put it in .package sprawl."

import "testing"

func TestConcurrency(t *testing.T) { _ = t }
