package unpaired // want `concurrency_test.go has no source file of the same name; .* add it to the allow list`

import "testing"

func TestConcurrency(t *testing.T) { _ = Widget{} }
