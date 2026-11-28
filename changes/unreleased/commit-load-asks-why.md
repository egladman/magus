### Fixed

- **A commit is refused for an unloadable magusfile only when the tree is at fault.** A stale
  or missing ./magus no longer refuses every commit and `rebase --continue` with a repair line
  that needs the load it lacks. The rule stands down, and the judging-binary note gives the
  relink.
- **The relink's stamp step no longer runs go-build.** The relinked ./magus links itself again
  with the build stamp through `magus buzz -e`, so no OpenCode runtime wasm is regenerated. A
  stamp it cannot take fails the line. A committed wasm stale against its sources is caught by
  the `generate` target's drift gate, which `ci` runs on the pull request.
- **The OpenCode runtime's generate target reads its package closure, not every Go file.** A Go
  edit outside the wasm's nine packages no longer misses its cache. `TestDeclaredReadsCoverTheClosure`
  pins the declared packages to `GOOS=js GOARCH=wasm go list -deps` both ways. It does not catch
  a new embedded file or a new module file.
