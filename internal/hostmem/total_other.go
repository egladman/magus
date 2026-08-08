//go:build !linux && !darwin

package hostmem

import "context"

// TotalBytes reports 0: no portable way to ask on the remaining hosts. The
// planner treats 0 as no budget and plans as it did before one existed. A guess
// would be worse: too high protects nothing, too low buys runners nobody needed.
func TotalBytes(_ context.Context) int64 { return 0 }
