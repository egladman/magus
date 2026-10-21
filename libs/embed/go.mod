module github.com/egladman/magus/libs/embed

go 1.26.0

require (
	github.com/egladman/magus/libs/xdg v0.1.0
	github.com/klauspost/compress v1.19.1
	github.com/stretchr/testify v1.11.1
	golang.org/x/sync v0.23.0
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/egladman/magus/libs/xdg => ../xdg
