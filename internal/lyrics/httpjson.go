package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aaronsuns/lark-server/internal/buildinfo"
)

// UserAgent identifies Melarka to lyrics services (LRCLIB asks clients to).
var UserAgent = buildinfo.UserAgent()

// maxBody caps how much of a provider response is read.
const maxBody = 2 << 20

// errNotFound is a provider's HTTP 404: a clean "not here", not a failure.
var errNotFound = errors.New("not found")

// statusError is any other non-2xx answer.
type statusError struct {
	Host string
	Code int
}

func (e *statusError) Error() string { return fmt.Sprintf("%s: HTTP %d", e.Host, e.Code) }

// clientError reports whether err is a 4xx answer (404 included).
func clientError(err error) bool {
	var se *statusError
	return errors.Is(err, errNotFound) || (errors.As(err, &se) && se.Code >= 400 && se.Code < 500)
}

var defaultHTTP = &http.Client{Timeout: 10 * time.Second}

func client(d HTTPDoer) HTTPDoer {
	if d == nil {
		return defaultHTTP
	}
	return d
}

// getJSON performs one request and decodes its JSON body into v. It sets
// Lark's User-Agent (headers may override it), reads at most 2 MiB, maps 404
// to errNotFound and any other non-2xx to an error, and reports a body that
// isn't the expected JSON (an HTML error page, say) as "bad response".
func getJSON(ctx context.Context, doer HTTPDoer, method, url string, body io.Reader, headers map[string]string, v any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	for k, val := range headers {
		req.Header.Set(k, val)
	}
	resp, err := client(doer).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &statusError{Host: req.URL.Host, Code: resp.StatusCode}
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("bad response: %w", err)
	}
	return nil
}
