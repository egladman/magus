// Package sdk imports a path shaped like a provider client SDK, outside every
// governed dir: the check still fires, since an accidental provider dependency
// is worth catching wherever it lands.
package sdk

import _ "example.com/go-github" // want `imports "example.com/go-github", shaped like a CI/VCS provider client library.*; provider I/O lives in scripts$`
