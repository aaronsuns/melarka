// Package tags stores track tags from several sources, with manual edits
// taking precedence over automatic ones.
package tags

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Tag struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type Store struct {
	DB *sql.DB
	// OnChange, when set, is called after every successful Replace (wired to
	// library.Store.Reindex so tag words become searchable).
	OnChange func(ctx context.Context, trackID int64) error
}

// ErrUnknownSource is wrapped (with %w) into the error Replace returns for an
// unrecognized source, so callers can recognize it as a validation error
// (400) rather than an internal one (500) without string-matching the
// message.
var ErrUnknownSource = errors.New("unknown tag source")

var sources = map[string]bool{"folder_rule": true, "lastfm": true, "musicbrainz": true, "agent": true, "manual": true}
var kinds = map[string]bool{"genre": true, "mood": true, "scene": true, "era": true, "language": true, "other": true}

func tagID(ctx context.Context, tx *sql.Tx, t Tag) (int64, error) {
	kind := t.Kind
	if !kinds[kind] {
		kind = "other"
	}
	if e, ok := Vocabulary().Lookup(t.Name); ok && e.Slug == t.Name {
		kind = e.Kind // a vocabulary tag always carries its vocabulary kind
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tags(name,kind) VALUES (?,?) ON CONFLICT(name) DO UPDATE SET kind=excluded.kind WHERE excluded.kind!='other'`, t.Name, kind); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE name=?`, t.Name).Scan(&id)
	return id, err
}

func clean(ts []Tag) []Tag {
	seen := map[string]bool{}
	out := []Tag{}
	for _, t := range ts {
		t.Name = strings.TrimSpace(t.Name)
		k := strings.ToLower(t.Name)
		if t.Name == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	return out
}

// Replace sets the tags a source assigns to a track and notifies OnChange.
func (s *Store) Replace(ctx context.Context, trackID int64, source string, in []Tag) error {
	if err := s.replace(ctx, trackID, source, in); err != nil {
		return err
	}
	if s.OnChange != nil {
		return s.OnChange(ctx, trackID)
	}
	return nil
}

func (s *Store) replace(ctx context.Context, trackID int64, source string, in []Tag) error {
	if !sources[source] {
		return fmt.Errorf("%w: %q", ErrUnknownSource, source)
	}
	want := clean(in)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if source == "manual" {
		keep := map[int64]bool{}
		for _, t := range want {
			id, err := tagID(ctx, tx, t)
			if err != nil {
				return err
			}
			keep[id] = true
			if _, err := tx.ExecContext(ctx, `INSERT INTO track_tags(track_id,tag_id,source,removed) VALUES (?,?,'manual',0)
				ON CONFLICT(track_id,tag_id,source) DO UPDATE SET removed=0`, trackID, id); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT tag_id FROM track_tags WHERE track_id=? AND removed=0`, trackID)
		if err != nil {
			return err
		}
		var drop []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if !keep[id] {
				drop = append(drop, id)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, id := range drop {
			if _, err := tx.ExecContext(ctx, `DELETE FROM track_tags WHERE track_id=? AND tag_id=?`, trackID, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO track_tags(track_id,tag_id,source,removed) VALUES (?,?,'manual',1)`, trackID, id); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM track_tags WHERE track_id=? AND source=?`, trackID, source); err != nil {
		return err
	}
	for _, t := range want {
		id, err := tagID(ctx, tx, t)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO track_tags(track_id,tag_id,source,removed)
			SELECT ?,?,?,0 WHERE NOT EXISTS (SELECT 1 FROM track_tags WHERE track_id=? AND tag_id=? AND source='manual' AND removed=1)`,
			trackID, id, source, trackID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ForTrack(ctx context.Context, trackID int64) ([]Tag, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT g.name, g.kind FROM track_tags tt JOIN tags g ON g.id=tt.tag_id
		WHERE tt.track_id=? AND tt.removed=0 ORDER BY g.kind, g.name`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.Name, &t.Kind); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PendingForTagging lists the tracks no agent batch has covered yet, kept
// tracks first, then newest first.
func (s *Store) PendingForTagging(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id FROM tracks t LEFT JOIN tagging_state s ON s.track_id=t.id
		WHERE t.status IN ('kept','pending') AND t.missing_since IS NULL AND t.broken=0 AND s.agent_at IS NULL
		ORDER BY (t.status='kept') DESC, t.added_at DESC, t.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkAgentDone records that an agent batch covered these tracks (even with
// no tags, so "nothing fits" is remembered).
func (s *Store) MarkAgentDone(ctx context.Context, ids []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tagging_state(track_id, agent_at) VALUES (?,?)
			ON CONFLICT(track_id) DO UPDATE SET agent_at=excluded.agent_at`, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
