module github.com/egladman/magus/libs/conventions

go 1.26

require (
	github.com/egladman/magus/libs/proofread v0.0.0-00010101000000-000000000000
	github.com/golangci/plugin-module-register v0.1.2
	github.com/stretchr/testify v1.11.1
	golang.org/x/mod v0.37.0
	golang.org/x/tools v0.47.0
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/egladman/magus/libs/diagnostics v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	golang.org/x/sync v0.21.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/egladman/magus/libs/diagnostics => ../diagnostics

replace github.com/egladman/magus/libs/proofread => ../proofread
