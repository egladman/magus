module github.com/egladman/magus/libs/mergequeue

go 1.26

require (
	github.com/egladman/magus/libs/gopherbuzz v0.1.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/ebitengine/purego v0.10.0 // indirect
	github.com/egladman/magus/libs/diagnostics v0.1.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/egladman/magus/libs/diagnostics => ../diagnostics

replace github.com/egladman/magus/libs/gopherbuzz => ../gopherbuzz
