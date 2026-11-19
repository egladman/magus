//go:build !unix

package job

import "os"

// openNonBlocking opens path to read. A path reaches no unix fifo on these hosts, so a
// plain open does not wait for a writer.
func openNonBlocking(path string) (*os.File, error) { return os.Open(path) }
