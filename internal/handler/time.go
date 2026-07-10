package handler

import (
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The domain carries time as int64 unix-millis (journal) or time.Time (status); the
// wire uses the well-known google.protobuf.Timestamp / Duration per AIP-142. These map
// between them, treating a zero value as "unset" -> nil, so an absent time (a still-
// running invocation's end, an epoch-zero record) stays absent on the wire.

func tsFromMs(ms int64) *timestamppb.Timestamp {
	if ms == 0 {
		return nil
	}
	return timestamppb.New(time.UnixMilli(ms))
}

func tsFromTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func durFromMs(ms int64) *durationpb.Duration {
	if ms == 0 {
		return nil
	}
	return durationpb.New(time.Duration(ms) * time.Millisecond)
}
