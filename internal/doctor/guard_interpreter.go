package doctor

import (
	"bytes"
	"context"
	"os"

	"github.com/egladman/magus/internal/agent"
)

// ownBinaryToken is how a committed hook command names the workspace's own binary.
const ownBinaryToken = "./magus"

// configsNamingOwnBinary lists the hook configs whose command runs ./magus.
//
// A hook command names its interpreter literally, chosen when the harness was applied, and
// nothing re-checks it afterwards. A checkout that loses ./magus has every such hook fail
// before a line of glue runs, which is the one breakage no notice arm inside the glue can
// report, and resolving ./magus-then-PATH the way the scripts do grades it healthy.
//
// Reads the descriptors rather than the verified inventory: verification runs the wired
// command, so it goes quiet in precisely the state this looks for.
func configsNamingOwnBinary(ctx context.Context, root string, wired ...string) []string {
	var out []string
	for _, cfg := range agent.HarnessConfigPaths(ctx, root, wired...) {
		body, err := os.ReadFile(cfg)
		if err != nil {
			continue
		}
		if bytes.Contains(body, []byte(ownBinaryToken)) {
			out = append(out, cfg)
		}
	}
	return out
}
