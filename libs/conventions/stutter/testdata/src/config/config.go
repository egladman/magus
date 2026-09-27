package settings

// Named for the directory, not the package clause, so it does not stutter.
type ConfigFile struct{}

type SettingsFile struct{} // want `settings.SettingsFile stutters`
