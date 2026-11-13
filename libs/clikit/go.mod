module github.com/egladman/magus/libs/clikit

go 1.26

require (
	github.com/egladman/magus/libs/apiversion v0.1.0
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
)

replace github.com/egladman/magus/libs/apiversion => ../apiversion
