package config

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// anyEmail matches any address. Tracked files may only use the documented placeholders.
// (users.noreply.github.com is deliberately not allowed: it can embed a work-account id.)
var anyEmail = regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9-]+(\.[a-z0-9-]+)*\.[a-z]{2,}`)
var allowedEmail = regexp.MustCompile(`(?i)@example\.(com|org|net)$|^pass@www\.youtube\.com$`)

func TestNoEmailAddresses(t *testing.T) {
	out, err := exec.Command("git", "-C", "../..", "ls-files", "-z").Output()
	if err != nil {
		t.Skip("not a git checkout") // the Docker build has no .git
	}
	files := strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 })
	if len(files) < 100 {
		t.Fatalf("only %d tracked files: wrong directory?", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join("../..", f))
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			continue // unreadable or binary
		}
		for _, m := range anyEmail.FindAll(b, -1) {
			if !allowedEmail.Match(m) {
				t.Errorf("%s contains an email address (%d bytes) — use an @example.com placeholder", f, len(m))
			}
		}
	}
}

func TestKugouFixtureKeyIsFake(t *testing.T) {
	b, err := os.ReadFile("../../internal/lyrics/testdata/kugou_candidates.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"accesskey":"FAKEACCESSKEY0000000000000000000"`) {
		t.Fatal("kugou fixture must carry the obviously fake access key")
	}
}
