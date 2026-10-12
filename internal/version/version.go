// Package version holds the build-time version information, injected with
// -ldflags by the release pipeline.
package version

import "fmt"

// Version is the release version ("dev" for local builds).
var Version = "dev"

// Commit is the git commit the binary was built from.
var Commit = "none"

// Date is the build date.
var Date = "unknown"

// String returns "ccswitch <version> (<commit>, <date>)".
func String() string {
	return fmt.Sprintf("ccswitch %s (%s, %s)", Version, Commit, Date)
}
