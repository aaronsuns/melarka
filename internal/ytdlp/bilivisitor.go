package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// BiliSPIURL is Bilibili's public visitor-id endpoint: the one a browser
// calls on its first visit to get its buvid3/buvid4 cookies.
const BiliSPIURL = "https://api.bilibili.com/x/frontend/finger/spi"

// BiliVisitor holds one real Bilibili visitor id for yt-dlp's Bilibili
// searches. Without it yt-dlp makes up a random buvid3 per run, and
// Bilibili's anti-bot check refuses about half of those searches (HTTP 412).
// The id is fetched once and reused; Forget drops it after a refusal, so the
// next search asks for a new one. Concurrent use is safe.
type BiliVisitor struct {
	HTTP *http.Client // nil: http.DefaultClient (honours HTTPS_PROXY)
	URL  string       // "": BiliSPIURL
	Dir  string       // where the per-run cookie files go; "": os.TempDir()

	mu  sync.Mutex
	jar string // Netscape cookie file contents; "" until fetched
}

const biliVisitorTimeout = 5 * time.Second

// CookieFile writes the visitor id to a fresh cookie file for one yt-dlp run
// (yt-dlp rewrites its --cookies file on exit, so runs never share one) and
// returns its path and a cleanup. An error means no id could be had: the
// caller runs without --cookies.
func (v *BiliVisitor) CookieFile(ctx context.Context) (string, func(), error) {
	jar, err := v.cookies(ctx)
	if err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp(v.Dir, "lark-bili-*.txt")
	if err != nil {
		return "", nil, err
	}
	_, werr := f.WriteString(jar)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(f.Name())
		return "", nil, werr
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// Forget drops the visitor id: Bilibili refused a search made with it.
func (v *BiliVisitor) Forget() {
	v.mu.Lock()
	v.jar = ""
	v.mu.Unlock()
}

func (v *BiliVisitor) cookies(ctx context.Context) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.jar != "" {
		return v.jar, nil
	}
	b3, b4, err := v.fetch(ctx)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	expires := now + 365*24*3600
	var sb strings.Builder
	sb.WriteString("# Netscape HTTP Cookie File\n")
	for _, kv := range [][2]string{{"buvid3", b3}, {"buvid4", b4}, {"b_nut", fmt.Sprint(now)}} {
		fmt.Fprintf(&sb, ".bilibili.com\tTRUE\t/\tFALSE\t%d\t%s\t%s\n", expires, kv[0], kv[1])
	}
	v.jar = sb.String()
	return v.jar, nil
}

func (v *BiliVisitor) fetch(ctx context.Context) (b3, b4 string, err error) {
	ctx, cancel := context.WithTimeout(ctx, biliVisitorTimeout)
	defer cancel()
	u := v.URL
	if u == "" {
		u = BiliSPIURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")
	hc := v.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("bilibili visitor id: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			B3 string `json:"b_3"`
			B4 string `json:"b_4"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", fmt.Errorf("bilibili visitor id: %w", err)
	}
	if body.Code != 0 || !cookieSafe(body.Data.B3) || !cookieSafe(body.Data.B4) {
		return "", "", fmt.Errorf("bilibili visitor id: code %d", body.Code)
	}
	return body.Data.B3, body.Data.B4, nil
}

// cookieSafe: a value that can't break the cookie file's tab/newline format.
func cookieSafe(s string) bool {
	return s != "" && len(s) <= 256 && !strings.ContainsAny(s, "\t\r\n ")
}
