module github.com/egladman/magus/libs/safetyscissors

go 1.26.0

require (
	github.com/bmatcuk/doublestar/v4 v4.10.0
	github.com/egladman/magus/libs/apiversion v0.1.0
	github.com/egladman/magus/libs/codec v0.1.0
	github.com/stretchr/testify v1.11.1
	golang.org/x/sync v0.23.0
	golang.org/x/sys v0.49.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/egladman/magus/libs/apiversion => ../apiversion

replace github.com/egladman/magus/libs/codec => ../codec
