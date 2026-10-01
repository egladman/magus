// Unpaired, and silent here: names are testpair's concern. Only the external
// package is reported.
package layout_test // want "declares .package layout_test.; put it in .package layout."

import "testing"

func TestConcurrency(t *testing.T) { _ = t }
