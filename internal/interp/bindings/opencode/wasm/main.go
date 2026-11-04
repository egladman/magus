//go:build js && wasm

package main

import harness "github.com/egladman/magus/internal/interp/bindings/opencode"

func main() { harness.Serve() }
