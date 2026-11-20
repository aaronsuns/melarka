// Package videosrc decides which video sources (YouTube, Bilibili) a server
// offers. LARK_VIDEO_SOURCES gives the default; once an admin saves a choice
// in Admin → System, the database wins. A source that is off is refused by
// the API and never contacted, by people's requests or background work.
package videosrc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// ErrUnknown: a source name Melarka does not know.
var ErrUnknown = errors.New("videosrc: unknown source")

// Parse reads a comma-separated list ("youtube,bilibili"), trimmed, any case,
// de-duplicated with the order kept. "none" is the empty list; an empty
// string, an empty item or an unknown name is an error.
func Parse(s string) ([]ytdlp.Source, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "none") {
		return []ytdlp.Source{}, nil
	}
	out := []ytdlp.Source{}
	for _, part := range strings.Split(s, ",") {
		src := ytdlp.Source(strings.ToLower(strings.TrimSpace(part)))
		if !slices.Contains(ytdlp.Sources, src) {
			return nil, fmt.Errorf("%w %q (want youtube, bilibili or none)", ErrUnknown, part)
		}
		if !slices.Contains(out, src) {
			out = append(out, src)
		}
	}
	return out, nil
}

// Service answers which sources are on. A nil *Service means YouTube only
// (the behaviour before Bilibili existed).
type Service struct {
	DB      *sql.DB
	Default []ytdlp.Source // from LARK_VIDEO_SOURCES
}

// List returns the sources in effect and who set them: "admin" when an admin
// saved a choice, else "env".
func (s *Service) List(ctx context.Context) ([]ytdlp.Source, string, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT sources FROM video_sources WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return slices.Clone(s.defaults()), "env", nil
	}
	if err != nil {
		return nil, "", err
	}
	if raw == "" {
		return []ytdlp.Source{}, "admin", nil
	}
	srcs, err := Parse(raw)
	if err != nil {
		return nil, "", err
	}
	return srcs, "admin", nil
}

func (s *Service) defaults() []ytdlp.Source {
	if s.Default == nil {
		return []ytdlp.Source{ytdlp.SourceYouTube}
	}
	return s.Default
}

// Enabled reports whether src is on. A database error counts as the default.
func (s *Service) Enabled(ctx context.Context, src ytdlp.Source) bool {
	if s == nil {
		return src == ytdlp.SourceYouTube
	}
	srcs, _, err := s.List(ctx)
	if err != nil {
		srcs = s.defaults()
	}
	return slices.Contains(srcs, src)
}

// Set saves an admin's choice (an empty list turns video features off).
func (s *Service) Set(ctx context.Context, srcs []ytdlp.Source) error {
	names := make([]string, 0, len(srcs))
	for _, src := range srcs {
		if !slices.Contains(ytdlp.Sources, src) {
			return fmt.Errorf("%w %q", ErrUnknown, src)
		}
		if !slices.Contains(names, string(src)) {
			names = append(names, string(src))
		}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO video_sources(id, sources) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET sources = excluded.sources`, strings.Join(names, ","))
	return err
}
