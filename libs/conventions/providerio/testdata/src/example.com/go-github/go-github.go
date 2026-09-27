// Package gogithub stands in for a real provider client SDK, so sdk.go can
// import a path shaped like one without a real network dependency.
package gogithub

// Client is unused; the import itself is what the test reports.
type Client struct{}
