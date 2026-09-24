//go:build !linux && !darwin

package pipepeer

import "os"

// ReadEnd always fails with ErrUnsupported here. On windows, proving the writer of an
// anonymous pipe needs the undocumented NtQuerySystemInformation handle table.
func ReadEnd(_, _ int) (Pipe, error) { return Pipe{}, ErrUnsupported }

// Writers always fails with ErrUnsupported here.
func (Pipe) Writers() ([]int, error) { return nil, ErrUnsupported }

// WrittenBy is always false here.
func (Pipe) WrittenBy(_ int) bool { return false }

// Args always fails with ErrUnsupported here.
func Args(_ int) ([]string, error) { return nil, ErrUnsupported }

// Parent always fails with ErrUnsupported here.
func Parent(_ int) (int, error) { return 0, ErrUnsupported }

func executable(_ int) (os.FileInfo, error) { return nil, ErrUnsupported }
