package cli

import "fmt"

func render(reason string) {
	fmt.Println("magus workspace: run it through magus") // want `guard rule text "magus workspace: run it through magus" in a file that owns .*; rules live in guard/`
	fmt.Println("verdict:", reason)
}
