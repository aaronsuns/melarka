package artwork

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aaronsuns/lark-server/internal/lyrics"
)

const maxImage = 8 << 20

var defaultHTTP = &http.Client{Timeout: 15 * time.Second, CheckRedirect: httpsRedirects}

// httpsRedirects follows at most 3 redirects, and only to https.
func httpsRedirects(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("redirect to %s://%s: not https", req.URL.Scheme, req.URL.Host)
	}
	return nil
}

// fetch downloads an https image (≤ 8 MiB, Content-Type image/*) into a temp
// file in s.Dir and returns its path; the caller removes it.
func (s *Service) fetch(ctx context.Context, url string) (string, error) {
	if !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("image %q: not https", url)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", lyrics.UserAgent)
	doer := s.HTTP
	if doer == nil {
		doer = defaultHTTP
	} else if c, ok := doer.(*http.Client); ok { // an injected client follows the same rules
		cc := *c
		cc.CheckRedirect = httpsRedirects
		doer = &cc
	}
	resp, err := doer.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.Request != nil && resp.Request.URL.Scheme != "https" { // any other HTTPDoer
		return "", fmt.Errorf("image %s: redirected off https", req.URL.Host)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image %s: HTTP %d", req.URL.Host, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		return "", fmt.Errorf("image %s: content type %q", req.URL.Host, ct)
	}
	f, err := os.CreateTemp(s.Dir, ".dl-*")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxImage+1))
	f.Close()
	if err == nil && n > maxImage {
		err = fmt.Errorf("image %s: larger than %d bytes", req.URL.Host, maxImage)
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
