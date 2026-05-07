package buzz

import (
	"context"

	vmpackage "github.com/egladman/gopherbuzz/vm"
)

// MarshalOption configures what Chunk.Marshal emits.
type MarshalOption = vmpackage.MarshalOption

// DebugOnly makes Marshal emit the debug-info (.bdb) blob instead of the
// executable bytecode (.bo). See vm.DebugOnly for full documentation.
var DebugOnly = vmpackage.DebugOnly

// UnmarshalChunk deserializes a Chunk produced by Chunk.Marshal.
var UnmarshalChunk = vmpackage.UnmarshalChunk

// ExecBytecode deserializes a Chunk from data and executes it in this session.
func (s *Session) ExecBytecode(ctx context.Context, data []byte) error {
	c, err := vmpackage.UnmarshalChunk(data)
	if err != nil {
		return err
	}
	return s.ExecChunk(ctx, c)
}
