// See types/mocks_satisfy_test.go for why these assertions exist and why they live
// outside the gen/ dir that holds the mocks.
//
// package magus_test, not package magus: gen/mocks imports magus, so asserting from
// inside the package itself would be an import cycle.
package magus_test

import (
	"github.com/egladman/magus"
	mocks "github.com/egladman/magus/gen/mocks"
)

var _ magus.Daemon = (*mocks.MockDaemon)(nil)
