//go:build mcp

package dashboard

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// The domain carries a status time as time.Time; the wire uses the well-known
// google.protobuf.Timestamp per AIP-142. This maps between them, treating a zero value as
// "unset" -> nil, so an absent time (an epoch-zero record) stays absent on the wire.

func tsFromTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
