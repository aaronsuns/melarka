// Package buildinfo carries the release version, set at build time with
// -ldflags "-X github.com/aaronsuns/lark-server/internal/buildinfo.Version=<v>".
package buildinfo

// Version is the release version; "dev" for local and test builds.
var Version = "dev"

// UserAgent identifies Melarka to the services it calls (LRCLIB asks clients to).
func UserAgent() string {
	return "Melarka/" + Version + " (+https://github.com/aaronsuns/melarka)"
}
