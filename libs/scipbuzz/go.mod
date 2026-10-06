module github.com/egladman/magus/libs/scipbuzz

go 1.26

require (
	github.com/egladman/magus/libs/gopherbuzz v0.2.0
	github.com/scip-code/scip/bindings/go/scip v0.9.0
	github.com/stretchr/testify v1.11.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/ebitengine/purego v0.10.0 // indirect
	github.com/egladman/magus/libs/diagnostics v0.2.0 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/hexops/gotextdiff v1.0.3 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sourcegraph/beaut v0.0.0-20240611013027-627e4c25335a // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/sys v0.46.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/egladman/magus/libs/gopherbuzz => ../gopherbuzz

replace github.com/egladman/magus/libs/diagnostics => ../diagnostics
