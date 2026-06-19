// Package magus is the high-level library for the magus build orchestrator.
//
// Entry points: [Open] returns a [Magus] for build/test cycles, [Inspect] for
// read-only commands. A [Magus] runs work via [Magus.Run] (one target),
// [Magus.RunCI] (the configured CI pipeline), and [Magus.RunAffected]
// (only projects touched since a baseline). Behavior is tuned with [Option]
// values passed to [Open]/[Inspect] (e.g. [WithLimiter]). [Limiter] caps
// concurrent spell executions and can be shared across daemon workspaces.
//
// Boundary: the library links the engine-agnostic interp surface and the Buzz VM,
// but deliberately not the host bindings (interp/bindings) or the Buzz engine
// backend — cmd/magus blank-imports those. So a script-driven backend (e.g. the
// spell-backed remote backend) reaches the library only through registered
// hooks such as [cache.RegisterRemoteBackendOpener], never a direct import.
package magus
