package env

// platformEnvAllow supplements commonEnvAllow with macOS-specific variables.
var platformEnvAllow = []string{
	"LOGNAME",
	"SHELL",
	"COLORTERM",
	"PWD",
	"TMPDIR",
	"__CF_USER_TEXT_ENCODING",
	"XPC_FLAGS",
	"XPC_SERVICE_NAME",
	"Apple_PubSub_Socket_Render",
}
