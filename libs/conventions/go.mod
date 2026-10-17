module github.com/egladman/magus/libs/conventions

go 1.26

require (
	github.com/egladman/magus/libs/proofread v0.0.0-00010101000000-000000000000
	github.com/golangci/plugin-module-register v0.1.2
	golang.org/x/mod v0.37.0
	golang.org/x/tools v0.47.0
)

require (
	github.com/egladman/magus/libs/diagnostics v0.2.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
)

replace github.com/egladman/magus/libs/diagnostics => ../diagnostics

replace github.com/egladman/magus/libs/proofread => ../proofread
