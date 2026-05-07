//go:build cgo && !luajit_static

package luajit

// Dynamic link against system libluajit-5.1 via pkg-config.

// #cgo pkg-config: luajit
import "C"
