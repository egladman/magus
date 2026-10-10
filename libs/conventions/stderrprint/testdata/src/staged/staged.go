package staged

import (
	"fmt"
	"os"
)

func notices() {
	fmt.Fprintln(os.Stderr, "magus: wrote 3 files")
}
