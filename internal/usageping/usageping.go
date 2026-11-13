// Package usageping is the anonymous "count this install" ping. Melarka has no
// analytics; this tells the project that the install is running. About once a
// week the server sends a random install id, its version and a few coarse
// numbers (see Payload), and nothing else, to the Melarka website, which only
// counts. On by default (migration 0031); an admin turns it off in Admin →
// System, or LARK_USAGE_PING=off keeps it off. Turning it off deletes the id,
// so turning it on again counts as a new install.
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
	"os"
	"runtime"
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
	// Off is LARK_USAGE_PING=off: nothing is ever sent, the id is deleted
	// when Run starts, and the admin switch cannot turn it on.
	Off bool
	// InContainer reports whether the server runs in a container; a check
	// for /.dockerenv when nil.
	InContainer func() bool

	wakeOnce sync.Once
	wake     chan struct{}
}

func (s *Service) wakeCh() chan struct{} {
	s.wakeOnce.Do(func() { s.wake = make(chan struct{}, 1) })
	return s.wake
}

// ErrOff is returned when an admin tries to turn the ping on while
// LARK_USAGE_PING=off.
var ErrOff = errors.New("usage ping is off by LARK_USAGE_PING")

// Payload is everything a ping sends. Users and Tracks are ranges, never
// exact counts.
type Payload struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Container bool   `json:"container"`
	Users     string `json:"users"`
	Tracks    string `json:"tracks"`
}

// UsersRange and TracksRange turn a count into the coarse range a ping sends.
func UsersRange(n int) string {
	switch {
	case n <= 1:
		return "1"
	case n <= 5:
		return "2-5"
	case n <= 20:
		return "6-20"
	}
	return "21+"
}

func TracksRange(n int) string {
	switch {
	case n == 0:
		return "0"
	case n < 100:
		return "1-99"
	case n < 1000:
		return "100-999"
	case n < 10000:
		return "1000-9999"
	}
	return "10000+"
}

func (s *Service) payload(ctx context.Context, id string) (Payload, error) {
	var users, tracks int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		return Payload{}, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tracks WHERE status != 'trashed'`).Scan(&tracks); err != nil {
		return Payload{}, err
	}
	inContainer := s.InContainer
	if inContainer == nil {
		inContainer = func() bool { _, err := os.Stat("/.dockerenv"); return err == nil }
	}
	return Payload{ID: id, Version: s.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Container: inContainer(),
		Users: UsersRange(users), Tracks: TracksRange(tracks)}, nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) Enabled(ctx context.Context) (bool, error) {
	if s.Off {
		return false, nil
	}
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
	if s.Off {
		return ErrOff
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
	if s.Off {
		return false, nil
	}
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
	p, err := s.payload(ctx, id)
	if err != nil {
		return false, err
	}
	body, _ := json.Marshal(p)
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
// With Off it deletes the id first and then never sends.
func (s *Service) Run(ctx context.Context) {
	if s.Off {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM usage_ping`); err != nil && s.Log != nil {
			s.Log.Warn("usage ping: delete id", "err", err)
		}
		return
	}
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
