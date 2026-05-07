package env

// platformEnvAllow supplements commonEnvAllow with Linux-specific variables.
var platformEnvAllow = []string{
	"LOGNAME",
	"SHELL",
	"COLORTERM",
	"PWD",
	"TMPDIR",
	"XDG_RUNTIME_DIR",
	"XDG_DATA_HOME",
	"XDG_CONFIG_HOME",
	"XDG_CACHE_HOME",
}
