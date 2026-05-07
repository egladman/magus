// Package bindings registers the Go-backed modules (magus, std: os, platform,
// fs, vcs, env, crypto, json, log, http, archive) available to every magusfile script.
// Blank-import this package so its init() fires before any magusfile runs.
package bindings

import (
	"context"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/engine"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
	luagen "github.com/egladman/magus/internal/std/gen/lua"
)

func init() {
	interp.RegisterHostBindings(registerAll)
}

func registerAll(r lua.Session, parseMode bool) error {
	r.SetGlobal("_magus_targets", r.NewTable())

	registerMagus(r, parseMode)
	registerStd(r)

	if err := registerSpells(r); err != nil {
		return err
	}
	registerLocalSpellSearcher(r)
	return nil
}

// registerStd makes each std module require-able as "magus.extra.<name>" via
// package.preload, mirroring how built-in spells resolve (see registerSpells).
// A magusfile binds what it uses with `local os = require("magus.extra.os")` —
// there is no magus.extra aggregate and no implicit global. Must run after
// registerMagus so the package table is in place.
func registerStd(r lua.Session) {
	pkg, ok := r.GetGlobal("package").AsTable()
	if !ok {
		return
	}
	preload, ok := pkg.RawGetString("preload").AsTable()
	if !ok {
		return
	}
	modules := map[string]engine.Table{
		"os":       luagen.RegisterOs(r),
		"platform": luagen.RegisterPlatform(r),
		"fs":       luagen.RegisterFs(r),
		"vcs":      luagen.RegisterVcs(r),
		"archive":  luagen.RegisterArchive(r),
		"crypto":   luagen.RegisterCrypto(r),
		"env":      luagen.RegisterEnv(r),
		"json":     luagen.RegisterJson(r),
		"http":     luagen.RegisterHttp(r),
		"time":     luagen.RegisterTime(r),
		"fmt":      luagen.RegisterFmt(r),
		"markdown": luagen.RegisterMarkdown(r),
		"charm":    luagen.RegisterCharm(r),
	}
	for name, mod := range modules {
		captured := mod
		preload.RawSetString("magus.extra."+name, r.NewFunction(func(_ context.Context, r lua.Session) int {
			r.Push(captured)
			return 1
		}))
	}
}
