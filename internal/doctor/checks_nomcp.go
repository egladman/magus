//go:build !mcp

package doctor

// probeBridgeReachability is a no-op on non-mcp builds: the bridge is not
// compiled in so there are no /api/v1/ routes to probe.
func probeBridgeReachability(_ *DaemonInfo) Check {
	return Check{
		Name:    "web bridge",
		Status:  StatusOK,
		Message: "bridge not compiled in (build without -tags mcp); check skipped",
	}
}
