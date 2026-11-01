// Package version is set with -ldflags at release time.
package version

// Version is the companion build. Release builds replace it.
var Version = "0.1.0-dev"

// DefaultAPIBaseURL is baked into official release binaries with -ldflags
// from the GitHub Actions variable SAGALORE_API_URL.
// Empty means the companion requires --api-url or SAGALORE_API_URL.
var DefaultAPIBaseURL = ""
