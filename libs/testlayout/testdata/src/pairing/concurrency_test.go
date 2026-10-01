// An external test package, whose pass holds no source files: the listing has to
// come off disk for the message to name the right thing.
package pairing_test // want `concurrency_test.go has no source file of the same name`

import "testing"

func TestConcurrency(t *testing.T) { _ = t }
