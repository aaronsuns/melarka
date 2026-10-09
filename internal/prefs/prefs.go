// Package prefs stores per-user preferences (UI language, what the player
// does when it opens, whether lyric lines go to the car/lock-screen title).
package prefs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrInvalid marks a caller-input validation failure (as opposed to a
// storage error): handlers map it to 400, not 500.
var ErrInvalid = errors.New("invalid preferences")

var languages = map[string]bool{"en": true, "zh-Hans": true, "zh-Hant": true, "sv": true}
var onOpen = map[string]bool{"shuffle_favorites": true, "resume": true, "nothing": true}

// Prefs is a user's saved preferences. Language is nil when the user wants
// the server default rather than a pinned language.
type Prefs struct {
	Language *string `json:"language"`
	OnOpen   string  `json:"on_open"`
	// CarLyrics: put the current synced lyric line into the media-session
	// title. Always set in what Get/Put return (default true); nil in a Put
	// (a client that predates the field) keeps the stored value.
	CarLyrics *bool `json:"car_lyrics"`
}

type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() int64 {
	if s.Now != nil {
		return s.Now().Unix()
	}
	return time.Now().Unix()
}

func (s *Store) Get(ctx context.Context, userID int64) (Prefs, error) {
	car := true
	p := Prefs{OnOpen: "shuffle_favorites", CarLyrics: &car}
	var lang sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT language, on_open, car_lyrics FROM user_prefs WHERE user_id=?`, userID).Scan(&lang, &p.OnOpen, &car)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if lang.Valid {
		p.Language = &lang.String
	}
	return p, err
}

// Put replaces a user's preferences wholesale and returns them as stored.
func (s *Store) Put(ctx context.Context, userID int64, p Prefs) (Prefs, error) {
	if !onOpen[p.OnOpen] || (p.Language != nil && !languages[*p.Language]) {
		return Prefs{}, ErrInvalid
	}
	var lang any
	if p.Language != nil {
		lang = *p.Language
	}
	var car any
	if p.CarLyrics != nil {
		car = *p.CarLyrics
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO user_prefs(user_id,language,on_open,car_lyrics,updated_at) VALUES (?1,?2,?3,COALESCE(?4,1),?5)
		ON CONFLICT(user_id) DO UPDATE SET language=excluded.language, on_open=excluded.on_open,
		car_lyrics=COALESCE(?4, user_prefs.car_lyrics), updated_at=excluded.updated_at`,
		userID, lang, p.OnOpen, car, s.now()); err != nil {
		return Prefs{}, err
	}
	return s.Get(ctx, userID)
}
