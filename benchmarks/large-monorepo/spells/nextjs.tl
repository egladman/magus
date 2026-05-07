-- Next.js spell for the large-monorepo benchmark.
--
-- Loaded by the generated gen/repo/magusfile.tl via magus.spell.load. It builds
-- a Next.js app by running its package.json "build" script (next build) and
-- caches the produced .next/ directory. The build target is direct, so the CLI
-- forks `npm run build` directly — no compiled Lua, no spells-generate.
--
-- Cache key: the app's own TypeScript/JS sources (below) plus the cache keys of
-- every project the app declares in depends_on (wired in the magusfile). magus
-- excludes declared outputs (.next/**) from the hashed inputs, so a warm run is
-- a true cache hit; editing an app page busts that app (S6) and editing a
-- depended lib busts the app via transitive propagation (S7).
local M: MagusSpell = {
    name = "nextjs",
    markers = {"next.config.js", "next.config.mjs", "next.config.ts"},
    claims = {},
    opaque = false,
    version_cmd = {"node", "--version"},

    sources = function(_dir: string): {string}
        return {
            "**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx", "**/*.json",
            "package.json",
            "next.config.js", "next.config.mjs", "next.config.ts",
            "package-lock.json",
        }
    end,

    outputs = function(): {string}
        return {".next/**"}
    end,

    -- No manifest-inferred dependencies; the app->lib edges are declared via
    -- depends_on in the magusfile.
    dependencies = function(_dir: string): {string}
        return {}
    end,

    targets = {
        build = { direct = true, cmd = "npm", args = {"run", "build"} },
    },
}

return M
