// Package profiles holds the profiles of the servers in the compatibility
// matrix, so proof can use them by name wherever it's installed.
package profiles

import "embed"

// FS has the profiles, e.g. babashka.toml.
//
//go:embed *.toml
var FS embed.FS
