package staged // want `staged/clean.go writes nothing to os.Stderr now; delete its stderrprint allow entry`

import (
	"fmt"
	"os"
)

func results() {
	fmt.Fprintln(os.Stdout, "a result")
}
