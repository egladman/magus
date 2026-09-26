package cli

import "fmt"

func render(reason string) {
	fmt.Println("magus workspace: run it through magus") // want `guard rule text "magus workspace: run it through magus" outside internal/guard`
	fmt.Println("verdict:", reason)
}
