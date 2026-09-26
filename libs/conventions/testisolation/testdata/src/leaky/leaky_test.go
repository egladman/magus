package leaky // want `this test binary links sockdir but no TestMain in its directory calls testkit.Main or testkit.Isolated`

import "testing"

func TestRun(t *testing.T) { Run() }
