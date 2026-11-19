// Package updatecheck tells admins that a newer Melarka release exists. Once a
// day the server asks GitHub for the latest release (a plain GET of the public
// releases API: no id, no version, no account, only a generic User-Agent) and
// keeps the answer in memory. The answer is shown to admins only (Admin →
// System). On by default; LARK_UPDATE_CHECK=off means nothing is ever sent.
// A failed check is silent: the old answer stays and the next hourly tick tries
// again.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultURL is GitHub's "latest release" endpoint for Melarka. It never
// returns drafts or prereleases.
const DefaultURL = "https://api.github.com/repos/aaronsuns/melarka/releases/latest"

// ReleasesURL is where the release notes live; used when GitHub gives no usable link.
const ReleasesURL = "https://github.com/aaronsuns/melarka/releases"

// Interval is how often an enabled server checks (plus up to MaxJitter).
const Interval = 24 * time.Hour

// MaxJitter spreads installs over the day so they do not all call at once.
const MaxJitter = 2 * time.Hour

// checkEvery is how often Run looks whether a check is due.
const checkEvery = time.Hour

// userAgent is generic on purpose: no version, no install detail.
const userAgent = "Melarka (+https://github.com/aaronsuns/melarka)"

// Result is what admins see.
type Result struct {
	Enabled bool `json:"enabled"`
	// Current is the running version ("dev" for local builds).
	Current string `json:"current"`
	// Latest is the newest release's version without the leading v; empty before the first successful check.
	Latest string `json:"latest"`
	URL    string `json:"url"`
	// Newer is true only when both versions are real releases and Latest is higher.
	Newer bool `json:"newer"`
	// CheckedAt is the Unix time of the last successful check; 0 if none yet.
	CheckedAt int64 `json:"checked_at"`
}

type Service struct {
	URL     string // DefaultURL when empty
	Version string // the running version
	Client  *http.Client
	Now     func() time.Time
	Log     *slog.Logger
	// Off is LARK_UPDATE_CHECK=off: nothing is ever sent.
	Off bool
	// Jitter returns the extra wait added to Interval; random up to MaxJitter when nil.
	Jitter func() time.Duration

	mu      sync.Mutex
	latest  string
	url     string
	checked time.Time
	nextDue time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Status is the cached answer; it never makes a request.
func (s *Service) Status() Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := Result{Enabled: !s.Off, Current: s.Version, Latest: s.latest, URL: s.url}
	if !s.checked.IsZero() {
		r.CheckedAt = s.checked.Unix()
	}
	r.Newer = !s.Off && s.latest != "" && Newer(s.latest, s.Version)
	if r.URL == "" {
		r.URL = ReleasesURL
	}
	return r
}

type release struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Check asks GitHub once and stores the answer. A draft, a prerelease or a tag
// that is not X.Y.Z is ignored without error (the old answer stays).
func (s *Service) Check(ctx context.Context) error {
	if s.Off {
		return nil
	}
	url := s.URL
	if url == "" {
		url = DefaultURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return fmt.Errorf("update check: %s", res.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&rel); err != nil {
		return err
	}
	if rel.Draft || rel.Prerelease {
		return nil
	}
	v := strings.TrimPrefix(rel.TagName, "v")
	if p, ok := parse(v); !ok || p.pre != "" {
		return nil
	}
	link := rel.HTMLURL
	// The page links to it, so only ever accept a link into this project.
	if !strings.HasPrefix(link, "https://github.com/aaronsuns/melarka/") {
		link = ReleasesURL
	}
	s.mu.Lock()
	s.latest, s.url, s.checked = v, link, s.now()
	s.mu.Unlock()
	return nil
}

// CheckIfDue checks when none has succeeded yet or the next one is due. A
// failure does not move the due time, so the next tick tries again.
func (s *Service) CheckIfDue(ctx context.Context) error {
	if s.Off {
		return nil
	}
	s.mu.Lock()
	due := s.nextDue.IsZero() || !s.now().Before(s.nextDue)
	s.mu.Unlock()
	if !due {
		return nil
	}
	if err := s.Check(ctx); err != nil {
		return err
	}
	jit := time.Duration(rand.Int64N(int64(MaxJitter)))
	if s.Jitter != nil {
		jit = s.Jitter()
	}
	s.mu.Lock()
	s.nextDue = s.now().Add(Interval + jit)
	s.mu.Unlock()
	return nil
}

// Run checks hourly whether a check is due; the first one is made at start.
func (s *Service) Run(ctx context.Context) {
	if s.Off {
		return
	}
	t := time.NewTicker(checkEvery)
	defer t.Stop()
	for {
		if err := s.CheckIfDue(ctx); err != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.Debug("update check failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

var errNotRelease = errors.New("not a release version")

type version struct {
	n   [3]int
	pre string
}

// parse reads "X.Y.Z" or "X.Y.Z-pre" (build metadata after + is dropped).
func parse(v string) (version, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	var out version
	if i := strings.IndexByte(v, '-'); i >= 0 {
		out.pre = v[i+1:]
		v = v[:i]
		if out.pre == "" {
			return out, false
		}
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return out, false
		}
		out.n[i] = n
	}
	return out, true
}

// Compare returns -1, 0 or 1 for a < b, a == b, a > b by semantic versioning
// (a prerelease is lower than its release). It fails for anything that is not
// X.Y.Z[-pre], such as "dev".
func Compare(a, b string) (int, error) {
	va, ok1 := parse(a)
	vb, ok2 := parse(b)
	if !ok1 || !ok2 {
		return 0, errNotRelease
	}
	for i := range va.n {
		if va.n[i] != vb.n[i] {
			if va.n[i] < vb.n[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	switch {
	case va.pre == vb.pre:
		return 0, nil
	case va.pre == "":
		return 1, nil
	case vb.pre == "":
		return -1, nil
	case va.pre < vb.pre:
		return -1, nil
	}
	return 1, nil
}

// Newer reports whether latest is a higher release than current. Dev and
// unknown builds are never told they are behind, and a prerelease is never
// offered as the update.
func Newer(latest, current string) bool {
	if l, ok := parse(latest); !ok || l.pre != "" {
		return false
	}
	c, err := Compare(latest, current)
	return err == nil && c > 0
}
