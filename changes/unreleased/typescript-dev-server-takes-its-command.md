### Changed

- **Breaking: the typescript spell's `dev-server` op takes the server command from
  the caller.** It no longer runs the `dev` script in `package.json`: pass the command
  the way `esbuild` takes its args, `typescript["dev-server"](ctx, {"args": ["vite"]})`
  runs `pnpm exec vite`. A call with no command fails with that call spelled out.
