module github.com/egladman/magus/libs/proofread

go 1.26

require (
	github.com/egladman/magus/libs/clikit v0.1.0
	github.com/egladman/magus/libs/diagnostics v0.2.0
	golang.org/x/tools v0.47.0
)

require github.com/egladman/magus/libs/apiversion v0.1.0 // indirect

replace github.com/egladman/magus/libs/diagnostics => ../diagnostics

replace github.com/egladman/magus/libs/clikit => ../clikit

replace github.com/egladman/magus/libs/apiversion => ../apiversion
