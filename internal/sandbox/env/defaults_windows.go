package env

// platformEnvAllow supplements commonEnvAllow with Windows-specific variables.
var platformEnvAllow = []string{
	"USERPROFILE",
	"APPDATA",
	"LOCALAPPDATA",
	"PATHEXT",
	"SYSTEMROOT",
	"COMSPEC",
	"TEMP",
	"TMP",
	"WINDIR",
	"PROCESSOR_ARCHITECTURE",
	"USERNAME",
	"COMPUTERNAME",
}
