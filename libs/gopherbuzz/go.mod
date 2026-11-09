module github.com/egladman/magus/libs/gopherbuzz

go 1.26

require (
	github.com/dlclark/regexp2 v1.12.0
	github.com/ebitengine/purego v0.10.0
	golang.org/x/crypto v0.53.0
	golang.org/x/tools v0.49.0
)

require golang.org/x/sys v0.47.0

require github.com/twitchyliquid64/golang-asm v0.15.1

require (
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/egladman/magus/libs/diagnostics v0.2.0
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/egladman/magus/libs/diagnostics => ../diagnostics
