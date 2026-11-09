package spell

import _ "embed"

// TargetRunSource / RunSource are the generated Buzz mirrors of one run and the
// targets in it (daemon.TargetRun and daemon.Run), the shape
// `magus server status` reports. A caller can ITERATE a run with them. Each
// target carries its state, duration and output ref, so nobody parses
// console output.
//
// TargetRun precedes Run, because Run.targets is a list of it and a struct-valued
// field mirrors as its bare type name, which must already be declared.
//
//go:generate go run ../../cmd/magus-utils types -type TargetRun -out gen/types/targetrun.buzz
//go:embed gen/types/targetrun.buzz
var TargetRunSource string

//go:generate go run ../../cmd/magus-utils types -type Run -out gen/types/run.buzz
//go:embed gen/types/run.buzz
var RunSource string
