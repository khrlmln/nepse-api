// Package version carries build metadata injected at link time.
package version

// Version is the release version. Local builds default to "dev".
var Version = "dev"

// Commit is the source revision the binary was built from.
var Commit = "unknown"

// BuildMode identifies the intended runtime profile. Production binaries built by the Makefile are stamped "production"; local `go run` builds remain "development".
var BuildMode = "development"
