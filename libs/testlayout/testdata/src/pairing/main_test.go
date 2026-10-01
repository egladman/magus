package pairing // want `main_test.go has no source file of the same name`

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(m.Run()) }
