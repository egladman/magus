module github.com/egladman/magus/libs/train

go 1.26.5

require (
	github.com/egladman/magus/libs/embed v0.1.0
	github.com/egladman/magus/libs/gopherbuzz v0.2.0
	github.com/egladman/magus/libs/xdg v0.1.0
	github.com/gomlx/compute v0.1.6
	github.com/gomlx/go-xla v0.4.5
	github.com/gomlx/gomlx v0.28.8
	github.com/stretchr/testify v1.11.1
	golang.org/x/text v0.42.0
	gonum.org/v1/gonum v0.17.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/ebitengine/purego v0.10.0 // indirect
	github.com/egladman/magus/libs/diagnostics v0.2.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
)

replace github.com/egladman/magus/libs/embed => ../embed

replace github.com/egladman/magus/libs/xdg => ../xdg

replace github.com/egladman/magus/libs/gopherbuzz => ../gopherbuzz

replace github.com/egladman/magus/libs/diagnostics => ../diagnostics
