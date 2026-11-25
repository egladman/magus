//go:build !linux && !darwin

package job

import "os"

// openNonBlocking opens path to read. Only linux and darwin are built for a host that
// reaches a fifo here, so a plain open does not wait for a writer.
func openNonBlocking(path string) (*os.File, error) { return os.Open(path) }
