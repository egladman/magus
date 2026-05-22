// Package bindings registers the Go-backed modules (magus, std: os, platform,
// fs, vcs, env, crypto, json, log, http, archive) available to every magusfile script.
// Blank-import this package so its init() fires before any magusfile runs.
package bindings
