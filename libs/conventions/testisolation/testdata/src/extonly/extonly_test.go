package extonly_test // want `this test binary links sockdir`

import (
	"testing"

	"broker"
)

func TestDial(t *testing.T) { broker.Dial() }
