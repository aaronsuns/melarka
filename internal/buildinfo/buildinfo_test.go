package buildinfo

import (
	"strings"
	"testing"
)

func TestVersionDefaultsToDev(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("Version = %q, want dev (it is set only by -ldflags at release)", Version)
	}
}

func TestUserAgent(t *testing.T) {
	ua := UserAgent()
	if !strings.HasPrefix(ua, "Melarka/dev") {
		t.Fatalf("UserAgent() = %q", ua)
	}
	if ua != "Melarka/dev (+https://github.com/aaronsuns/melarka)" {
		t.Fatalf("UserAgent() = %q", ua)
	}
}
