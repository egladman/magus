package main

import (
	_ "github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings"
	_ "github.com/egladman/magus/internal/interp/engine/buzz"
	_ "github.com/egladman/magus/internal/interp/engine/lua/gopherlua"
	_ "github.com/egladman/magus/internal/interp/engine/lua/luajit"
)
