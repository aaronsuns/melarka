// Package usageping is the opt-in "count this install" ping. Melarka has no
// analytics; an admin can turn this on (Admin → System) to tell the project the
// install is running. About once a week the server then sends a random install
// id and its version, and nothing else, to the Melarka website, which only
// counts installs. Off by default; turning it off deletes the id, so turning
// it on again counts as a new install.
package usageping

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// DefaultURL is where pings go: the website's worker (site/worker.js).
const DefaultURL = "https://melarka.zexa.se/api/ping"

// Interval is how often an enabled install pings.
const Interval = 7 * 24 * time.Hour

// checkEvery is how often Run looks whether a ping is due.
const checkEvery = time.Hour

type Service struct {
	DB      *sql.DB
	URL     string // DefaultURL when empty
	Version string
	Client  *http.Client     // a 15-second client when nil
	Now     func() time.Time // time.Now when nil
	Log     *slog.Logger

	wakeOnce sync.Once
	wake     chan struct{}
}

func (s *Service) wakeCh() chan struct{} {
	s.wakeOnce.Do(func() { s.wake = make(chan struct{}, 1) })
	return s.wake
}

// Payload is everything a ping sends.
type Payload struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) Enabled(ctx context.Context) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_ping`).Scan(&n)
	return n > 0, err
}

// SetEnabled turns the ping on (with a new random id, sent soon) or off
// (deleting the id). Turning it on when it is already on keeps the id.
func (s *Service) SetEnabled(ctx context.Context, on bool) error {
	if !on {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM usage_ping`)
		return err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO usage_ping(id, install_id, last_sent_at) VALUES (1, ?, 0)`,
		hex.EncodeToString(b)); err != nil {
		return err
	}
	select {
	case s.wakeCh() <- struct{}{}:
	default:
	}
	return nil
}

// SendIfDue pings when the ping is on and the last one is older than Interval.
func (s *Service) SendIfDue(ctx context.Context) (bool, error) {
	var id string
	var last int64
	err := s.DB.QueryRowContext(ctx, `SELECT install_id, last_sent_at FROM usage_ping WHERE id = 1`).Scan(&id, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := s.now()
	if last > 0 && now.Sub(time.Unix(last, 0)) < Interval {
		return false, nil
	}
	body, _ := json.Marshal(Payload{ID: id, Version: s.Version})
	url := s.URL
	if url == "" {
		url = DefaultURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return false, err
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	res.Body.Close()
	if res.StatusCode/100 != 2 {
		return false, fmt.Errorf("usage ping: %s", res.Status)
	}
	// Only if the ping is still on: turning it off while the request ran must not bring the row back.
	_, err = s.DB.ExecContext(ctx, `UPDATE usage_ping SET last_sent_at = ? WHERE id = 1 AND install_id = ?`, now.Unix(), id)
	return err == nil, err
}

// Run checks hourly (and right after the ping is turned on) whether a ping is due.
// A failed ping is tried again at the next check.
func (s *Service) Run(ctx context.Context) {
	wake := s.wakeCh()
	t := time.NewTicker(checkEvery)
	defer t.Stop()
	for {
		if _, err := s.SendIfDue(ctx); err != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.Debug("usage ping failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-wake:
		}
	}
}
