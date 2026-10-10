module github.com/egladman/magus/libs/conventions

go 1.26

require (
	github.com/egladman/magus/libs/diagnostics v0.2.0
	github.com/golangci/plugin-module-register v0.1.2
	golang.org/x/mod v0.37.0
	golang.org/x/tools v0.47.0
)

require golang.org/x/sync v0.21.0 // indirect

replace github.com/egladman/magus/libs/diagnostics => ../diagnostics
